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
TRANSACTION_ACTIVE=0
SERVICE_WAS_ACTIVE=0
SERVICE_WAS_ENABLED=0
UNIT_LINK_EXISTED=0
UNIT_LINK_TARGET=""
FIREWALL_RUNTIME_PRESENT=0
FIREWALL_PERMANENT_PRESENT=0

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
    local rollback_status=0
    trap - EXIT

    if [[ "$status" -ne 0 && "$TRANSACTION_ACTIVE" -eq 1 ]]; then
        set +e
        rollback_uninstall_transaction
        rollback_status=$?
        if [[ "$rollback_status" -ne 0 ]]; then
            printf '[keepalived-uninstaller] ERROR: uninstall rollback incomplete; inspect the retained backup and errors above\n' >&2
        fi
    fi
    if [[ "$status" -ne 0 && -n "$BACKUP_DIR" && -d "$BACKUP_DIR" ]]; then
        printf '[keepalived-uninstaller] 已保留恢复备份：%s\n' "$BACKUP_DIR" >&2
    fi
    exit "$status"
}

trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

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

valid_selinux_record_row() {
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
        valid_selinux_record_row "$action" "$pattern" "$previous_type" "$applied_type" || die "SELinux 映射记录语义无效：$pattern"
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

capture_uninstall_state() {
    SERVICE_WAS_ACTIVE=0
    SERVICE_WAS_ENABLED=0
    UNIT_LINK_EXISTED=0
    UNIT_LINK_TARGET=""
    systemctl is-active --quiet keepalived.service && SERVICE_WAS_ACTIVE=1 || SERVICE_WAS_ACTIVE=0
    systemctl is-enabled --quiet keepalived.service && SERVICE_WAS_ENABLED=1 || SERVICE_WAS_ENABLED=0
    if [[ -e "$UNIT_LINK" || -L "$UNIT_LINK" ]]; then
        UNIT_LINK_EXISTED=1
        UNIT_LINK_TARGET="$(readlink -f -- "$UNIT_LINK" 2>/dev/null || true)"
        [[ "$UNIT_LINK_TARGET" == "$EXPECTED_UNIT" ]] || die "keepalived.service unit link changed before uninstall"
    fi
}

create_and_verify_backup() {
    umask 077
    install -d -o root -g root -m 700 "$BACKUP_ROOT"
    BACKUP_DIR="$BACKUP_ROOT/keepalived-$(date -u +%Y%m%dT%H%M%SZ)"
    [[ ! -e "$BACKUP_DIR" ]] || die "备份目录已存在：$BACKUP_DIR"
    install -d -o root -g root -m 700 "$BACKUP_DIR"

    cp -a -- "$APP_ROOT" "$BACKUP_DIR/installed-root"

    cat >"$BACKUP_DIR/uninstall-manifest.txt" <<EOF
installed_root=$APP_ROOT
unit_target=$(readlink -f -- "$UNIT_LINK" 2>/dev/null || printf 'none')
created_at_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)
service_was_active=$SERVICE_WAS_ACTIVE
service_was_enabled=$SERVICE_WAS_ENABLED
unit_link_existed=$UNIT_LINK_EXISTED
unit_link_target=$UNIT_LINK_TARGET
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

preflight_firewall_state() {
    FIREWALL_RUNTIME_PRESENT=0
    FIREWALL_PERMANENT_PRESENT=0
    if [[ ! -e "$FIREWALL_RECORD" && ! -L "$FIREWALL_RECORD" ]]; then
        return 0
    fi
    [[ -f "$FIREWALL_RECORD" && ! -L "$FIREWALL_RECORD" ]] || die "firewall ownership record must be a regular non-symlink file: $FIREWALL_RECORD"
    parse_firewall_record "$FIREWALL_RECORD"
    require_command firewall-cmd
    systemctl is-active --quiet firewalld.service || die "firewalld must be active while owned firewall rules exist"
    if [[ "$FIREWALL_RECORD_RUNTIME_CREATED" == 1 ]] && owned_firewall_rule_exists runtime "$FIREWALL_RECORD_ZONE" "$FIREWALL_RECORD_RULE"; then
        FIREWALL_RUNTIME_PRESENT=1
    fi
    if [[ "$FIREWALL_RECORD_PERMANENT_CREATED" == 1 ]] && owned_firewall_rule_exists permanent "$FIREWALL_RECORD_ZONE" "$FIREWALL_RECORD_RULE"; then
        FIREWALL_PERMANENT_PRESENT=1
    fi
}

append_uninstall_firewall_journal() {
    local form="$1" zone="$2" rule="$3"
    [[ "$TRANSACTION_ACTIVE" -eq 1 ]] || return 0
    printf '%s\t%s\t%s\n' "$form" "$zone" "$rule" >>"$BACKUP_DIR/uninstall-firewall.journal"
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
        append_uninstall_firewall_journal runtime "$zone" "$rule"
        firewall-cmd --zone="$zone" --remove-rich-rule="$rule" >/dev/null
    fi
    if [[ "$permanent_created" == 1 ]] && owned_firewall_rule_exists permanent "$zone" "$rule"; then
        append_uninstall_firewall_journal permanent "$zone" "$rule"
        firewall-cmd --permanent --zone="$zone" --remove-rich-rule="$rule" >/dev/null
    fi
}

preflight_selinux_state() {
    local action="" pattern="" previous_type="" applied_type="" current_context="" current_type=""

    [[ -e "$SELINUX_RECORD" || -L "$SELINUX_RECORD" ]] || return 0
    validate_selinux_record_file "$SELINUX_RECORD"
    require_command semanage
    while IFS='|' read -r action pattern previous_type applied_type; do
        current_context="$(mapping_context "$pattern")"
        [[ -n "$current_context" ]] || die "SELinux mapping missing before uninstall: $pattern"
        current_type="$(context_type "$current_context")" || die "cannot parse SELinux mapping before uninstall: $pattern"
        [[ "$current_type" == "$applied_type" ]] || die "SELinux mapping externally changed before uninstall: $pattern"
    done <"$SELINUX_RECORD"
}

append_uninstall_selinux_journal() {
    local action="$1" pattern="$2" previous_type="$3" applied_type="$4"
    [[ "$TRANSACTION_ACTIVE" -eq 1 ]] || return 0
    printf '%s|%s|%s|%s\n' "$action" "$pattern" "$previous_type" "$applied_type" >>"$BACKUP_DIR/uninstall-selinux.journal"
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
                append_uninstall_selinux_journal "$action" "$pattern" "$previous_type" "$applied_type"
                semanage fcontext -d "$pattern"
                ;;
            updated)
                [[ -n "$current_context" ]] || die "待恢复的 SELinux 映射已缺失：$pattern"
                current_type="$(context_type "$current_context")" || die "无法解析 SELinux 映射：$pattern"
                [[ "$current_type" == "$applied_type" ]] || die "SELinux 映射已被外部修改，拒绝覆盖：$pattern"
                [[ "$previous_type" == *_t ]] || die "SELinux 原始类型记录无效：$pattern"
                append_uninstall_selinux_journal "$action" "$pattern" "$previous_type" "$applied_type"
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

safe_remove_uninstall_root() {
    local resolved_root=""

    resolved_root="$(readlink -f -- "$APP_ROOT" 2>/dev/null || true)"
    [[ "$resolved_root" == "$APP_ROOT" ]] || return 1
    mountpoint -q "$APP_ROOT" && return 1
    rm -rf -- "$APP_ROOT"
    [[ ! -e "$APP_ROOT" && ! -L "$APP_ROOT" ]]
}

rollback_uninstall_selinux_journal() {
    local journal="$BACKUP_DIR/uninstall-selinux.journal"
    local -a rows=()
    local index=0 action="" pattern="" previous_type="" applied_type=""
    local current_context="" current_type="" rollback_status=0

    [[ -s "$journal" ]] || return 0
    mapfile -t rows <"$journal" || return 1
    for ((index=${#rows[@]} - 1; index >= 0; index--)); do
        IFS='|' read -r action pattern previous_type applied_type <<<"${rows[$index]}"
        current_context="$(mapping_context "$pattern")"
        current_type=""
        if [[ -n "$current_context" ]]; then
            current_type="$(context_type "$current_context")" || {
                rollback_status=1
                continue
            }
        fi
        case "$action" in
            created)
                if [[ -z "$current_context" ]]; then
                    semanage fcontext -a -t "$applied_type" "$pattern" || rollback_status=1
                elif [[ "$current_type" != "$applied_type" ]]; then
                    rollback_status=1
                fi
                ;;
            updated)
                if [[ "$current_type" == "$previous_type" ]]; then
                    semanage fcontext -m -t "$applied_type" "$pattern" || rollback_status=1
                elif [[ "$current_type" != "$applied_type" ]]; then
                    rollback_status=1
                fi
                ;;
            *) rollback_status=1 ;;
        esac
    done
    return "$rollback_status"
}

rollback_uninstall_firewall_journal() {
    local journal="$BACKUP_DIR/uninstall-firewall.journal"
    local -a rows=()
    local index=0 form="" zone="" rule="" query_status=0 rollback_status=0

    [[ -s "$journal" ]] || return 0
    systemctl is-active --quiet firewalld.service || return 1
    mapfile -t rows <"$journal" || return 1
    for ((index=${#rows[@]} - 1; index >= 0; index--)); do
        IFS=$'\t' read -r form zone rule <<<"${rows[$index]}"
        if [[ "$form" == permanent ]]; then
            firewall-cmd --permanent --zone="$zone" --query-rich-rule="$rule" >/dev/null 2>&1 && query_status=0 || query_status=$?
            if [[ "$query_status" -eq 1 ]]; then
                firewall-cmd --permanent --zone="$zone" --add-rich-rule="$rule" >/dev/null || rollback_status=1
            elif [[ "$query_status" -ne 0 ]]; then
                rollback_status=1
            fi
        elif [[ "$form" == runtime ]]; then
            firewall-cmd --zone="$zone" --query-rich-rule="$rule" >/dev/null 2>&1 && query_status=0 || query_status=$?
            if [[ "$query_status" -eq 1 ]]; then
                firewall-cmd --zone="$zone" --add-rich-rule="$rule" >/dev/null || rollback_status=1
            elif [[ "$query_status" -ne 0 ]]; then
                rollback_status=1
            fi
        else
            rollback_status=1
        fi
    done
    return "$rollback_status"
}

restore_uninstall_unit_and_service() {
    local current_target="" rollback_status=0

    if [[ -e "$UNIT_LINK" || -L "$UNIT_LINK" ]]; then
        current_target="$(readlink -f -- "$UNIT_LINK" 2>/dev/null || true)"
        if [[ "$current_target" == "$EXPECTED_UNIT" ]]; then
            rm -f -- "$UNIT_LINK" || rollback_status=1
        else
            rollback_status=1
        fi
    fi
    if [[ "$UNIT_LINK_EXISTED" -eq 1 ]]; then
        [[ "$UNIT_LINK_TARGET" == "$EXPECTED_UNIT" ]] && ln -s -- "$UNIT_LINK_TARGET" "$UNIT_LINK" || rollback_status=1
    fi
    systemctl daemon-reload || rollback_status=1
    if [[ "$SERVICE_WAS_ENABLED" -eq 1 ]]; then
        systemctl enable keepalived.service || rollback_status=1
    else
        systemctl disable keepalived.service || rollback_status=1
    fi
    if [[ "$SERVICE_WAS_ACTIVE" -eq 1 ]]; then
        systemctl restart keepalived.service || rollback_status=1
    else
        systemctl stop keepalived.service || rollback_status=1
    fi
    return "$rollback_status"
}

rollback_uninstall_transaction() {
    local rollback_status=0

    if [[ -z "$BACKUP_DIR" || ! -d "$BACKUP_DIR/installed-root" ]] ||
       ! (cd "$BACKUP_DIR" && sha256sum --check BACKUP.sha256); then
        return 1
    fi
    if [[ -e "$APP_ROOT" || -L "$APP_ROOT" ]]; then
        safe_remove_uninstall_root || return 1
    fi
    cp -a -- "$BACKUP_DIR/installed-root" "$APP_ROOT" || return 1
    rollback_uninstall_selinux_journal || rollback_status=1
    rollback_uninstall_firewall_journal || rollback_status=1
    restore_uninstall_unit_and_service || rollback_status=1
    return "$rollback_status"
}

preflight_uninstall_state() {
    [[ -d "$APP_ROOT" ]] || die "Keepalived installation root not found: $APP_ROOT"
    [[ "$(readlink -f -- "$APP_ROOT")" == "$APP_ROOT" ]] || die "refusing unexpected Keepalived installation path"
    mountpoint -q "$APP_ROOT" && die "refusing to uninstall a mounted application root"
    validate_unit_ownership
    capture_uninstall_state
    preflight_firewall_state
    preflight_selinux_state
}

perform_uninstall_mutations() {
    systemctl stop keepalived.service
    systemctl is-active --quiet keepalived.service && die "keepalived.service remained active after stop"
    systemctl disable keepalived.service
    if [[ -L "$UNIT_LINK" && "$(readlink -f -- "$UNIT_LINK")" == "$EXPECTED_UNIT" ]]; then
        rm -f -- "$UNIT_LINK"
    fi
    systemctl daemon-reload
    remove_owned_firewall_rule
    restore_selinux_mappings
    safe_remove_uninstall_root || die "failed to remove the managed Keepalived installation root"
    if command -v restorecon >/dev/null 2>&1 && [[ -d /aifar/apps ]]; then
        restorecon -F /aifar/apps
    fi
}

execute_uninstall_transaction() {
    preflight_uninstall_state
    create_and_verify_backup
    : >"$BACKUP_DIR/uninstall-firewall.journal"
    : >"$BACKUP_DIR/uninstall-selinux.journal"
    chmod 600 "$BACKUP_DIR/uninstall-firewall.journal" "$BACKUP_DIR/uninstall-selinux.journal"
    TRANSACTION_ACTIVE=1
    perform_uninstall_mutations
    TRANSACTION_ACTIVE=0
}

main() {
    [[ "$#" -eq 0 ]] || die "此脚本不接受参数"

    if [[ "$EUID" -ne 0 ]]; then
        require_command sudo
        exec sudo bash "$(readlink -f -- "${BASH_SOURCE[0]}")"
    fi

    for command_name in awk chmod cp date dirname find install ln mountpoint readlink rm sha256sum sort systemctl xargs; do
        require_command "$command_name"
    done
    execute_uninstall_transaction
    log "uninstall completed; verified backup retained at $BACKUP_DIR"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
