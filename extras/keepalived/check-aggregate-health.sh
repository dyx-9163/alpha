#!/usr/bin/env bash
set -Eeuo pipefail

readonly DEFAULT_HEALTH_URL_FILE="/aifar/apps/keepalived/etc/keepalived/keepalived-health-url"
readonly MAX_HEALTH_RESPONSE_BYTES=65536

validate_health_url_shape() {
    local url="$1" remainder="" authority="" port=""
    [[ "$url" =~ ^https?://[A-Za-z0-9.-]+(:[0-9]{1,5})?(/[A-Za-z0-9._~/?\&=%-]*)?$ ]] || return 1
    remainder="${url#*://}"
    authority="${remainder%%/*}"
    if [[ "$authority" == *:* ]]; then
        port="${authority##*:}"
        [[ "$port" =~ ^[0-9]+$ ]] && ((10#$port >= 1 && 10#$port <= 65535)) || return 1
    fi
}

read_health_url() {
    local file="$1"
    local -a lines=()
    [[ -r "$file" ]] || return 1
    mapfile -t lines <"$file"
    [[ "${#lines[@]}" -eq 1 && -n "${lines[0]}" ]] || return 1
    validate_health_url_shape "${lines[0]}" || return 1
    printf '%s\n' "${lines[0]}"
}

json_up_is_true() {
    python3 -c 'import json, sys
try:
    value = json.load(sys.stdin)
except (TypeError, ValueError):
    raise SystemExit(1)
raise SystemExit(0 if isinstance(value, dict) and value.get("up") is True else 1)'
}

check_health() {
    local url_file="$1"
    local url=""
    local response_file=""
    local http_status=""
    local response_size=0
    local result=1

    url="$(read_health_url "$url_file")" || return 1
    response_file="$(mktemp "${TMPDIR:-/tmp}/keepalived-health.XXXXXX")" || return 1
    if http_status="$(curl \
        --silent \
        --show-error \
        --connect-timeout 1 \
        --max-time 2 \
        --max-redirs 0 \
        --max-filesize "$MAX_HEALTH_RESPONSE_BYTES" \
        --output "$response_file" \
        --write-out '%{http_code}' \
        -- "$url")" &&
       [[ "$http_status" =~ ^[0-9]{3}$ ]] &&
       ((10#$http_status >= 200 && 10#$http_status <= 299)); then
        response_size="$(stat -c '%s' "$response_file" 2>/dev/null || printf '%s' $((MAX_HEALTH_RESPONSE_BYTES + 1)))"
        if [[ "$response_size" =~ ^[0-9]+$ ]] &&
           ((response_size <= MAX_HEALTH_RESPONSE_BYTES)) &&
           json_up_is_true <"$response_file"; then
            result=0
        fi
    fi
    rm -f -- "$response_file" || return 1
    return "$result"
}

main() {
    [[ "$#" -eq 0 ]] || return 2
    check_health "$DEFAULT_HEALTH_URL_FILE"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
