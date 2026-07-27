BEGIN {
  ENVIRON["TZ"] = server_tz
  month["Jan"] = 1; month["Feb"] = 2; month["Mar"] = 3; month["Apr"] = 4
  month["May"] = 5; month["Jun"] = 6; month["Jul"] = 7; month["Aug"] = 8
  month["Sep"] = 9; month["Oct"] = 10; month["Nov"] = 11; month["Dec"] = 12
  record_epoch = -1
  record_text = ""
  scanned_bytes = 0
  filtered_bytes = 0
  filtered_records = 0
  warning_count = 0
  selected_parser = ""
}

function warn(code) {
  warning_count++
  warning_codes[code]++
}

function select_parser(name) {
  if (selected_parser == "") selected_parser = name
  else if (selected_parser != name) selected_parser = "mixed"
}

function flush_record(    length_bytes) {
  if (record_text != "" && record_epoch >= since_epoch && record_epoch < until_epoch) {
    printf "%s", record_text
    length_bytes = length(record_text)
    filtered_bytes += length_bytes
    filtered_records++
    select_parser(record_parser)
  }
  record_text = ""
  record_epoch = -1
  record_parser = ""
}

function utc_epoch(year, mon, day, hour, minute, second, offset_sign, offset_hour, offset_minute,    epoch, offset) {
  epoch = mktime(sprintf("%04d %02d %02d %02d %02d %02d", year, mon, day, hour, minute, second), 1)
  if (epoch < 0) return -1
  offset = offset_hour * 3600 + offset_minute * 60
  if (offset_sign == "+") epoch -= offset
  else if (offset_sign == "-") epoch += offset
  return epoch
}

function local_epoch(year, mon, day, hour, minute, second) {
  if (server_utc_offset_seconds != "") {
    return utc_epoch(year, mon, day, hour, minute, second, "+", int(server_utc_offset_seconds / 3600), int((server_utc_offset_seconds % 3600) / 60))
  }
  return mktime(sprintf("%04d %02d %02d %02d %02d %02d", year, mon, day, hour, minute, second))
}

function parse_iso(value,    parts, timezone, sign, hours, minutes) {
  if (value !~ /^[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9][T ][0-9][0-9]:[0-9][0-9]:[0-9][0-9]/) return -1
  parts[1] = substr(value, 1, 4) + 0
  parts[2] = substr(value, 6, 2) + 0
  parts[3] = substr(value, 9, 2) + 0
  parts[4] = substr(value, 12, 2) + 0
  parts[5] = substr(value, 15, 2) + 0
  parts[6] = substr(value, 18, 2) + 0
  if (match(value, /(Z|[+-][0-9][0-9]:?[0-9][0-9])$/, timezone)) {
    if (timezone[1] == "Z") return utc_epoch(parts[1], parts[2], parts[3], parts[4], parts[5], parts[6], "+", 0, 0)
    sign = substr(timezone[1], 1, 1)
    hours = substr(timezone[1], 2, 2) + 0
    minutes = substr(timezone[1], length(timezone[1]) - 1, 2) + 0
    return utc_epoch(parts[1], parts[2], parts[3], parts[4], parts[5], parts[6], sign, hours, minutes)
  }
  return local_epoch(parts[1], parts[2], parts[3], parts[4], parts[5], parts[6])
}

function parse_timestamp(line,    epoch_value, json, access, spring, nginx_error, numeric, iso_match) {
  parsed_parser = ""
  if (match(line, /"(timestamp|time|@timestamp|ts)"[[:space:]]*:[[:space:]]*"([^"]+)"/, json)) {
    epoch_value = parse_iso(json[2])
    if (epoch_value >= 0) { parsed_parser = "json"; return epoch_value }
  }
  if (match(line, /"ts"[[:space:]]*:[[:space:]]*([0-9]{10}|[0-9]{13})/, numeric)) {
    epoch_value = numeric[1] + 0
    if (length(numeric[1]) == 13) epoch_value = int(epoch_value / 1000)
    parsed_parser = "json"
    return epoch_value
  }
  if (match(line, /^([0-9]{4})-([0-9]{2})-([0-9]{2})[[:space:]]([0-9]{2}):([0-9]{2}):([0-9]{2})[,.][0-9]+/, spring)) {
    parsed_parser = "spring"
    return local_epoch(spring[1], spring[2], spring[3], spring[4], spring[5], spring[6])
  }
  if (match(line, /^([0-9]{4})\/([0-9]{2})\/([0-9]{2})[[:space:]]([0-9]{2}):([0-9]{2}):([0-9]{2})/, nginx_error)) {
    parsed_parser = "nginx-error"
    return local_epoch(nginx_error[1], nginx_error[2], nginx_error[3], nginx_error[4], nginx_error[5], nginx_error[6])
  }
  if (match(line, /\[([0-9]{2})\/([A-Z][a-z]{2})\/([0-9]{4}):([0-9]{2}):([0-9]{2}):([0-9]{2})[[:space:]]([+-])([0-9]{2})([0-9]{2})\]/, access)) {
    if (!(access[2] in month)) return -1
    parsed_parser = "nginx-access"
    return utc_epoch(access[3], month[access[2]], access[1], access[4], access[5], access[6], access[7], access[8], access[9])
  }
  if (match(line, /^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}([^[:space:]]*)/, iso_match)) {
    epoch_value = parse_iso(iso_match[0])
    if (epoch_value >= 0) { parsed_parser = "iso"; return epoch_value }
  }
  return -1
}

function is_continuation(line) {
  return line ~ /^[[:space:]]+/ || line ~ /^Caused by:/ || line ~ /^Suppressed:/ || line ~ /^\.\.\. [0-9]+ more$/
}

{
  scanned_bytes += length($0) + 1
  epoch = parse_timestamp($0)
  if (epoch >= 0) {
    flush_record()
    record_epoch = epoch
    record_parser = parsed_parser
    record_text = $0 ORS
  } else if (is_continuation($0)) {
    if (record_text != "") record_text = record_text $0 ORS
    else warn("orphan-continuation")
  } else {
    flush_record()
    warn("timestamp-unrecognized")
  }
}

END {
  if (initial_ended_newline + 0 == 1) flush_record()
  else {
    if (scanned_bytes > 0) scanned_bytes--
    if (record_text != "") warn("active-tail-deferred")
    record_text = ""
  }
  if (selected_parser == "") selected_parser = "none"
  printf "AIFAR_DIAG_FILTER_V1\t%s\t%d\t%d\t%d\t%d\n", selected_parser, scanned_bytes, filtered_bytes, filtered_records, warning_count > summary_path
  close(summary_path)
  PROCINFO["sorted_in"] = "@ind_str_asc"
  for (code in warning_codes) printf "%s\t%d\n", code, warning_codes[code] > warning_path
  close(warning_path)
}
