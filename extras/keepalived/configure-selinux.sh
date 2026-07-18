#!/usr/bin/env bash
set -Eeuo pipefail

export LC_ALL=C

readonly APP_ROOT="/aifar/apps/keepalived"
readonly RECORD_FILE="$APP_ROOT/var/lib/aifar/keepalived-selinux-fcontexts"
readonly RECORD_TMP="$RECORD_FILE.tmp"
MAPPINGS_COMMITTED=0

log() {
    printf '[keepalived-selinux] %s\n' "$*"
}

die() {
    printf '[keepalived-selinux] ERROR: %s\n' "$*" >&2
    exit 1
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || die "缺少必要命令：$1"
}

mapping_context() {
    semanage fcontext -l -C | awk -v pattern="$1" '$1 == pattern { print $NF; exit }'
}

context_type() {
    local context="$1"
    local type=""

    IFS=: read -r _ _ type _ <<<"$context"
    [[ -n "$type" && "$type" == *_t ]] || return 1
    printf '%s\n' "$type"
}

reference_type() {
    local output=""
    local context=""
    local type=""

    output="$(matchpathcon -n "$1" 2>/dev/null)" || die "发行版策略没有参考标签：$1"
    output="${output##*$'\n'}"
    context="${output##* }"
    type="$(context_type "$context")" || die "无法解析参考标签：$1"
    printf '%s\n' "$type"
}

rollback_new_mappings() {
    local -a records=()
    local index=""
    local action=""
    local pattern=""
    local previous_type=""
    local applied_type=""

    [[ -s "$RECORD_TMP" ]] || return 0
    mapfile -t records <"$RECORD_TMP"
    for ((index=${#records[@]} - 1; index >= 0; index--)); do
        IFS='|' read -r action pattern previous_type applied_type <<<"${records[$index]}"
        case "$action" in
            created) semanage fcontext -d "$pattern" >/dev/null 2>&1 || true ;;
            updated) semanage fcontext -m -t "$previous_type" "$pattern" >/dev/null 2>&1 || true ;;
            unchanged) : ;;
        esac
    done
}

cleanup() {
    local status=$?
    trap - EXIT

    if [[ "$status" -ne 0 && "$MAPPINGS_COMMITTED" -eq 0 ]]; then
        rollback_new_mappings
    fi
    rm -f -- "$RECORD_TMP"
    exit "$status"
}

trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

apply_mapping() {
    local pattern="$1"
    local reference="$2"
    local type=""
    local current_context=""
    local previous_type="-"
    local action=""

    type="$(reference_type "$reference")"
    current_context="$(mapping_context "$pattern")"
    if [[ -z "$current_context" ]]; then
        semanage fcontext -a -t "$type" "$pattern"
        action="created"
    else
        previous_type="$(context_type "$current_context")" || die "无法解析现有映射：$pattern"
        if [[ "$previous_type" == "$type" ]]; then
            action="unchanged"
        else
            semanage fcontext -m -t "$type" "$pattern"
            action="updated"
        fi
    fi
    printf '%s|%s|%s|%s\n' "$action" "$pattern" "$previous_type" "$type" >>"$RECORD_TMP"
}

ensure_recorded_mappings() {
    local action=""
    local pattern=""
    local previous_type=""
    local applied_type=""
    local current_context=""
    local current_type=""

    while IFS='|' read -r action pattern previous_type applied_type; do
        [[ -n "$pattern" && "$applied_type" == *_t ]] || die "SELinux 映射记录损坏：$RECORD_FILE"
        current_context="$(mapping_context "$pattern")"
        if [[ -z "$current_context" ]]; then
            semanage fcontext -a -t "$applied_type" "$pattern"
            continue
        fi
        current_type="$(context_type "$current_context")" || die "无法解析现有映射：$pattern"
        if [[ "$current_type" != "$applied_type" ]]; then
            semanage fcontext -m -t "$applied_type" "$pattern"
        fi
    done <"$RECORD_FILE"
}

verify_label() {
    local target="$1"
    local reference="$2"
    local expected_type=""
    local actual_context=""
    local actual_type=""

    [[ -e "$target" ]] || die "SELinux 标签校验目标不存在：$target"
    expected_type="$(reference_type "$reference")"
    actual_context="$(stat -c '%C' "$target")"
    actual_type="$(context_type "$actual_context")" || die "无法读取 SELinux 标签：$target"
    [[ "$actual_type" == "$expected_type" ]] || die "SELinux 标签不匹配：$target，期望 $expected_type，实际 $actual_type"
}

main() {
    [[ "$#" -eq 0 ]] || die "此脚本不接受参数"

    if [[ "$EUID" -ne 0 ]]; then
        require_command sudo
        exec sudo bash "$(readlink -f -- "${BASH_SOURCE[0]}")"
    fi

    require_command getenforce
    [[ "$(getenforce)" != "Disabled" ]] || die "SELinux 当前为 Disabled；脚本不会修改系统 SELinux 模式"
    require_command dnf
    if ! command -v matchpathcon >/dev/null 2>&1 ||
       ! command -v restorecon >/dev/null 2>&1 ||
       ! command -v semanage >/dev/null 2>&1; then
        dnf --assumeyes --setopt=install_weak_deps=False install \
            policycoreutils \
            policycoreutils-python-utils \
            selinux-policy-targeted
    fi
    for command_name in awk grep install matchpathcon restorecon semanage stat; do
        require_command "$command_name"
    done

    [[ -x "$APP_ROOT/sbin/keepalived" ]] || die "未找到 Keepalived 二进制：$APP_ROOT/sbin/keepalived"
    [[ -f "$APP_ROOT/systemd/keepalived.service" ]] || die "未找到 Keepalived systemd unit"
    mkdir -p \
        "$APP_ROOT/etc" \
        "$APP_ROOT/scripts" \
        "$APP_ROOT/var/lib/aifar" \
        "$APP_ROOT/run"

    if [[ -s "$RECORD_FILE" ]]; then
        MAPPINGS_COMMITTED=1
        ensure_recorded_mappings
    else
        : >"$RECORD_TMP"
        apply_mapping '/aifar/apps/keepalived/sbin/keepalived' '/usr/sbin/keepalived'
        apply_mapping '/aifar/apps/keepalived/etc(/.*)?' '/etc/keepalived'
        apply_mapping '/aifar/apps/keepalived/scripts(/.*)?' '/usr/libexec/keepalived'
        apply_mapping '/aifar/apps/keepalived/var(/.*)?' '/var/lib/keepalived'
        apply_mapping '/aifar/apps/keepalived/run(/.*)?' '/run/keepalived'
        apply_mapping '/aifar/apps/keepalived/systemd/keepalived\.service' '/usr/lib/systemd/system/keepalived.service'
    fi

    restorecon -RF "$APP_ROOT"
    verify_label "$APP_ROOT/sbin/keepalived" '/usr/sbin/keepalived'
    verify_label "$APP_ROOT/etc" '/etc/keepalived'
    verify_label "$APP_ROOT/scripts" '/usr/libexec/keepalived'
    verify_label "$APP_ROOT/var" '/var/lib/keepalived'
    verify_label "$APP_ROOT/run" '/run/keepalived'
    verify_label "$APP_ROOT/systemd/keepalived.service" '/usr/lib/systemd/system/keepalived.service'

    if [[ "$MAPPINGS_COMMITTED" -eq 0 ]]; then
        install -o root -g root -m 600 "$RECORD_TMP" "$RECORD_FILE"
        restorecon -F "$RECORD_FILE"
        MAPPINGS_COMMITTED=1
    fi
    log "SELinux 模式保持为 $(getenforce)，Keepalived 标签已应用"
}

main "$@"
