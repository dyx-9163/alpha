#!/usr/bin/env bash
set -Eeuo pipefail

export LC_ALL=C

readonly APP_ROOT="/aifar/apps/keepalived"
readonly BACKUP_ROOT="/aifar/backups"
readonly UNIT_LINK="/etc/systemd/system/keepalived.service"
readonly EXPECTED_UNIT="$APP_ROOT/systemd/keepalived.service"
readonly SELINUX_RECORD="$APP_ROOT/var/lib/aifar/keepalived-selinux-fcontexts"
readonly FIREWALL_RECORD="$APP_ROOT/var/lib/aifar/firewall-rule"
BACKUP_DIR=""

log() {
    printf '[keepalived-uninstaller] %s\n' "$*"
}

die() {
    printf '[keepalived-uninstaller] ERROR: %s\n' "$*" >&2
    exit 1
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || die "缺少必要命令：$1"
}

cleanup() {
    local status=$?
    trap - EXIT

    if [[ "$status" -ne 0 && -n "$BACKUP_DIR" && -d "$BACKUP_DIR" ]]; then
        printf '[keepalived-uninstaller] 已保留恢复备份：%s\n' "$BACKUP_DIR" >&2
    fi
    exit "$status"
}

trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

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

validate_unit_ownership() {
    local unit_target=""
    local fragment=""
    local fragment_target=""

    unit_target="$(readlink -f -- "$UNIT_LINK" 2>/dev/null || true)"
    if [[ -n "$unit_target" && "$unit_target" != "$EXPECTED_UNIT" ]]; then
        die "系统 keepalived.service 不属于此安装：$unit_target"
    fi

    fragment="$(systemctl show -p FragmentPath --value keepalived.service 2>/dev/null || true)"
    fragment_target="$(readlink -f -- "$fragment" 2>/dev/null || true)"
    if [[ -n "$fragment_target" && "$fragment_target" != "$EXPECTED_UNIT" ]]; then
        die "已加载的 keepalived.service 不属于此安装：$fragment_target"
    fi
}

create_and_verify_backup() {
    local relative=""

    umask 077
    install -d -o root -g root -m 700 "$BACKUP_ROOT"
    BACKUP_DIR="$BACKUP_ROOT/keepalived-$(date -u +%Y%m%dT%H%M%SZ)"
    [[ ! -e "$BACKUP_DIR" ]] || die "备份目录已存在：$BACKUP_DIR"
    install -d -o root -g root -m 700 "$BACKUP_DIR"

    for relative in etc scripts systemd/keepalived.service var/lib/aifar/keepalived-selinux-fcontexts var/lib/aifar/firewall-rule; do
        if [[ -e "$APP_ROOT/$relative" ]]; then
            install -d -o root -g root -m 700 "$BACKUP_DIR/$(dirname "$relative")"
            cp -a -- "$APP_ROOT/$relative" "$BACKUP_DIR/$relative"
        fi
    done

    cat >"$BACKUP_DIR/uninstall-manifest.txt" <<EOF
installed_root=$APP_ROOT
unit_target=$(readlink -f -- "$UNIT_LINK" 2>/dev/null || printf 'none')
created_at_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF

    (
        cd "$BACKUP_DIR"
        find . -type f ! -name BACKUP.sha256 -print0 | sort -z | xargs -0 sha256sum >BACKUP.sha256
        test -s uninstall-manifest.txt
        sha256sum --check BACKUP.sha256
    )
}

valid_ipv4() {
    local address="$1" octet=""
    local -a octets=()
    [[ "$address" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || return 1
    IFS=. read -r -a octets <<<"$address"
    for octet in "${octets[@]}"; do
        [[ "$octet" =~ ^(0|[1-9][0-9]{0,2})$ ]] || return 1
        ((10#$octet <= 255)) || return 1
    done
}

valid_firewall_zone() {
    [[ "$1" =~ ^[A-Za-z0-9_.-]{1,64}$ ]]
}

valid_peer_firewall_rule() {
    local rule="$1"
    local peer=""

    [[ "$rule" =~ ^rule\ family=\"ipv4\"\ source\ address=\"([0-9]+\.[0-9]+\.[0-9]+\.[0-9]+)/32\"\ protocol\ value=\"112\"\ accept$ ]] || return 1
    peer="${BASH_REMATCH[1]}"
    valid_ipv4 "$peer"
}

parse_firewall_record() {
    local file="$1" line="" key="" value=""
    declare -A seen=()
    FIREWALL_RECORD_ZONE=""
    FIREWALL_RECORD_RULE=""
    FIREWALL_RECORD_RUNTIME_CREATED=""
    FIREWALL_RECORD_PERMANENT_CREATED=""

    [[ -r "$file" ]] || die "防火墙所有权记录不可读：$file"
    while IFS= read -r line || [[ -n "$line" ]]; do
        [[ "$line" =~ ^([a-z_]+)=(.*)$ ]] || die "防火墙所有权记录格式无效：$file"
        key="${BASH_REMATCH[1]}"
        value="${BASH_REMATCH[2]}"
        case "$key" in
            zone) FIREWALL_RECORD_ZONE="$value" ;;
            rule) FIREWALL_RECORD_RULE="$value" ;;
            runtime_created) FIREWALL_RECORD_RUNTIME_CREATED="$value" ;;
            permanent_created) FIREWALL_RECORD_PERMANENT_CREATED="$value" ;;
            *) die "防火墙所有权记录包含未知字段：$key" ;;
        esac
        [[ -z "${seen[$key]+x}" ]] || die "防火墙所有权记录字段重复：$key"
        seen[$key]=1
    done <"$file"
    for key in zone rule runtime_created permanent_created; do
        [[ -n "${seen[$key]+x}" ]] || die "防火墙所有权记录缺少字段：$key"
    done
    valid_firewall_zone "$FIREWALL_RECORD_ZONE" || die "防火墙所有权记录 zone 无效"
    valid_peer_firewall_rule "$FIREWALL_RECORD_RULE" || die "防火墙所有权记录 rule 无效"
    [[ "$FIREWALL_RECORD_RUNTIME_CREATED" =~ ^[01]$ ]] || die "防火墙所有权记录 runtime_created 无效"
    [[ "$FIREWALL_RECORD_PERMANENT_CREATED" =~ ^[01]$ ]] || die "防火墙所有权记录 permanent_created 无效"
}

owned_firewall_rule_exists() {
    local form="$1" zone="$2" rule="$3" status=0

    if [[ "$form" == permanent ]]; then
        firewall-cmd --permanent --zone="$zone" --query-rich-rule="$rule" >/dev/null 2>&1 && return 0
    else
        firewall-cmd --zone="$zone" --query-rich-rule="$rule" >/dev/null 2>&1 && return 0
    fi
    status=$?
    [[ "$status" -eq 1 ]] && return 1
    die "查询 firewalld $form 规则失败（状态 $status），已停止卸载"
}

remove_owned_firewall_rule() {
    local zone="" rule="" runtime_created=0 permanent_created=0

    if [[ ! -e "$FIREWALL_RECORD" && ! -L "$FIREWALL_RECORD" ]]; then
        return 0
    fi
    [[ -f "$FIREWALL_RECORD" && ! -L "$FIREWALL_RECORD" ]] || die "防火墙所有权记录不是普通文件：$FIREWALL_RECORD"
    parse_firewall_record "$FIREWALL_RECORD"
    zone="$FIREWALL_RECORD_ZONE"
    rule="$FIREWALL_RECORD_RULE"
    runtime_created="$FIREWALL_RECORD_RUNTIME_CREATED"
    permanent_created="$FIREWALL_RECORD_PERMANENT_CREATED"

    if [[ "$runtime_created" == 1 ]] && owned_firewall_rule_exists runtime "$zone" "$rule"; then
        firewall-cmd --zone="$zone" --remove-rich-rule="$rule" >/dev/null
    fi
    if [[ "$permanent_created" == 1 ]] && owned_firewall_rule_exists permanent "$zone" "$rule"; then
        firewall-cmd --permanent --zone="$zone" --remove-rich-rule="$rule" >/dev/null
    fi
}

restore_selinux_mappings() {
    local action=""
    local pattern=""
    local previous_type=""
    local applied_type=""
    local current_context=""
    local current_type=""

    [[ -s "$SELINUX_RECORD" ]] || return 0
    while IFS='|' read -r action pattern previous_type applied_type; do
        current_context="$(mapping_context "$pattern")"
        case "$action" in
            created)
                [[ -n "$current_context" ]] || continue
                current_type="$(context_type "$current_context")" || die "无法解析 SELinux 映射：$pattern"
                [[ "$current_type" == "$applied_type" ]] || die "SELinux 映射已被外部修改，拒绝删除：$pattern"
                semanage fcontext -d "$pattern"
                ;;
            updated)
                [[ -n "$current_context" ]] || die "待恢复的 SELinux 映射已缺失：$pattern"
                current_type="$(context_type "$current_context")" || die "无法解析 SELinux 映射：$pattern"
                [[ "$current_type" == "$applied_type" ]] || die "SELinux 映射已被外部修改，拒绝覆盖：$pattern"
                [[ "$previous_type" == *_t ]] || die "SELinux 原始类型记录无效：$pattern"
                semanage fcontext -m -t "$previous_type" "$pattern"
                ;;
            unchanged)
                ;;
            *)
                die "未知 SELinux 映射记录：$action"
                ;;
        esac
    done <"$SELINUX_RECORD"
}

main() {
    [[ "$#" -eq 0 ]] || die "此脚本不接受参数"

    if [[ "$EUID" -ne 0 ]]; then
        require_command sudo
        exec sudo bash "$(readlink -f -- "${BASH_SOURCE[0]}")"
    fi

    for command_name in awk cp date dirname find install mountpoint readlink rm sha256sum sort systemctl xargs; do
        require_command "$command_name"
    done
    [[ -d "$APP_ROOT" ]] || die "未找到安装目录：$APP_ROOT"
    [[ "$(readlink -f -- "$APP_ROOT")" == "$APP_ROOT" ]] || die "拒绝删除非预期安装路径"
    mountpoint -q "$APP_ROOT" && die "拒绝删除挂载点：$APP_ROOT"
    validate_unit_ownership

    if [[ -s "$SELINUX_RECORD" ]]; then
        require_command semanage
    fi

    create_and_verify_backup

    if [[ -e "$FIREWALL_RECORD" || -L "$FIREWALL_RECORD" ]]; then
        [[ -f "$FIREWALL_RECORD" && ! -L "$FIREWALL_RECORD" ]] || die "防火墙所有权记录不是普通文件：$FIREWALL_RECORD"
        parse_firewall_record "$FIREWALL_RECORD"
        require_command firewall-cmd
        systemctl is-active --quiet firewalld.service || die "存在防火墙所有权记录，但 firewalld 不可用；已停止卸载"
    fi

    systemctl stop keepalived.service
    systemctl is-active --quiet keepalived.service && die "Keepalived 服务仍在运行，已停止卸载"
    systemctl disable keepalived.service || true
    if [[ -L "$UNIT_LINK" && "$(readlink -f -- "$UNIT_LINK")" == "$EXPECTED_UNIT" ]]; then
        rm -f -- "$UNIT_LINK"
    fi
    systemctl daemon-reload

    remove_owned_firewall_rule
    restore_selinux_mappings

    [[ "$(readlink -f -- "$APP_ROOT")" == "$APP_ROOT" ]] || die "删除前安装路径发生变化，已停止卸载"
    mountpoint -q "$APP_ROOT" && die "删除前安装目录变为挂载点，已停止卸载"
    rm -rf -- "$APP_ROOT"
    [[ ! -e "$APP_ROOT" ]] || die "安装目录删除失败：$APP_ROOT"

    if command -v restorecon >/dev/null 2>&1 && [[ -d /aifar/apps ]]; then
        restorecon -F /aifar/apps
    fi
    log "卸载完成，备份保留在：$BACKUP_DIR"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
