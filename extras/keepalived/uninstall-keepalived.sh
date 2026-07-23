#!/usr/bin/env bash
set -Eeuo pipefail

export LC_ALL=C

readonly APP_ROOT="/aifar/apps/keepalived"
readonly BACKUP_ROOT="/aifar/backups"
readonly UNIT_FILE="/etc/systemd/system/keepalived.service"
readonly BUILT_UNIT="$APP_ROOT/systemd/keepalived.service"
readonly SELINUX_RECORD="$APP_ROOT/var/lib/aifar/keepalived-selinux-fcontexts"
readonly FIREWALL_RECORD="$APP_ROOT/var/lib/aifar/firewall-rule"
BACKUP_DIR=""
TRANSACTION_ACTIVE=0
ROOT_MUTATION_STARTED=0
SERVICE_WAS_ACTIVE=0
SERVICE_WAS_ENABLED=0
UNIT_STATE='absent'
UNIT_LINK_TARGET=""
UNIT_FILE_SHA256=""
UNIT_ROLLBACK_CONFLICT=0
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

is_managed_unit_file() {
    local file="$1"
    [[ -f "$file" && ! -L "$file" ]] || return 1
    awk -v executable="$APP_ROOT/sbin/keepalived" '
        /^[[:space:]]*\[[^]]+\][[:space:]]*$/ {
            section=$0
            gsub(/^[[:space:]]*\[|\][[:space:]]*$/, "", section)
            in_service=(section == "Service")
            next
        }
        !in_service || /^[[:space:]]*[#;]/ { next }
        /^[[:space:]]*ExecStart[[:space:]]*=/ {
            line=$0
            sub(/^[[:space:]]*ExecStart[[:space:]]*=[[:space:]]*/, "", line)
            if (line ~ /^[[:space:]]*$/) next
            split(line, fields, /[[:space:]]+/)
            if (fields[1] != executable) invalid=1
            else found=1
        }
        END { exit (found && !invalid ? 0 : 1) }
    ' "$file"
}

unit_fragment_matches_state() {
    local expected_state="$1" fragment='' fragment_target=''

    fragment="$(systemctl show -p FragmentPath --value keepalived.service 2>/dev/null || true)"
    case "$expected_state" in
        absent)
            [[ -z "$fragment" ]]
            ;;
        legacy-link)
            [[ -n "$fragment" ]] || return 1
            fragment_target="$(readlink -f -- "$fragment" 2>/dev/null || true)"
            [[ "$fragment_target" == "$BUILT_UNIT" ]]
            ;;
        managed-file)
            [[ -n "$fragment" ]] || return 1
            fragment_target="$(readlink -f -- "$fragment" 2>/dev/null || true)"
            [[ "$fragment_target" == "$UNIT_FILE" ]]
            ;;
        *) return 1 ;;
    esac
}

capture_uninstall_unit_state() {
    UNIT_STATE='absent'
    UNIT_LINK_TARGET=''
    UNIT_FILE_SHA256=''
    if [[ -L "$UNIT_FILE" ]]; then
        UNIT_LINK_TARGET="$(readlink -f -- "$UNIT_FILE" 2>/dev/null || true)"
        [[ "$UNIT_LINK_TARGET" == "$BUILT_UNIT" ]] || die "系统 keepalived.service 不属于此安装：$UNIT_LINK_TARGET"
        UNIT_STATE='legacy-link'
    elif [[ -e "$UNIT_FILE" ]]; then
        is_managed_unit_file "$UNIT_FILE" || die "系统 keepalived.service 不属于此安装：$UNIT_FILE"
        UNIT_STATE='managed-file'
        UNIT_FILE_SHA256="$(sha256sum "$UNIT_FILE" | awk '{print $1}')"
    fi
}

capture_uninstall_state() {
    SERVICE_WAS_ACTIVE=0
    SERVICE_WAS_ENABLED=0
    systemctl is-active --quiet keepalived.service && SERVICE_WAS_ACTIVE=1 || SERVICE_WAS_ACTIVE=0
    systemctl is-enabled --quiet keepalived.service && SERVICE_WAS_ENABLED=1 || SERVICE_WAS_ENABLED=0
}

uninstall_unit_state_matches_capture() {
    local current_sha=''

    case "$UNIT_STATE" in
        absent)
            [[ ! -e "$UNIT_FILE" && ! -L "$UNIT_FILE" ]]
            ;;
        legacy-link)
            [[ -L "$UNIT_FILE" ]] || return 1
            [[ "$(readlink -f -- "$UNIT_FILE" 2>/dev/null || true)" == "$UNIT_LINK_TARGET" ]]
            ;;
        managed-file)
            is_managed_unit_file "$UNIT_FILE" || return 1
            current_sha="$(sha256sum "$UNIT_FILE" | awk '{print $1}')"
            [[ "$current_sha" == "$UNIT_FILE_SHA256" ]]
            ;;
        *) return 1 ;;
    esac
}

require_uninstall_unit_state_unchanged() {
    uninstall_service_control_gate || die "keepalived.service or FragmentPath changed before service mutation"
}

uninstall_service_control_gate() {
    uninstall_unit_state_matches_capture && unit_fragment_matches_state "$UNIT_STATE"
}

create_and_verify_backup() {
    umask 077
    install -d -o root -g root -m 700 "$BACKUP_ROOT"
    BACKUP_DIR="$BACKUP_ROOT/keepalived-$(date -u +%Y%m%dT%H%M%SZ)"
    [[ ! -e "$BACKUP_DIR" ]] || die "备份目录已存在：$BACKUP_DIR"
    install -d -o root -g root -m 700 "$BACKUP_DIR"

    cp -a -- "$APP_ROOT" "$BACKUP_DIR/installed-root"
    if [[ "$UNIT_STATE" == 'managed-file' ]]; then
        cp -a -- "$UNIT_FILE" "$BACKUP_DIR/systemd-unit"
    fi

    cat >"$BACKUP_DIR/uninstall-manifest.txt" <<EOF
installed_root=$APP_ROOT
created_at_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)
service_was_active=$SERVICE_WAS_ACTIVE
service_was_enabled=$SERVICE_WAS_ENABLED
unit_state=$UNIT_STATE
unit_link_target=$UNIT_LINK_TARGET
unit_file_sha256=$UNIT_FILE_SHA256
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
        [[ "$action" != unchanged ]] || continue
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
        [[ "$action" != unchanged ]] || continue
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

restore_uninstall_disabled_legacy_link() {
    local unit_tmp="$UNIT_FILE.rollback.$$"

    [[ "$UNIT_STATE" == 'legacy-link' ]] || return 0
    uninstall_unit_state_matches_capture && return 0

    if [[ -e "$UNIT_FILE" || -L "$UNIT_FILE" ]]; then
        UNIT_ROLLBACK_CONFLICT=1
        return 1
    fi
    [[ "$UNIT_LINK_TARGET" == "$BUILT_UNIT" ]] || return 1
    rm -f -- "$unit_tmp" || return 1
    ln -sT -- "$UNIT_LINK_TARGET" "$unit_tmp" || return 1
    if [[ -e "$UNIT_FILE" || -L "$UNIT_FILE" ]]; then
        rm -f -- "$unit_tmp"
        UNIT_ROLLBACK_CONFLICT=1
        return 1
    fi
    mv -Tn -- "$unit_tmp" "$UNIT_FILE" || {
        rm -f -- "$unit_tmp"
        [[ ! -e "$UNIT_FILE" && ! -L "$UNIT_FILE" ]] || UNIT_ROLLBACK_CONFLICT=1
        return 1
    }
    if [[ -e "$unit_tmp" || -L "$unit_tmp" ]]; then
        rm -f -- "$unit_tmp"
        UNIT_ROLLBACK_CONFLICT=1
        return 1
    fi
    systemctl daemon-reload || return 1
    uninstall_unit_state_matches_capture || {
        UNIT_ROLLBACK_CONFLICT=1
        return 1
    }
    unit_fragment_matches_state legacy-link || { UNIT_ROLLBACK_CONFLICT=1; return 1; }
}

restore_uninstall_unit_and_service() {
    local restored_sha='' rollback_status=0 unit_tmp="$UNIT_FILE.rollback.$$"
    local unit_reused=0

    UNIT_ROLLBACK_CONFLICT=0
    case "$UNIT_STATE" in
        absent)
            if [[ -e "$UNIT_FILE" || -L "$UNIT_FILE" ]]; then
                UNIT_ROLLBACK_CONFLICT=1
                return 1
            fi
            ;;
        legacy-link)
            [[ "$UNIT_LINK_TARGET" == "$BUILT_UNIT" ]] || return 1
            if uninstall_unit_state_matches_capture; then
                unit_reused=1
            elif [[ ! -e "$UNIT_FILE" && ! -L "$UNIT_FILE" ]]; then
                rm -f -- "$unit_tmp" || return 1
                ln -sT -- "$UNIT_LINK_TARGET" "$unit_tmp" || return 1
                [[ ! -e "$UNIT_FILE" && ! -L "$UNIT_FILE" ]] || { rm -f -- "$unit_tmp"; UNIT_ROLLBACK_CONFLICT=1; return 1; }
                mv -Tn -- "$unit_tmp" "$UNIT_FILE" || { rm -f -- "$unit_tmp"; return 1; }
                [[ ! -e "$unit_tmp" && ! -L "$unit_tmp" ]] || { rm -f -- "$unit_tmp"; UNIT_ROLLBACK_CONFLICT=1; return 1; }
            else
                UNIT_ROLLBACK_CONFLICT=1
                return 1
            fi
            ;;
        managed-file)
            if uninstall_unit_state_matches_capture; then
                unit_reused=1
            elif [[ ! -e "$UNIT_FILE" && ! -L "$UNIT_FILE" ]]; then
                [[ -f "$BACKUP_DIR/systemd-unit" ]] || return 1
                restored_sha="$(sha256sum "$BACKUP_DIR/systemd-unit" | awk '{print $1}')"
                [[ "$restored_sha" == "$UNIT_FILE_SHA256" ]] || return 1
                rm -f -- "$unit_tmp" || return 1
                install -o root -g root -m 0644 "$BACKUP_DIR/systemd-unit" "$unit_tmp" || return 1
                [[ ! -e "$UNIT_FILE" && ! -L "$UNIT_FILE" ]] || { rm -f -- "$unit_tmp"; UNIT_ROLLBACK_CONFLICT=1; return 1; }
                mv -Tn -- "$unit_tmp" "$UNIT_FILE" || { rm -f -- "$unit_tmp"; return 1; }
                [[ ! -e "$unit_tmp" && ! -L "$unit_tmp" ]] || { rm -f -- "$unit_tmp"; UNIT_ROLLBACK_CONFLICT=1; return 1; }
            else
                UNIT_ROLLBACK_CONFLICT=1
                return 1
            fi
            ;;
        *) return 1 ;;
    esac
    if [[ "$unit_reused" -eq 1 ]]; then
        log "reusing unchanged captured systemd unit during rollback"
    fi
    systemctl daemon-reload || return 1
    uninstall_unit_state_matches_capture || { UNIT_ROLLBACK_CONFLICT=1; return 1; }
    unit_fragment_matches_state "$UNIT_STATE" || { UNIT_ROLLBACK_CONFLICT=1; return 1; }
    if [[ "$SERVICE_WAS_ENABLED" -eq 1 ]]; then
        uninstall_service_control_gate || { UNIT_ROLLBACK_CONFLICT=1; return 1; }
        systemctl enable keepalived.service || rollback_status=1
    else
        uninstall_service_control_gate || { UNIT_ROLLBACK_CONFLICT=1; return 1; }
        systemctl disable keepalived.service || rollback_status=1
        restore_uninstall_disabled_legacy_link || return 1
    fi
    uninstall_unit_state_matches_capture || { UNIT_ROLLBACK_CONFLICT=1; return 1; }
    unit_fragment_matches_state "$UNIT_STATE" || { UNIT_ROLLBACK_CONFLICT=1; return 1; }
    if [[ "$SERVICE_WAS_ACTIVE" -eq 1 ]]; then
        uninstall_service_control_gate || { UNIT_ROLLBACK_CONFLICT=1; return 1; }
        systemctl restart keepalived.service || rollback_status=1
    else
        uninstall_service_control_gate || { UNIT_ROLLBACK_CONFLICT=1; return 1; }
        systemctl stop keepalived.service || rollback_status=1
    fi
    verify_uninstall_service_state_restored || rollback_status=1
    return "$rollback_status"
}

verify_uninstall_service_state_restored() {
    local active_now=0 enabled_now=0

    systemctl is-active --quiet keepalived.service && active_now=1 || active_now=0
    systemctl is-enabled --quiet keepalived.service && enabled_now=1 || enabled_now=0
    [[ "$active_now" -eq "$SERVICE_WAS_ACTIVE" && "$enabled_now" -eq "$SERVICE_WAS_ENABLED" ]]
}

rollback_uninstall_transaction() {
    local rollback_status=0

    if [[ -z "$BACKUP_DIR" || ! -d "$BACKUP_DIR/installed-root" ]] ||
       ! (cd "$BACKUP_DIR" && sha256sum --check BACKUP.sha256); then
        return 1
    fi
    if [[ "$ROOT_MUTATION_STARTED" -eq 1 ]]; then
        if [[ -e "$APP_ROOT" || -L "$APP_ROOT" ]]; then
            safe_remove_uninstall_root || return 1
        fi
        cp -a -- "$BACKUP_DIR/installed-root" "$APP_ROOT" || return 1
    fi
    rollback_uninstall_selinux_journal || rollback_status=1
    rollback_uninstall_firewall_journal || rollback_status=1
    restore_uninstall_unit_and_service || rollback_status=1
    return "$rollback_status"
}

preflight_uninstall_state() {
    [[ -d "$APP_ROOT" ]] || die "Keepalived installation root not found: $APP_ROOT"
    [[ "$(readlink -f -- "$APP_ROOT")" == "$APP_ROOT" ]] || die "refusing unexpected Keepalived installation path"
    mountpoint -q "$APP_ROOT" && die "refusing to uninstall a mounted application root"
    capture_uninstall_unit_state
    unit_fragment_matches_state "$UNIT_STATE" || die "keepalived.service FragmentPath does not match captured unit state: $UNIT_STATE"
    capture_uninstall_state
    preflight_firewall_state
    preflight_selinux_state
}

perform_uninstall_mutations() {
    require_uninstall_unit_state_unchanged
    systemctl stop keepalived.service
    systemctl is-active --quiet keepalived.service && die "keepalived.service remained active after stop"
    require_uninstall_unit_state_unchanged
    systemctl disable keepalived.service
    case "$UNIT_STATE" in
        managed-file)
            is_managed_unit_file "$UNIT_FILE" || die "keepalived.service changed before removal"
            [[ "$(sha256sum "$UNIT_FILE" | awk '{print $1}')" == "$UNIT_FILE_SHA256" ]] || die "keepalived.service changed before removal"
            ;;
        legacy-link)
            if [[ -e "$UNIT_FILE" || -L "$UNIT_FILE" ]]; then
                [[ -L "$UNIT_FILE" && "$(readlink -f -- "$UNIT_FILE")" == "$BUILT_UNIT" ]] || die "keepalived.service changed before removal"
            fi
            ;;
        absent) ;;
        *) die "invalid captured keepalived.service state" ;;
    esac
    if [[ "$UNIT_STATE" != 'absent' && ( -e "$UNIT_FILE" || -L "$UNIT_FILE" ) ]]; then
        rm -f -- "$UNIT_FILE"
    fi
    systemctl daemon-reload
    unit_fragment_matches_state absent || die "keepalived.service FragmentPath remained loaded after unit removal"
    remove_owned_firewall_rule
    restore_selinux_mappings
    ROOT_MUTATION_STARTED=1
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
