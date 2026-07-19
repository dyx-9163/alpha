#!/usr/bin/env bash
set -Eeuo pipefail

# Zero-argument source installer for openEuler 24.03 LTS SP3 x86_64.
# Place keepalived-2.4.2.tar.gz beside this script. Build dependencies are
# installed from the DNF repositories already configured on the server.

readonly APP_ROOT="/aifar/apps/keepalived"
readonly KEEPALIVED_VERSION="2.4.2"
readonly SOURCE_ARCHIVE_NAME="keepalived-${KEEPALIVED_VERSION}.tar.gz"
readonly SOURCE_ARCHIVE_SHA256="76397ad758ae871dfa713b9fc6b4ead754db7964809a3969e40c2d288bc3460b"
readonly SELINUX_SCRIPT_NAME="configure-selinux.sh"

SCRIPT_PATH="$(readlink -f -- "${BASH_SOURCE[0]}")"
SCRIPT_DIR="$(dirname -- "$SCRIPT_PATH")"
SOURCE_ARCHIVE="${SCRIPT_DIR}/${SOURCE_ARCHIVE_NAME}"
SELINUX_SCRIPT="${SCRIPT_DIR}/${SELINUX_SCRIPT_NAME}"
readonly NODE_CONFIG="${SCRIPT_DIR}/keepalived.env"
readonly CONFIG_TEMPLATE="${SCRIPT_DIR}/keepalived.conf.tpl"
readonly HEALTH_SCRIPT_SOURCE="${SCRIPT_DIR}/check-aggregate-health.sh"
readonly FORMAL_CONFIG="${APP_ROOT}/etc/keepalived/keepalived.conf"
readonly HEALTH_URL_FILE="${APP_ROOT}/etc/keepalived/keepalived-health-url"
readonly FIREWALL_RECORD="${APP_ROOT}/var/lib/aifar/firewall-rule"
readonly SELINUX_RECORD="${APP_ROOT}/var/lib/aifar/keepalived-selinux-fcontexts"
readonly BACKUP_ROOT="/aifar/backups"
readonly UNIT_LINK="/etc/systemd/system/keepalived.service"
readonly EXPECTED_UNIT="${APP_ROOT}/systemd/keepalived.service"
NODE_LOCAL_IP=""
NODE_PEER_IP=""
NODE_VIP_CIDR=""
NODE_INTERFACE=""
NODE_PRIORITY=""
NODE_VIRTUAL_ROUTER_ID=""
NODE_HEALTH_URL=""
WORK_DIR=""
TRANSACTION_ACTIVE=0
APP_ROOT_EXISTED=0
SERVICE_WAS_ACTIVE=0
SERVICE_WAS_ENABLED=0
UNIT_LINK_EXISTED=0
UNIT_LINK_TARGET=''
BACKUP_DIR=''

log() {
    printf '[keepalived-installer] %s\n' "$*"
}

die() {
    printf '[keepalived-installer] ERROR: %s\n' "$*" >&2
    exit 1
}

[[ -f "$HEALTH_SCRIPT_SOURCE" ]] || die "缺少健康检查脚本：$HEALTH_SCRIPT_SOURCE"
# shellcheck source=check-aggregate-health.sh
source "$HEALTH_SCRIPT_SOURCE"

cleanup() {
    local status=$?
    local rollback_status=0
    local resolved_work_dir=""
    trap - EXIT

    if [[ "$status" -ne 0 && "$TRANSACTION_ACTIVE" -eq 1 ]]; then
        set +e
        rollback_install_transaction
        rollback_status=$?
        if [[ "$rollback_status" -ne 0 ]]; then
            printf '[keepalived-installer] ERROR: 安装失败后的回滚也未完整成功；请检查上方回滚日志。\n' >&2
        fi
    fi

    if [[ -n "$WORK_DIR" && "$WORK_DIR" == /tmp/keepalived-offline.* && -d "$WORK_DIR" ]]; then
        resolved_work_dir="$(readlink -f -- "$WORK_DIR" 2>/dev/null || true)"
        if [[ "$resolved_work_dir" == "$WORK_DIR" && "$resolved_work_dir" == /tmp/keepalived-offline.* ]]; then
            if mountpoint -q "$WORK_DIR"; then
                printf '[keepalived-installer] WARNING: 拒绝递归清理临时挂载点：%s\n' "$WORK_DIR" >&2
            else
                rm -rf -- "$WORK_DIR" || printf '[keepalived-installer] WARNING: 临时目录清理失败：%s\n' "$WORK_DIR" >&2
            fi
        else
            printf '[keepalived-installer] WARNING: 拒绝清理非预期临时目录：%s\n' "$WORK_DIR" >&2
        fi
    fi

    exit "$status"
}

trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

require_command() {
    command -v "$1" >/dev/null 2>&1 || die "缺少必要命令：$1"
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

valid_selinux_journal_row() {
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
        *) valid_selinux_record_row "$action" "$pattern" "$previous_type" "$applied_type" ;;
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

mapping_context() {
    semanage fcontext -l -C | PATTERN="$1" awk '$1 == ENVIRON["PATTERN"] { print $NF; exit }'
}

context_type() {
    local context="$1" type=""

    IFS=: read -r _ _ type _ <<<"$context"
    [[ "$type" =~ ^[A-Za-z0-9_]+_t$ ]] || return 1
    printf '%s\n' "$type"
}

capture_service_state() {
    SERVICE_WAS_ACTIVE=0
    SERVICE_WAS_ENABLED=0
    UNIT_LINK_EXISTED=0
    UNIT_LINK_TARGET=''
    systemctl is-active --quiet keepalived.service && SERVICE_WAS_ACTIVE=1 || SERVICE_WAS_ACTIVE=0
    systemctl is-enabled --quiet keepalived.service && SERVICE_WAS_ENABLED=1 || SERVICE_WAS_ENABLED=0
    if [[ -e "$UNIT_LINK" || -L "$UNIT_LINK" ]]; then
        UNIT_LINK_EXISTED=1
        UNIT_LINK_TARGET="$(readlink -f -- "$UNIT_LINK" 2>/dev/null || true)"
    fi
}

validate_unit_ownership() {
    local unit_target=""
    local fragment=""
    local fragment_target=""

    if [[ -e "$UNIT_LINK" || -L "$UNIT_LINK" ]]; then
        unit_target="$(readlink -f -- "$UNIT_LINK" 2>/dev/null || true)"
        [[ -n "$unit_target" ]] || die "无法解析现有 keepalived.service unit：$UNIT_LINK"
        [[ "$unit_target" == "$EXPECTED_UNIT" ]] || die "系统 keepalived.service 不属于此安装：$unit_target"
    fi

    fragment="$(systemctl show -p FragmentPath --value keepalived.service 2>/dev/null || true)"
    if [[ -n "$fragment" ]]; then
        fragment_target="$(readlink -f -- "$fragment" 2>/dev/null || true)"
        [[ -n "$fragment_target" ]] || die "无法解析已加载的 keepalived.service unit：$fragment"
        [[ "$fragment_target" == "$EXPECTED_UNIT" ]] || die "已加载的 keepalived.service 不属于此安装：$fragment_target"
    fi
}

create_install_backup() {
    local resolved_root=""

    validate_unit_ownership
    TRANSACTION_ACTIVE=0
    APP_ROOT_EXISTED=0
    if [[ -e "$APP_ROOT" || -L "$APP_ROOT" ]]; then
        resolved_root="$(readlink -f -- "$APP_ROOT" 2>/dev/null || true)"
        [[ "$resolved_root" == "/aifar/apps/keepalived" ]] || die "现有安装目录不是预期路径：$resolved_root"
        [[ -d "$APP_ROOT" ]] || die "现有安装路径不是目录：$APP_ROOT"
        mountpoint -q "$APP_ROOT" && die "现有安装目录是挂载点，无法保证事务回滚：$APP_ROOT"
    fi
    umask 077
    install -d -o root -g root -m 700 "$BACKUP_ROOT"
    BACKUP_DIR="$BACKUP_ROOT/keepalived-update-$(date -u +%Y%m%dT%H%M%SZ)-$$"
    install -d -o root -g root -m 700 "$BACKUP_DIR"
    if [[ -d "$APP_ROOT" ]]; then
        APP_ROOT_EXISTED=1
        cp -a -- "$APP_ROOT" "$BACKUP_DIR/installed-root"
    fi
    printf 'app_root_existed=%s\nservice_was_active=%s\nservice_was_enabled=%s\nunit_link_existed=%s\nunit_link_target=%s\n' \
        "$APP_ROOT_EXISTED" "$SERVICE_WAS_ACTIVE" "$SERVICE_WAS_ENABLED" "$UNIT_LINK_EXISTED" "$UNIT_LINK_TARGET" \
        >"$BACKUP_DIR/install-state.txt"
    (cd "$BACKUP_DIR" && find . -type f ! -name BACKUP.sha256 -print0 | sort -z | xargs -0 sha256sum >BACKUP.sha256 && sha256sum --check BACKUP.sha256)
    TRANSACTION_ACTIVE=1
}

safe_remove_app_root() {
    local resolved_root=""

    resolved_root="$(readlink -f -- "$APP_ROOT" 2>/dev/null || true)"
    if [[ "$resolved_root" != "/aifar/apps/keepalived" ]]; then
        printf '[keepalived-installer] ERROR: 拒绝递归删除非预期安装路径：%s\n' "$resolved_root" >&2
        return 1
    fi
    if mountpoint -q "$APP_ROOT"; then
        printf '[keepalived-installer] ERROR: 拒绝递归删除挂载点：%s\n' "$APP_ROOT" >&2
        return 1
    fi
    rm -rf -- "$APP_ROOT"
}

firewall_rule_for_peer() {
    printf 'rule family="ipv4" source address="%s/32" protocol value="112" accept\n' "$1"
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

firewall_rule_exists() {
    local form="$1" zone="$2" rule="$3" status=0

    if [[ "$form" == permanent ]]; then
        firewall-cmd --permanent --zone="$zone" --query-rich-rule="$rule" >/dev/null 2>&1 && return 0
    else
        firewall-cmd --zone="$zone" --query-rich-rule="$rule" >/dev/null 2>&1 && return 0
    fi
    status=$?
    [[ "$status" -eq 1 ]] && return 1
    die "查询 firewalld $form 规则失败（状态 $status）"
}

mutate_firewall_rule() {
    local mutation="$1" form="$2" zone="$3" rule="$4"
    local command_option="--${mutation}-rich-rule=$rule"
    local journal_action=""

    if [[ "$mutation" == add ]]; then
        journal_action=added
    else
        journal_action=removed
    fi
    append_firewall_journal "$journal_action" "$form" "$zone" "$rule"

    if [[ "$form" == permanent ]]; then
        firewall-cmd --permanent --zone="$zone" "$command_option" >/dev/null || die "更新 firewalld permanent 规则失败"
    else
        firewall-cmd --zone="$zone" "$command_option" >/dev/null || die "更新 firewalld runtime 规则失败"
    fi
}

append_firewall_journal() {
    local action="$1" form="$2" zone="$3" rule="$4"
    local journal="$WORK_DIR/firewall-journal.tsv"
    local row="${action}"$'\t'"${form}"$'\t'"${zone}"$'\t'"${rule}"
    local -a before=() after=()

    if [[ -e "$journal" || -L "$journal" ]]; then
        [[ -f "$journal" && ! -L "$journal" ]] || die "防火墙事务日志不是普通文件：$journal"
        mapfile -t before <"$journal" || die "无法读取防火墙事务日志：$journal"
    fi
    printf '%s\n' "$row" >>"$journal" || die "无法预写防火墙事务日志：$journal"
    mapfile -t after <"$journal" || die "无法验证防火墙事务日志：$journal"
    [[ "${#after[@]}" -eq $((${#before[@]} + 1)) ]] || die "防火墙事务日志追加行数校验失败"
    [[ "${after[-1]}" == "$row" ]] || die "防火墙事务日志追加内容校验失败"
}

rollback_firewall_journal() {
    local journal="$WORK_DIR/firewall-journal.tsv"
    local index=0 action="" form="" zone="" rule="" rollback_status=0 query_status=0
    local -a rows=()

    [[ -s "$journal" ]] || return 0
    mapfile -t rows <"$journal"
    for ((index=${#rows[@]} - 1; index >= 0; index--)); do
        IFS=$'\t' read -r action form zone rule <<<"${rows[$index]}"
        if ! valid_firewall_zone "$zone" || ! valid_peer_firewall_rule "$rule" || [[ "$form" != runtime && "$form" != permanent ]]; then
            printf '[keepalived-installer] ERROR: 防火墙回滚日志记录无效\n' >&2
            rollback_status=1
            continue
        fi
        if [[ "$form" == permanent ]]; then
            if firewall-cmd --permanent --zone="$zone" --query-rich-rule="$rule" >/dev/null 2>&1; then
                query_status=0
            else
                query_status=$?
            fi
        else
            if firewall-cmd --zone="$zone" --query-rich-rule="$rule" >/dev/null 2>&1; then
                query_status=0
            else
                query_status=$?
            fi
        fi
        if [[ "$query_status" -ne 0 && "$query_status" -ne 1 ]]; then
            printf '[keepalived-installer] ERROR: 回滚时查询 firewalld %s 规则失败（状态 %s）\n' "$form" "$query_status" >&2
            rollback_status=1
            continue
        fi
        case "$action" in
            added)
                if [[ "$query_status" -eq 0 ]]; then
                    if [[ "$form" == permanent ]]; then
                        firewall-cmd --permanent --zone="$zone" --remove-rich-rule="$rule" >/dev/null || rollback_status=1
                    else
                        firewall-cmd --zone="$zone" --remove-rich-rule="$rule" >/dev/null || rollback_status=1
                    fi
                fi
                ;;
            removed)
                if [[ "$query_status" -eq 1 ]]; then
                    if [[ "$form" == permanent ]]; then
                        firewall-cmd --permanent --zone="$zone" --add-rich-rule="$rule" >/dev/null || rollback_status=1
                    else
                        firewall-cmd --zone="$zone" --add-rich-rule="$rule" >/dev/null || rollback_status=1
                    fi
                fi
                ;;
            *)
                printf '[keepalived-installer] ERROR: 防火墙回滚日志动作无效：%s\n' "$action" >&2
                rollback_status=1
                ;;
        esac
    done
    return "$rollback_status"
}

rollback_selinux_journal() {
    local journal="$WORK_DIR/selinux-journal.tsv"
    local index=0 action="" pattern="" previous_type="" applied_type="" rollback_status=0
    local -a rows=()

    [[ -e "$journal" || -L "$journal" ]] || return 0
    if [[ ! -f "$journal" || -L "$journal" ]]; then
        printf '[keepalived-installer] ERROR: SELinux 回滚日志不是普通文件：%s\n' "$journal" >&2
        return 1
    fi
    command -v semanage >/dev/null 2>&1 || {
        printf '[keepalived-installer] ERROR: SELinux 回滚缺少 semanage 命令\n' >&2
        return 1
    }
    mapfile -t rows <"$journal" || return 1
    for ((index=${#rows[@]} - 1; index >= 0; index--)); do
        if [[ ! "${rows[$index]}" =~ ^([^|]*)[|]([^|]*)[|]([^|]*)[|]([^|]*)$ ]]; then
            printf '[keepalived-installer] ERROR: SELinux 回滚日志记录无效\n' >&2
            rollback_status=1
            continue
        fi
        action="${BASH_REMATCH[1]}"
        pattern="${BASH_REMATCH[2]}"
        previous_type="${BASH_REMATCH[3]}"
        applied_type="${BASH_REMATCH[4]}"
        if ! valid_selinux_journal_row "$action" "$pattern" "$previous_type" "$applied_type" || [[ "$action" == unchanged ]]; then
            printf '[keepalived-installer] ERROR: SELinux 回滚日志语义无效：%s\n' "$pattern" >&2
            rollback_status=1
            continue
        fi
        local current_context="" current_type="" restored_context="" restored_type=""
        current_context="$(mapping_context "$pattern")"
        if [[ "$action" == retired_created ]]; then
            if [[ -n "$current_context" ]]; then
                current_type="$(context_type "$current_context")" || current_type=""
                if [[ "$current_type" != "$applied_type" ]]; then
                    printf '[keepalived-installer] ERROR: retired SELinux mapping was externally modified: %s\n' "$pattern" >&2
                    rollback_status=1
                fi
            elif ! semanage fcontext -a -t "$applied_type" "$pattern"; then
                rollback_status=1
            else
                restored_context="$(mapping_context "$pattern")"
                restored_type="$(context_type "$restored_context")" || restored_type=""
                [[ "$restored_type" == "$applied_type" ]] || rollback_status=1
            fi
            continue
        fi
        if [[ "$action" == retired_updated ]]; then
            if [[ -z "$current_context" ]]; then
                printf '[keepalived-installer] ERROR: retired SELinux mapping is missing: %s\n' "$pattern" >&2
                rollback_status=1
                continue
            fi
            current_type="$(context_type "$current_context")" || current_type=""
            if [[ "$current_type" == "$applied_type" ]]; then
                continue
            fi
            if [[ "$current_type" != "$previous_type" ]]; then
                printf '[keepalived-installer] ERROR: retired SELinux mapping was externally modified: %s\n' "$pattern" >&2
                rollback_status=1
            elif ! semanage fcontext -m -t "$applied_type" "$pattern"; then
                rollback_status=1
            else
                restored_context="$(mapping_context "$pattern")"
                restored_type="$(context_type "$restored_context")" || restored_type=""
                [[ "$restored_type" == "$applied_type" ]] || rollback_status=1
            fi
            continue
        fi
        if [[ -z "$current_context" ]]; then
            printf '[keepalived-installer] ERROR: SELinux 回滚映射已缺失：%s\n' "$pattern" >&2
            rollback_status=1
            continue
        fi
        current_type="$(context_type "$current_context")" || {
            printf '[keepalived-installer] ERROR: 无法解析 SELinux 回滚映射：%s\n' "$pattern" >&2
            rollback_status=1
            continue
        }
        if [[ "$current_type" != "$applied_type" ]]; then
            printf '[keepalived-installer] ERROR: SELinux 映射已被外部修改，拒绝覆盖：%s\n' "$pattern" >&2
            rollback_status=1
            continue
        fi
        case "$action" in
            created)
                if ! semanage fcontext -d "$pattern"; then
                    rollback_status=1
                elif [[ -n "$(mapping_context "$pattern")" ]]; then
                    printf '[keepalived-installer] ERROR: SELinux created 映射回滚校验失败：%s\n' "$pattern" >&2
                    rollback_status=1
                fi
                ;;
            updated)
                if ! semanage fcontext -m -t "$previous_type" "$pattern"; then
                    rollback_status=1
                else
                    restored_context="$(mapping_context "$pattern")"
                    restored_type="$(context_type "$restored_context")" || restored_type=""
                    if [[ "$restored_type" != "$previous_type" ]]; then
                        printf '[keepalived-installer] ERROR: SELinux updated 映射回滚校验失败：%s\n' "$pattern" >&2
                        rollback_status=1
                    fi
                fi
                ;;
        esac
    done
    return "$rollback_status"
}

preflight_firewall_reconciliation() {
    local record_exists=0

    if [[ -e "$FIREWALL_RECORD" || -L "$FIREWALL_RECORD" ]]; then
        [[ -f "$FIREWALL_RECORD" && ! -L "$FIREWALL_RECORD" ]] || die "firewall ownership record must be a regular non-symlink file: $FIREWALL_RECORD"
        parse_firewall_record "$FIREWALL_RECORD"
        record_exists=1
    fi
    if ! systemctl is-active --quiet firewalld.service; then
        [[ "$record_exists" -eq 0 ]] || die "existing firewall ownership record requires active firewalld before reinstall"
        return 0
    fi
    return 0
}

reconcile_firewall_rule() {
    local zone="" desired_rule="" record_tmp=""
    local runtime_created=0 permanent_created=0
    local old_record_exists=0 old_same=0

    preflight_firewall_reconciliation
    if ! systemctl is-active --quiet firewalld.service; then
        log "firewalld 未运行，跳过 VRRP peer 防火墙规则"
        return 0
    fi
    require_command firewall-cmd
    if ! zone="$(firewall-cmd --get-zone-of-interface="$NODE_INTERFACE" 2>/dev/null)"; then
        die "无法获取接口 $NODE_INTERFACE 的 firewalld zone"
    fi
    if [[ -z "$zone" || "$zone" == "no zone" ]]; then
        zone="$(firewall-cmd --get-default-zone)" || die "无法获取 firewalld 默认 zone"
    fi
    valid_firewall_zone "$zone" || die "firewalld zone 无效：$zone"
    desired_rule="$(firewall_rule_for_peer "$NODE_PEER_IP")"
    : >"$WORK_DIR/firewall-journal.tsv"

    if [[ -e "$FIREWALL_RECORD" || -L "$FIREWALL_RECORD" ]]; then
        [[ -f "$FIREWALL_RECORD" && ! -L "$FIREWALL_RECORD" ]] || die "防火墙所有权记录不是普通文件：$FIREWALL_RECORD"
        parse_firewall_record "$FIREWALL_RECORD"
        old_record_exists=1
        if [[ "$FIREWALL_RECORD_ZONE" == "$zone" && "$FIREWALL_RECORD_RULE" == "$desired_rule" ]]; then
            old_same=1
        else
            if [[ "$FIREWALL_RECORD_RUNTIME_CREATED" -eq 1 ]] && firewall_rule_exists runtime "$FIREWALL_RECORD_ZONE" "$FIREWALL_RECORD_RULE"; then
                mutate_firewall_rule remove runtime "$FIREWALL_RECORD_ZONE" "$FIREWALL_RECORD_RULE"
            fi
            if [[ "$FIREWALL_RECORD_PERMANENT_CREATED" -eq 1 ]] && firewall_rule_exists permanent "$FIREWALL_RECORD_ZONE" "$FIREWALL_RECORD_RULE"; then
                mutate_firewall_rule remove permanent "$FIREWALL_RECORD_ZONE" "$FIREWALL_RECORD_RULE"
            fi
        fi
    fi

    if firewall_rule_exists runtime "$zone" "$desired_rule"; then
        if [[ "$old_record_exists" -eq 1 && "$old_same" -eq 1 ]]; then
            runtime_created="$FIREWALL_RECORD_RUNTIME_CREATED"
        fi
    else
        mutate_firewall_rule add runtime "$zone" "$desired_rule"
        runtime_created=1
    fi
    if firewall_rule_exists permanent "$zone" "$desired_rule"; then
        if [[ "$old_record_exists" -eq 1 && "$old_same" -eq 1 ]]; then
            permanent_created="$FIREWALL_RECORD_PERMANENT_CREATED"
        fi
    else
        mutate_firewall_rule add permanent "$zone" "$desired_rule"
        permanent_created=1
    fi

    install -d -o root -g root -m 750 "$(dirname -- "$FIREWALL_RECORD")"
    record_tmp="$FIREWALL_RECORD.tmp.$$"
    printf 'zone=%s\nrule=%s\nruntime_created=%s\npermanent_created=%s\n' \
        "$zone" "$desired_rule" "$runtime_created" "$permanent_created" >"$WORK_DIR/firewall-rule"
    install -o root -g root -m 600 "$WORK_DIR/firewall-rule" "$record_tmp"
    mv -f -- "$record_tmp" "$FIREWALL_RECORD"
}

rollback_install_transaction() {
    local rollback_status=0
    local root_restore_allowed=1
    local current_unit_target=""
    local current_unit_value=""

    log "安装事务失败，正在恢复安装前状态"
    rollback_firewall_journal || rollback_status=1
    rollback_selinux_journal || rollback_status=1
    ensure_keepalived_inactive || {
        printf '[keepalived-installer] ERROR: 回滚时停止 keepalived.service 失败\n' >&2
        rollback_status=1
    }

    if [[ "$APP_ROOT_EXISTED" -eq 1 ]]; then
        if [[ -z "$BACKUP_DIR" || "$BACKUP_DIR" != "$BACKUP_ROOT"/keepalived-update-* || ! -d "$BACKUP_DIR/installed-root" ]]; then
            printf '[keepalived-installer] ERROR: 回滚备份目录无效，拒绝替换当前安装目录：%s\n' "$BACKUP_DIR" >&2
            rollback_status=1
            root_restore_allowed=0
        elif ! (cd "$BACKUP_DIR" && sha256sum --check BACKUP.sha256); then
            printf '[keepalived-installer] ERROR: 回滚备份校验失败，拒绝替换当前安装目录：%s\n' "$BACKUP_DIR" >&2
            rollback_status=1
            root_restore_allowed=0
        fi
    fi

    if [[ "$root_restore_allowed" -eq 1 && ( -e "$APP_ROOT" || -L "$APP_ROOT" ) ]]; then
        safe_remove_app_root || {
            rollback_status=1
            root_restore_allowed=0
        }
    fi
    if [[ "$APP_ROOT_EXISTED" -eq 1 && "$root_restore_allowed" -eq 1 ]]; then
        cp -a -- "$BACKUP_DIR/installed-root" "$APP_ROOT" || {
            printf '[keepalived-installer] ERROR: 回滚时恢复原安装目录失败\n' >&2
            rollback_status=1
        }
    fi

    systemctl daemon-reload || {
        printf '[keepalived-installer] ERROR: 回滚时 systemd daemon-reload 失败\n' >&2
        rollback_status=1
    }
    if [[ "$SERVICE_WAS_ENABLED" -eq 1 ]]; then
        systemctl enable keepalived.service || rollback_status=1
    else
        systemctl disable keepalived.service || rollback_status=1
    fi
    if [[ "$SERVICE_WAS_ACTIVE" -eq 1 ]]; then
        systemctl restart keepalived.service || rollback_status=1
    else
        ensure_keepalived_inactive || rollback_status=1
    fi

    if [[ -e "$UNIT_LINK" || -L "$UNIT_LINK" ]]; then
        current_unit_target="$(readlink -f -- "$UNIT_LINK" 2>/dev/null || true)"
        current_unit_value="$(readlink -- "$UNIT_LINK" 2>/dev/null || true)"
        if [[ "$current_unit_target" == "$EXPECTED_UNIT" || "$current_unit_value" == "$EXPECTED_UNIT" ]]; then
            rm -f -- "$UNIT_LINK" || {
                printf '[keepalived-installer] ERROR: 回滚时删除事务 unit 链接失败：%s\n' "$UNIT_LINK" >&2
                rollback_status=1
            }
        else
            printf '[keepalived-installer] ERROR: unit 链接已被外部修改，回滚拒绝覆盖：%s\n' "$UNIT_LINK" >&2
            rollback_status=1
        fi
    fi
    if [[ "$UNIT_LINK_EXISTED" -eq 1 && ! -e "$UNIT_LINK" && ! -L "$UNIT_LINK" ]]; then
        if [[ -n "$UNIT_LINK_TARGET" && "$UNIT_LINK_TARGET" == "$EXPECTED_UNIT" ]]; then
            ln -s -- "$UNIT_LINK_TARGET" "$UNIT_LINK" || {
                printf '[keepalived-installer] ERROR: 回滚时恢复原 unit 链接失败：%s\n' "$UNIT_LINK" >&2
                rollback_status=1
            }
        else
            printf '[keepalived-installer] ERROR: 原 unit 链接记录无效，无法恢复：%s\n' "$UNIT_LINK_TARGET" >&2
            rollback_status=1
        fi
    fi
    systemctl daemon-reload || {
        printf '[keepalived-installer] ERROR: 回滚恢复 unit 链接后 systemd daemon-reload 失败\n' >&2
        rollback_status=1
    }

    return "$rollback_status"
}

ensure_keepalived_inactive() {
    local service_state=''
    local service_status=0

    if service_state="$(systemctl is-active keepalived.service 2>/dev/null)"; then
        systemctl stop keepalived.service
        return
    else
        service_status=$?
    fi

    case "$service_status:$service_state" in
        3:inactive|3:failed|4:unknown)
            return 0
            ;;
        *)
            return "$service_status"
            ;;
    esac
}

parse_node_config() {
    local file="$1" line="" key="" value=""
    local line_number=0
    declare -A seen=()
    NODE_LOCAL_IP=""
    NODE_PEER_IP=""
    NODE_VIP_CIDR=""
    NODE_INTERFACE=""
    NODE_PRIORITY=""
    NODE_VIRTUAL_ROUTER_ID=""
    NODE_HEALTH_URL=""
    [[ -r "$file" ]] || die "缺少节点配置：$file；请复制 keepalived.env.example 后修改"
    while IFS= read -r line || [[ -n "$line" ]]; do
        line_number=$((line_number + 1))
        line="${line%$'\r'}"
        [[ -z "$line" || "$line" == \#* ]] && continue
        [[ "$line" =~ ^([A-Z][A-Z0-9_]*)=([^[:space:]]+)$ ]] || die "节点配置第 $line_number 行格式无效"
        key="${BASH_REMATCH[1]}"
        value="${BASH_REMATCH[2]}"
        case "$key" in
            KEEPALIVED_LOCAL_IP|KEEPALIVED_PEER_IP|KEEPALIVED_VIP_CIDR|KEEPALIVED_INTERFACE|KEEPALIVED_PRIORITY|KEEPALIVED_VIRTUAL_ROUTER_ID|KEEPALIVED_HEALTH_URL) ;;
            *) die "节点配置包含未知字段：$key" ;;
        esac
        [[ -z "${seen[$key]+x}" ]] || die "节点配置字段重复：$key"
        seen[$key]=1
        printf -v "NODE_${key#KEEPALIVED_}" '%s' "$value"
    done <"$file"
    local variable="" config_key=""
    for key in LOCAL_IP PEER_IP VIP_CIDR INTERFACE PRIORITY VIRTUAL_ROUTER_ID HEALTH_URL; do
        variable="NODE_$key"
        config_key="KEEPALIVED_$key"
        [[ -n "${seen[$config_key]+x}" && -n "${!variable}" ]] || die "节点配置缺少字段：$config_key"
    done
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

validate_node_config() {
    local vip="${NODE_VIP_CIDR%/*}"
    local prefix="${NODE_VIP_CIDR##*/}"
    local prefix_number=0 priority_number=0 virtual_router_id_number=0
    local authority="${NODE_HEALTH_URL#*://}"
    local host_port="${authority%%/*}"
    local host="${host_port%%:*}"
    valid_ipv4 "$NODE_LOCAL_IP" || die "本机 IP 无效"
    valid_ipv4 "$NODE_PEER_IP" || die "对端 IP 无效"
    [[ "$NODE_LOCAL_IP" != "$NODE_PEER_IP" ]] || die "本机 IP 与对端 IP 不能相同"
    [[ "$NODE_VIP_CIDR" == */* ]] && valid_ipv4 "$vip" || die "VIP/CIDR 无效"
    [[ "$prefix" =~ ^(0|[1-9][0-9]{0,1})$ ]] || die "VIP 前缀必须为 1-32"
    prefix_number=$((10#$prefix))
    ((prefix_number >= 1 && prefix_number <= 32)) || die "VIP 前缀必须为 1-32"
    [[ "$vip" != "$NODE_LOCAL_IP" && "$vip" != "$NODE_PEER_IP" ]] || die "VIP 不能等于节点 IP"
    [[ "$NODE_INTERFACE" =~ ^[A-Za-z0-9_.:-]{1,15}$ ]] || die "接口名称无效"
    [[ "$NODE_PRIORITY" =~ ^(0|[1-9][0-9]{0,2})$ ]] || die "优先级必须为 1-254"
    priority_number=$((10#$NODE_PRIORITY))
    ((priority_number >= 1 && priority_number <= 254)) || die "优先级必须为 1-254"
    [[ "$NODE_VIRTUAL_ROUTER_ID" =~ ^(0|[1-9][0-9]{0,2})$ ]] || die "virtual router id 必须为 1-255"
    virtual_router_id_number=$((10#$NODE_VIRTUAL_ROUTER_ID))
    ((virtual_router_id_number >= 1 && virtual_router_id_number <= 255)) || die "virtual router id 必须为 1-255"
    validate_health_url_shape "$NODE_HEALTH_URL" || die "健康 URL 格式无效"
    [[ "$host" == "$NODE_LOCAL_IP" || "$host" == 127.0.0.1 || "$host" == localhost ]] || die "健康 URL 必须指向本机"
    ip link show dev "$NODE_INTERFACE" >/dev/null 2>&1 || die "接口不存在：$NODE_INTERFACE"
    ip -o -4 addr show dev "$NODE_INTERFACE" | awk -v expected="$NODE_LOCAL_IP" '{split($4,a,"/"); if (a[1]==expected) found=1} END{exit !found}' || die "接口未绑定本机 IP"
    ip -4 route get "$vip" | awk -v expected="$NODE_INTERFACE" '{for(i=1;i<NF;i++) if($i=="dev" && $(i+1)==expected) found=1} END{exit !found}' || die "VIP 不通过指定接口路由"
}

render_keepalived_config() {
    local template="$1" output="$2" content="" router_id=""
    router_id="AIFAR_${NODE_LOCAL_IP//./_}"
    content="$(<"$template")"
    content="${content//@ROUTER_ID@/$router_id}"
    content="${content//@INTERFACE@/$NODE_INTERFACE}"
    content="${content//@VIRTUAL_ROUTER_ID@/$NODE_VIRTUAL_ROUTER_ID}"
    content="${content//@PRIORITY@/$NODE_PRIORITY}"
    content="${content//@LOCAL_IP@/$NODE_LOCAL_IP}"
    content="${content//@PEER_IP@/$NODE_PEER_IP}"
    content="${content//@VIP_CIDR@/$NODE_VIP_CIDR}"
    [[ ! "$content" =~ @[A-Z_]+@ ]] || die "Keepalived 模板仍有未替换字段"
    printf '%s\n' "$content" >"$output"
}

validate_platform() {
    [[ -r /etc/os-release ]] || die "无法读取 /etc/os-release"

    # shellcheck disable=SC1091
    source /etc/os-release

    local os_id="${ID:-}"
    os_id="${os_id,,}"
    [[ "$os_id" == "openeuler" ]] || die "仅支持 openEuler，当前系统 ID=${ID:-unknown}"
    [[ "${VERSION_ID:-}" == 24.03* ]] || die "仅支持 openEuler 24.03 LTS SP3，当前 VERSION_ID=${VERSION_ID:-unknown}"

    local release_text
    release_text="$(cat /etc/os-release /etc/openEuler-release 2>/dev/null || true)"
    grep -qi 'SP3' <<<"$release_text" || die "当前系统不是 openEuler 24.03 LTS SP3"

    [[ "$(uname -m)" == "x86_64" ]] || die "仅支持 x86_64，当前架构为 $(uname -m)"
}

verify_source_archive() {
    local checksum_file="${WORK_DIR}/SHA256SUMS"

    printf '%s  %s\n' "$SOURCE_ARCHIVE_SHA256" "$SOURCE_ARCHIVE_NAME" >"$checksum_file"
    (
        cd "$SCRIPT_DIR"
        sha256sum --check "$checksum_file"
    ) || die "Keepalived 源码包 SHA256 校验失败"
    [[ "$(stat -c '%s' "$SOURCE_ARCHIVE")" == "6350291" ]] || die "Keepalived 源码包大小不匹配"
    tar -tzf "$SOURCE_ARCHIVE" >/dev/null || die "源码包损坏或不是有效的 tar.gz：$SOURCE_ARCHIVE"
}

install_build_dependencies() {
    local -a packages=(
        gcc
        make
        autoconf
        automake
        libtool
        pkgconf-pkg-config
        openssl-devel
        libnl3-devel
        systemd-devel
        curl
        python3
    )

    log "使用服务器当前启用的 DNF 仓库安装编译依赖"
    dnf --assumeyes --setopt=install_weak_deps=False --setopt=keepcache=False install "${packages[@]}"

    local command_name
    for command_name in gcc make autoconf automake autoreconf aclocal pkg-config curl python3; do
        require_command "$command_name"
    done
}

build_and_install_keepalived() {
    local source_parent="${WORK_DIR}/source"
    local source_dir="${source_parent}/keepalived-${KEEPALIVED_VERSION}"
    local jobs

    mkdir -p "$source_parent"
    tar -xzf "$SOURCE_ARCHIVE" -C "$source_parent"
    [[ -d "$source_dir" && -f "$source_dir/autogen.sh" ]] || die "源码包目录结构不符合 keepalived-${KEEPALIVED_VERSION}"

    jobs="$(nproc 2>/dev/null || printf '1')"

    log "生成 configure 脚本"
    (
        cd "$source_dir"
        sh autogen.sh
    )

    log "配置安装目录：$APP_ROOT"
    (
        cd "$source_dir"
        ./configure \
            --prefix="$APP_ROOT" \
            --sysconfdir="$APP_ROOT/etc" \
            --localstatedir="$APP_ROOT/var" \
            --runstatedir="$APP_ROOT/run" \
            --with-samples-dir="$APP_ROOT/etc/keepalived/samples" \
            --with-init=systemd \
            --with-systemdsystemunitdir="$APP_ROOT/systemd"
        make -j "$jobs"
        make install
    )

    mkdir -p \
        "$APP_ROOT/etc/keepalived" \
        "$APP_ROOT/run" \
        "$APP_ROOT/var"
}

register_systemd_unit() {
    local unit_source="$EXPECTED_UNIT"
    local existing_fragment=""
    local resolved_fragment=""

    [[ -f "$unit_source" ]] || die "未生成 systemd 单元：$unit_source"

    if [[ -e "$UNIT_LINK" || -L "$UNIT_LINK" ]]; then
        resolved_fragment="$(readlink -f -- "$UNIT_LINK" 2>/dev/null || true)"
        if [[ "$resolved_fragment" != "$unit_source" ]]; then
            die "系统已存在其他 Keepalived unit：$UNIT_LINK；为避免覆盖，本次安装已停止"
        fi
    else
        existing_fragment="$(systemctl show -p FragmentPath --value keepalived.service 2>/dev/null || true)"
        if [[ -n "$existing_fragment" ]]; then
            resolved_fragment="$(readlink -f -- "$existing_fragment" 2>/dev/null || true)"
            if [[ "$resolved_fragment" != "$unit_source" ]]; then
                die "系统已加载其他 Keepalived unit：$existing_fragment；为避免覆盖，本次安装已停止"
            fi
        fi
        systemctl link "$unit_source"
    fi

    systemctl daemon-reload
}

install_managed_configuration() {
    local staged_config="$1"
    local validation_config="$WORK_DIR/keepalived.validation.conf"
    local config_tmp="$FORMAL_CONFIG.tmp.$$"
    local health_url_tmp="$HEALTH_URL_FILE.tmp.$$"
    local health_script_target="$APP_ROOT/libexec/check-aggregate-health.sh"
    local health_script_tmp="$APP_ROOT/libexec/check-aggregate-health.sh.tmp.$$"
    local line=''

    install -d -o root -g root -m 750 "$APP_ROOT/etc/keepalived" "$APP_ROOT/libexec" "$APP_ROOT/var/lib/aifar"
    install -o root -g root -m 750 "$HEALTH_SCRIPT_SOURCE" "$health_script_tmp"
    : >"$validation_config"
    while IFS= read -r line || [[ -n "$line" ]]; do
        printf '%s\n' "${line//$health_script_target/$health_script_tmp}" >>"$validation_config"
    done <"$staged_config"
    grep -Fq "$health_script_tmp" "$validation_config" || die "临时配置未引用待安装的健康检查脚本"
    "$APP_ROOT/sbin/keepalived" -t -f "$validation_config"

    printf '%s\n' "$NODE_HEALTH_URL" >"$WORK_DIR/keepalived-health-url"
    install -o root -g root -m 640 "$WORK_DIR/keepalived-health-url" "$health_url_tmp"
    install -o root -g root -m 640 "$staged_config" "$config_tmp"

    mv -f -- "$health_script_tmp" "$health_script_target"
    mv -f -- "$health_url_tmp" "$HEALTH_URL_FILE"
    mv -f -- "$config_tmp" "$FORMAL_CONFIG"
    "$APP_ROOT/sbin/keepalived" -t -f "$FORMAL_CONFIG"
}

configure_selinux_if_enabled() {
    if command -v getenforce >/dev/null 2>&1 && [[ "$(getenforce)" != "Disabled" ]]; then
        [[ -f "$SELINUX_SCRIPT" ]] || die "SELinux 已启用，但缺少脚本：$SELINUX_SCRIPT"
        KEEPALIVED_SELINUX_TRANSACTION_FILE="$WORK_DIR/selinux-journal.tsv" bash "$SELINUX_SCRIPT"
    fi
}

activate_keepalived() {
    systemctl daemon-reload
    systemctl enable keepalived.service
    if [[ "$SERVICE_WAS_ACTIVE" -eq 1 ]]; then
        systemctl restart keepalived.service
    else
        systemctl start keepalived.service
    fi
    systemctl is-active --quiet keepalived.service || die "keepalived.service 启动失败"
    if ! "$APP_ROOT/libexec/check-aggregate-health.sh"; then
        log "WARNING: 健康检查当前不可用；服务保持 active，VRRP 实例将保持 FAULT"
    fi
}

verify_installation() {
    local binary="$APP_ROOT/sbin/keepalived"
    local unit_file="$APP_ROOT/systemd/keepalived.service"
    local ldd_output

    [[ -x "$binary" ]] || die "Keepalived 二进制不存在或不可执行：$binary"
    "$binary" --version >/dev/null 2>&1 || die "Keepalived 二进制版本检查失败"

    ldd_output="$(ldd "$binary")"
    if grep -q 'not found' <<<"$ldd_output"; then
        printf '%s\n' "$ldd_output" >&2
        die "Keepalived 存在未解析的动态库依赖"
    fi

    grep -Fq "ExecStart=$APP_ROOT/sbin/keepalived" "$unit_file" || die "systemd unit 未引用自定义安装目录"
}

print_next_steps() {
    cat <<EOF

Keepalived ${KEEPALIVED_VERSION} 安装完成。

安装目录：$APP_ROOT
配置示例：$APP_ROOT/etc/keepalived/keepalived.conf.sample
正式配置：$APP_ROOT/etc/keepalived/keepalived.conf

脚本已安装托管配置，并启用和启动 keepalived.service。
可执行以下命令确认运行状态：

  systemctl status keepalived
EOF
}

main() {
    [[ "$#" -eq 0 ]] || die "此脚本不需要任何参数，直接运行：bash install-keepalived-offline.sh"

    if [[ "$EUID" -ne 0 ]]; then
        require_command sudo
        log "需要 root 权限，正在通过 sudo 重新执行"
        exec sudo bash "$SCRIPT_PATH"
    fi

    validate_platform

    for command_name in awk cp date dirname dnf find grep install ip ldd ln mountpoint mv readlink rm sha256sum sort stat systemctl tar xargs; do
        require_command "$command_name"
    done

    [[ -f "$SOURCE_ARCHIVE" ]] || die "请将 $SOURCE_ARCHIVE_NAME 放到脚本同一目录：$SCRIPT_DIR"
    WORK_DIR="$(mktemp -d /tmp/keepalived-offline.XXXXXX)"
    verify_source_archive
    parse_node_config "$NODE_CONFIG"
    validate_node_config
    render_keepalived_config "$CONFIG_TEMPLATE" "$WORK_DIR/keepalived.conf"
    validate_selinux_record_file "$SELINUX_RECORD"
    preflight_firewall_reconciliation
    capture_service_state
    create_install_backup
    install_build_dependencies
    build_and_install_keepalived
    register_systemd_unit
    install_managed_configuration "$WORK_DIR/keepalived.conf"
    reconcile_firewall_rule
    configure_selinux_if_enabled
    activate_keepalived
    verify_installation
    TRANSACTION_ACTIVE=0
    print_next_steps
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
