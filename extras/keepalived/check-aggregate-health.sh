#!/usr/bin/env bash
set -Eeuo pipefail

readonly DEFAULT_HEALTH_URL_FILE="/aifar/apps/keepalived/etc/keepalived-health-url"

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
    local response=""
    url="$(read_health_url "$url_file")" || return 1
    response="$(curl --fail --silent --show-error --connect-timeout 1 --max-time 2 -- "$url")" || return 1
    printf '%s' "$response" | json_up_is_true
}

main() {
    [[ "$#" -eq 0 ]] || return 2
    check_health "$DEFAULT_HEALTH_URL_FILE"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
