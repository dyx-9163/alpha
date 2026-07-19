#!/usr/bin/env bash
set -Eeuo pipefail

export LC_ALL=C

readonly APP_ROOT="/aifar/apps/keepalived"
readonly RECORD_FILE="$APP_ROOT/var/lib/aifar/keepalived-selinux-fcontexts"
readonly NEXT_RECORD="$RECORD_FILE.next.$$"
readonly CURRENT_JOURNAL="$RECORD_FILE.journal.$$"
TRANSACTION_FILE=""
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
    semanage fcontext -l -C | PATTERN="$1" awk '$1 == ENVIRON["PATTERN"] { print $NF; exit }'
}

context_type() {
    local context="$1"
    local type=""

    IFS=: read -r _ _ type _ <<<"$context"
    [[ -n "$type" && "$type" == *_t ]] || return 1
    printf '%s\n' "$type"
}

reference_type() {
    local output="" context="" type=""

    output="$(matchpathcon -n "$1" 2>/dev/null)" || die "发行版策略没有参考标签：$1"
    output="${output##*$'\n'}"
    context="${output##* }"
    type="$(context_type "$context")" || die "无法解析参考标签：$1"
    printf '%s\n' "$type"
}

valid_mapping_row() {
    local action="$1" pattern="$2" previous_type="$3" applied_type="$4"

    case "$pattern" in
        '/aifar/apps/keepalived/sbin/keepalived'|\
        '/aifar/apps/keepalived/etc(/.*)?'|\
        '/aifar/apps/keepalived/libexec(/.*)?'|\
        '/aifar/apps/keepalived/var(/.*)?'|\
        '/aifar/apps/keepalived/run(/.*)?'|\
        '/aifar/apps/keepalived/systemd/keepalived\.service'|\
        '/aifar/apps/keepalived/scripts(/.*)?') ;;
        *) return 1 ;;
    esac
    [[ "$applied_type" =~ ^[A-Za-z0-9_]+_t$ ]] || return 1
    case "$action" in
        created) [[ "$previous_type" == '-' ]] ;;
        updated)
            [[ "$previous_type" =~ ^[A-Za-z0-9_]+_t$ && "$previous_type" != "$applied_type" ]]
            ;;
        unchanged) [[ "$previous_type" == "$applied_type" ]] ;;
        *) return 1 ;;
    esac
}

valid_journal_row() {
    local action="$1" pattern="$2" previous_type="$3" applied_type="$4"

    case "$action" in
        retired_created)
            [[ "$pattern" == '/aifar/apps/keepalived/scripts(/.*)?' &&
               "$previous_type" == '-' &&
               "$applied_type" =~ ^[A-Za-z0-9_]+_t$ ]]
            ;;
        retired_updated)
            [[ "$pattern" == '/aifar/apps/keepalived/scripts(/.*)?' &&
               "$previous_type" =~ ^[A-Za-z0-9_]+_t$ &&
               "$applied_type" =~ ^[A-Za-z0-9_]+_t$ &&
               "$previous_type" != "$applied_type" ]]
            ;;
        *) valid_mapping_row "$action" "$pattern" "$previous_type" "$applied_type" ;;
    esac
}

validate_selinux_record_file() {
    local file="$1" line="" action="" pattern="" previous_type="" applied_type="" count=0 required_pattern=""
    declare -A seen_patterns=()

    [[ -e "$file" || -L "$file" ]] || return 0
    [[ -f "$file" && ! -L "$file" && -s "$file" ]] || die "SELinux 映射记录必须是非空普通文件：$file"
    while IFS= read -r line || [[ -n "$line" ]]; do
        [[ "$line" =~ ^([^|]*)[|]([^|]*)[|]([^|]*)[|]([^|]*)$ ]] || die "SELinux 映射记录字段格式无效：$file"
        action="${BASH_REMATCH[1]}"
        pattern="${BASH_REMATCH[2]}"
        previous_type="${BASH_REMATCH[3]}"
        applied_type="${BASH_REMATCH[4]}"
        valid_mapping_row "$action" "$pattern" "$previous_type" "$applied_type" || die "SELinux 映射记录语义无效：$pattern"
        [[ -z "${seen_patterns[$pattern]+x}" ]] || die "SELinux 映射记录包含重复 pattern：$pattern"
        seen_patterns[$pattern]=1
        ((count += 1))
    done <"$file"
    for required_pattern in \
        '/aifar/apps/keepalived/sbin/keepalived' \
        '/aifar/apps/keepalived/etc(/.*)?' \
        '/aifar/apps/keepalived/var(/.*)?' \
        '/aifar/apps/keepalived/run(/.*)?' \
        '/aifar/apps/keepalived/systemd/keepalived\.service'; do
        [[ -n "${seen_patterns[$required_pattern]+x}" ]] || die "SELinux ownership record missing core pattern: $required_pattern"
    done
    if [[ -z "${seen_patterns['/aifar/apps/keepalived/libexec(/.*)?']+x}" ]]; then
        [[ -n "${seen_patterns['/aifar/apps/keepalived/scripts(/.*)?']+x}" ]] || die "SELinux ownership record missing libexec core pattern"
    fi
    [[ -z "${seen_patterns['/aifar/apps/keepalived/libexec(/.*)?']+x}" ||
       -z "${seen_patterns['/aifar/apps/keepalived/scripts(/.*)?']+x}" ]] || die "SELinux ownership record mixes current and legacy core patterns"
    ((count == 6)) || die "SELinux ownership record must contain exactly six core patterns"
}

append_journal() {
    local action="$1" pattern="$2" previous_type="$3" applied_type="$4"
    local row="$action|$pattern|$previous_type|$applied_type"
    local last="" candidate="$CURRENT_JOURNAL.append.$$"

    rm -f -- "$candidate"
    cp -- "$CURRENT_JOURNAL" "$candidate" || return 1
    printf '%s\n' "$row" >>"$candidate" || { rm -f -- "$candidate"; return 1; }
    last="$(tail -n 1 "$candidate")" || { rm -f -- "$candidate"; return 1; }
    [[ "$last" == "$row" ]] || { rm -f -- "$candidate"; return 1; }
    mv -f -- "$candidate" "$CURRENT_JOURNAL" || { rm -f -- "$candidate"; return 1; }
}

reverse_mapping_mutation() {
    local action="$1" pattern="$2" previous_type="$3" applied_type="$4"
    local current_context="" current_type="" restored_context="" restored_type=""

    case "$action" in
        created)
            current_context="$(mapping_context "$pattern")"
            [[ -n "$current_context" ]] || return 1
            current_type="$(context_type "$current_context")" || return 1
            [[ "$current_type" == "$applied_type" ]] || return 1
            semanage fcontext -d "$pattern" || return 1
            [[ -z "$(mapping_context "$pattern")" ]] || return 1
            ;;
        updated)
            current_context="$(mapping_context "$pattern")"
            [[ -n "$current_context" ]] || return 1
            current_type="$(context_type "$current_context")" || return 1
            [[ "$current_type" == "$applied_type" ]] || return 1
            semanage fcontext -m -t "$previous_type" "$pattern" || return 1
            restored_context="$(mapping_context "$pattern")"
            restored_type="$(context_type "$restored_context")" || return 1
            [[ "$restored_type" == "$previous_type" ]] || return 1
            ;;
        retired_created)
            current_context="$(mapping_context "$pattern")"
            if [[ -n "$current_context" ]]; then
                current_type="$(context_type "$current_context")" || return 1
                [[ "$current_type" == "$applied_type" ]] || return 1
                return 0
            fi
            semanage fcontext -a -t "$applied_type" "$pattern" || return 1
            restored_context="$(mapping_context "$pattern")"
            restored_type="$(context_type "$restored_context")" || return 1
            [[ "$restored_type" == "$applied_type" ]] || return 1
            ;;
        retired_updated)
            current_context="$(mapping_context "$pattern")"
            [[ -n "$current_context" ]] || return 1
            current_type="$(context_type "$current_context")" || return 1
            if [[ "$current_type" == "$applied_type" ]]; then
                return 0
            fi
            [[ "$current_type" == "$previous_type" ]] || return 1
            semanage fcontext -m -t "$applied_type" "$pattern" || return 1
            restored_context="$(mapping_context "$pattern")"
            restored_type="$(context_type "$restored_context")" || return 1
            [[ "$restored_type" == "$applied_type" ]] || return 1
            ;;
        *) return 1 ;;
    esac
}

apply_mapping_mutation() {
    local action="$1" pattern="$2" previous_type="$3" applied_type="$4"

    case "$action" in
        created)
            semanage fcontext -a -t "$applied_type" "$pattern" || die "创建 SELinux 映射失败：$pattern"
            ;;
        updated)
            semanage fcontext -m -t "$applied_type" "$pattern" || die "更新 SELinux 映射失败：$pattern"
            ;;
        *) die "SELinux mutation 动作无效：$action" ;;
    esac
    if ! append_journal "$action" "$pattern" "$previous_type" "$applied_type"; then
        if ! reverse_mapping_mutation "$action" "$pattern" "$previous_type" "$applied_type"; then
            die "SELinux mutation 已成功但 journal 写入和即时撤销均失败：$pattern"
        fi
        die "SELinux mutation 已即时撤销，因为 journal 写入失败：$pattern"
    fi
}

rollback_current_journal() {
    local -a rows=()
    local index=0 action="" pattern="" previous_type="" applied_type="" rollback_status=0

    [[ -s "$CURRENT_JOURNAL" ]] || return 0
    mapfile -t rows <"$CURRENT_JOURNAL"
    for ((index=${#rows[@]} - 1; index >= 0; index--)); do
        IFS='|' read -r action pattern previous_type applied_type <<<"${rows[$index]}"
        valid_journal_row "$action" "$pattern" "$previous_type" "$applied_type" || continue
        [[ "$action" != unchanged ]] || continue
        reverse_mapping_mutation "$action" "$pattern" "$previous_type" "$applied_type" || rollback_status=1
    done
    return "$rollback_status"
}

cleanup() {
    local status=$?
    trap - EXIT

    if [[ "$status" -ne 0 && "$MAPPINGS_COMMITTED" -eq 0 ]]; then
        rollback_current_journal || printf '[keepalived-selinux] ERROR: 本轮 SELinux journal 未完整回滚\n' >&2
        if [[ -n "$TRANSACTION_FILE" ]]; then
            rm -f -- "$TRANSACTION_FILE" "$TRANSACTION_FILE.tmp.$$"
        fi
    fi
    rm -f -- "$NEXT_RECORD" "$NEXT_RECORD.install.$$" "$CURRENT_JOURNAL"
    exit "$status"
}

trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

prepare_transaction_file() {
    local requested="${KEEPALIVED_SELINUX_TRANSACTION_FILE:-}"
    local resolved="" parent=""

    [[ -n "$requested" ]] || return 0
    resolved="$(readlink -m -- "$requested")" || die "无法解析 SELinux 事务日志路径"
    [[ "$resolved" == /tmp/keepalived-offline.*/* ]] || die "SELinux 事务日志必须位于 /tmp/keepalived-offline.* 下"
    parent="$(dirname -- "$resolved")"
    [[ -d "$parent" ]] || die "SELinux 事务日志目录不存在：$parent"
    [[ ! -L "$requested" ]] || die "SELinux 事务日志不能是符号链接：$requested"
    if [[ -e "$requested" ]]; then
        [[ -f "$requested" ]] || die "SELinux 事务日志不是普通文件：$requested"
    fi
    TRANSACTION_FILE="$resolved"
}

apply_new_mapping() {
    local pattern="$1" reference="$2"
    local type="" current_context="" previous_type="-" action=""

    type="$(reference_type "$reference")"
    current_context="$(mapping_context "$pattern")"
    if [[ -z "$current_context" ]]; then
        action=created
        apply_mapping_mutation "$action" "$pattern" "$previous_type" "$type"
    else
        previous_type="$(context_type "$current_context")" || die "无法解析现有映射：$pattern"
        if [[ "$previous_type" == "$type" ]]; then
            action=unchanged
        else
            action=updated
            apply_mapping_mutation "$action" "$pattern" "$previous_type" "$type"
        fi
    fi
    printf '%s|%s|%s|%s\n' "$action" "$pattern" "$previous_type" "$type" >>"$NEXT_RECORD"
}

reconcile_recorded_mapping() {
    local action="$1" pattern="$2" previous_type="$3" applied_type="$4"
    local current_context="" current_type=""

    valid_mapping_row "$action" "$pattern" "$previous_type" "$applied_type" || die "SELinux 映射记录损坏：$RECORD_FILE"
    current_context="$(mapping_context "$pattern")"
    if [[ -z "$current_context" ]]; then
        apply_mapping_mutation created "$pattern" - "$applied_type"
    else
        current_type="$(context_type "$current_context")" || die "无法解析现有映射：$pattern"
        if [[ "$current_type" != "$applied_type" ]]; then
            apply_mapping_mutation updated "$pattern" "$current_type" "$applied_type"
        fi
    fi
    printf '%s|%s|%s|%s\n' "$action" "$pattern" "$previous_type" "$applied_type" >>"$NEXT_RECORD"
}

retire_legacy_mapping() {
    local action="$1" pattern="$2" previous_type="$3" applied_type="$4"
    local current_context="" current_type="" retired_action=""

    [[ "$pattern" == '/aifar/apps/keepalived/scripts(/.*)?' ]] || die "unexpected legacy SELinux pattern: $pattern"
    [[ "$action" != unchanged ]] || return 0
    current_context="$(mapping_context "$pattern")"
    [[ -n "$current_context" ]] || die "legacy SELinux mapping is missing: $pattern"
    current_type="$(context_type "$current_context")" || die "cannot parse legacy SELinux mapping: $pattern"
    [[ "$current_type" == "$applied_type" ]] || die "legacy SELinux mapping was externally modified: $pattern"

    case "$action" in
        created)
            retired_action=retired_created
            append_journal "$retired_action" "$pattern" "$previous_type" "$applied_type" || die "cannot journal legacy SELinux mapping retirement: $pattern"
            semanage fcontext -d "$pattern" || die "failed to retire legacy SELinux mapping: $pattern"
            [[ -z "$(mapping_context "$pattern")" ]] || die "legacy SELinux mapping retirement verification failed: $pattern"
            ;;
        updated)
            retired_action=retired_updated
            append_journal "$retired_action" "$pattern" "$previous_type" "$applied_type" || die "cannot journal legacy SELinux mapping restore: $pattern"
            semanage fcontext -m -t "$previous_type" "$pattern" || die "failed to restore legacy SELinux mapping: $pattern"
            current_context="$(mapping_context "$pattern")"
            current_type="$(context_type "$current_context")" || die "cannot verify restored legacy SELinux mapping: $pattern"
            [[ "$current_type" == "$previous_type" ]] || die "legacy SELinux mapping restore verification failed: $pattern"
            ;;
        *) die "invalid legacy SELinux record action: $action" ;;
    esac
}

build_next_record() {
    local action="" pattern="" previous_type="" applied_type=""
    declare -A recorded_patterns=()

    validate_selinux_record_file "$RECORD_FILE"
    : >"$NEXT_RECORD"
    : >"$CURRENT_JOURNAL"
    if [[ -e "$RECORD_FILE" || -L "$RECORD_FILE" ]]; then
        while IFS='|' read -r action pattern previous_type applied_type; do
            [[ -z "${recorded_patterns[$pattern]+x}" ]] || die "SELinux 映射记录包含重复 pattern：$pattern"
            recorded_patterns[$pattern]=1
            if [[ "$pattern" == '/aifar/apps/keepalived/scripts(/.*)?' ]]; then
                retire_legacy_mapping "$action" "$pattern" "$previous_type" "$applied_type"
            else
                reconcile_recorded_mapping "$action" "$pattern" "$previous_type" "$applied_type"
            fi
        done <"$RECORD_FILE"
    else
        apply_new_mapping '/aifar/apps/keepalived/sbin/keepalived' '/usr/sbin/keepalived'
        apply_new_mapping '/aifar/apps/keepalived/etc(/.*)?' '/etc/keepalived'
        apply_new_mapping '/aifar/apps/keepalived/var(/.*)?' '/var/lib/keepalived'
        apply_new_mapping '/aifar/apps/keepalived/run(/.*)?' '/run/keepalived'
        apply_new_mapping '/aifar/apps/keepalived/systemd/keepalived\.service' '/usr/lib/systemd/system/keepalived.service'
    fi
    if [[ -z "${recorded_patterns['/aifar/apps/keepalived/libexec(/.*)?']+x}" ]]; then
        apply_new_mapping '/aifar/apps/keepalived/libexec(/.*)?' '/usr/libexec/keepalived'
    fi
}

verify_label() {
    local target="$1" reference="$2"
    local expected_type="" actual_context="" actual_type=""

    [[ -e "$target" ]] || die "SELinux 标签校验目标不存在：$target"
    expected_type="$(reference_type "$reference")"
    actual_context="$(stat -c '%C' "$target")"
    actual_type="$(context_type "$actual_context")" || die "无法读取 SELinux 标签：$target"
    [[ "$actual_type" == "$expected_type" ]] || die "SELinux 标签不匹配：$target，期望 $expected_type，实际 $actual_type"
}

export_transaction_journal() {
    local transaction_tmp=""

    [[ -n "$TRANSACTION_FILE" ]] || return 0
    transaction_tmp="$TRANSACTION_FILE.tmp.$$"
    install -o root -g root -m 600 "$CURRENT_JOURNAL" "$transaction_tmp"
    mv -f -- "$transaction_tmp" "$TRANSACTION_FILE"
}

commit_ownership_record() {
    install -o root -g root -m 600 "$NEXT_RECORD" "$NEXT_RECORD.install.$$"
    restorecon -F "$NEXT_RECORD.install.$$"
    mv -f -- "$NEXT_RECORD.install.$$" "$RECORD_FILE"
    MAPPINGS_COMMITTED=1
}

main() {
    [[ "$#" -eq 0 ]] || die "此脚本不接受参数"

    if [[ "$EUID" -ne 0 ]]; then
        require_command sudo
        exec sudo bash "$(readlink -f -- "${BASH_SOURCE[0]}")"
    fi

    require_command getenforce
    [[ "$(getenforce)" != Disabled ]] || die "SELinux 当前为 Disabled；脚本不会修改系统 SELinux 模式"
    require_command dnf
    if ! command -v matchpathcon >/dev/null 2>&1 ||
       ! command -v restorecon >/dev/null 2>&1 ||
       ! command -v semanage >/dev/null 2>&1; then
        dnf --assumeyes --setopt=install_weak_deps=False install \
            policycoreutils \
            policycoreutils-python-utils \
            selinux-policy-targeted
    fi
    for command_name in awk cp dirname install matchpathcon mv readlink restorecon rm semanage stat tail; do
        require_command "$command_name"
    done

    [[ -x "$APP_ROOT/sbin/keepalived" ]] || die "未找到 Keepalived 二进制：$APP_ROOT/sbin/keepalived"
    [[ -f "$APP_ROOT/systemd/keepalived.service" ]] || die "未找到 Keepalived systemd unit"
    mkdir -p \
        "$APP_ROOT/etc" \
        "$APP_ROOT/libexec" \
        "$APP_ROOT/var/lib/aifar" \
        "$APP_ROOT/run"
    [[ -d "$APP_ROOT/libexec" ]] || die "无法创建 Keepalived libexec 目录"

    prepare_transaction_file
    build_next_record
    restorecon -RF "$APP_ROOT"
    verify_label "$APP_ROOT/sbin/keepalived" '/usr/sbin/keepalived'
    verify_label "$APP_ROOT/etc" '/etc/keepalived'
    verify_label "$APP_ROOT/libexec" '/usr/libexec/keepalived'
    verify_label "$APP_ROOT/var" '/var/lib/keepalived'
    verify_label "$APP_ROOT/run" '/run/keepalived'
    verify_label "$APP_ROOT/systemd/keepalived.service" '/usr/lib/systemd/system/keepalived.service'
    export_transaction_journal
    commit_ownership_record
    log "SELinux 模式保持为 $(getenforce)，Keepalived 标签已应用"
}

main "$@"
