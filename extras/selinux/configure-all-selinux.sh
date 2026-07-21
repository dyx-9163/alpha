#!/usr/bin/env bash
set -Eeuo pipefail

readonly MANAGED_BASE="/aifar/apps"
readonly TRANSACTION_BASE="/var/lib/aifar-selinux/transactions"
readonly SERVICE_ORDER="docker aifar-agent aifar-runtime mysql mysql-router redis minio nacos keepalived https-ingress"

ENTRY_MODE=""
TRANSACTION_DIR=""
JOURNAL_FILE=""
TRANSACTION_ACTIVE=0
MUTATION_COUNT=0
declare -A SERVICE_STATUS=()

log() {
    printf '[aifar-selinux] %s\n' "$*"
}

die() {
    printf '[aifar-selinux] ERROR: %s\n' "$*" >&2
    exit 1
}

set_status() {
    SERVICE_STATUS["$1"]="$2"
}

ensure_root() {
    if [[ "$(id -u)" == "0" ]]; then
        return 0
    fi
    command -v sudo >/dev/null 2>&1 || die "必须使用 root 用户执行"
    exec sudo -n bash "$0" "$@"
}

validate_platform() {
    [[ "$(uname -m)" == "x86_64" ]] || die "仅支持 x86_64"
    [[ -r /etc/os-release ]] || die "无法读取 /etc/os-release"

    local os_id os_version
    os_id="$(awk -F= '$1=="ID" {gsub(/^"|"$/, "", $2); print $2; exit}' /etc/os-release)"
    os_version="$(awk -F= '$1=="VERSION_ID" {gsub(/^"|"$/, "", $2); print $2; exit}' /etc/os-release)"
    [[ "$os_id" == "openEuler" ]] || die "仅支持 openEuler"
    [[ "$os_version" == 24.03* ]] || die "仅支持 openEuler 24.03"
}

ensure_selinux_tools() {
    local command_name missing=0
    for command_name in getenforce semanage matchpathcon restorecon; do
        if ! command -v "$command_name" >/dev/null 2>&1; then
            missing=1
        fi
    done

    if [[ "$missing" == "1" ]]; then
        command -v dnf >/dev/null 2>&1 || die "缺少 SELinux 管理命令，且 dnf 不可用"
        log "从当前 DNF 仓库安装 SELinux 管理工具"
        dnf -y install policycoreutils policycoreutils-python-utils selinux-policy-targeted
    fi

    for command_name in getenforce semanage matchpathcon restorecon; do
        command -v "$command_name" >/dev/null 2>&1 || die "缺少命令：$command_name"
    done
}

ensure_container_selinux_policy() {
    if rpm -q container-selinux >/dev/null 2>&1; then
        return 0
    fi
    command -v dnf >/dev/null 2>&1 || die "Docker SELinux 策略缺失，且 dnf 不可用"
    log "安装 Docker 所需的 container-selinux 策略包"
    dnf -y install container-selinux
    rpm -q container-selinux >/dev/null 2>&1 || die "container-selinux 安装后仍不可用"
}

validate_port() {
    local port="$1"
    [[ "$port" =~ ^[0-9]+$ ]] || return 1
    (( 10#$port >= 1 && 10#$port <= 65535 ))
}

canonical_managed_path() {
    local root="$1" candidate="$2" resolved
    [[ "$root" == /* && "$candidate" == /* ]] || return 1
    resolved="$(readlink -m -- "$candidate")" || return 1
    [[ "$resolved" == "$root" || "$resolved" == "$root"/* ]] || return 1
    printf '%s\n' "$resolved"
}

context_type() {
    local context="$1"
    awk -F: 'NF >= 3 && $3 ~ /_t$/ {print $3; exit}' <<<"$context"
}

reference_type() {
    local reference="$1" context type
    context="$(matchpathcon "$reference" 2>/dev/null | awk '{print $2; exit}')"
    type="$(context_type "$context")"
    [[ "$type" == *_t ]] || return 1
    printf '%s\n' "$type"
}

begin_transaction() {
    umask 077
    install -d -m 0700 "$TRANSACTION_BASE"
    TRANSACTION_DIR="$TRANSACTION_BASE/$(date -u +%Y%m%dT%H%M%SZ)-$$"
    install -d -m 0700 "$TRANSACTION_DIR"
    JOURNAL_FILE="$TRANSACTION_DIR/journal.tsv"
    : >"$JOURNAL_FILE"
    TRANSACTION_ACTIVE=1
}

journal() {
    printf '%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$4" >>"$JOURNAL_FILE"
}

local_port_type() {
    local port="$1"
    semanage port -l -C 2>/dev/null | awk -v want_port="$port" '
        $2 == "tcp" {
            for (i = 3; i <= NF; i++) {
                gsub(",", "", $i)
                n = split($i, port_range, "-")
                if ((n == 1 && port_range[1] == want_port) ||
                    (n == 2 && want_port >= port_range[1] && want_port <= port_range[2])) {
                    print $1
                    exit
                }
            }
        }
    '
}

effective_port_has_type() {
    local expected_type="$1" port="$2"
    semanage port -l 2>/dev/null | awk -v want_type="$expected_type" -v want_port="$port" '
        $1 == want_type && $2 == "tcp" {
            for (i = 3; i <= NF; i++) {
                gsub(",", "", $i)
                n = split($i, port_range, "-")
                if ((n == 1 && port_range[1] == want_port) ||
                    (n == 2 && want_port >= port_range[1] && want_port <= port_range[2])) {
                    found = 1
                }
            }
        }
        END { exit(found ? 0 : 1) }
    '
}

ensure_port_type() {
    local component="$1" expected_type="$2" port="$3" current_local
    validate_port "$port" || die "$component 端口无效：$port"
    current_local="$(local_port_type "$port" || true)"
    if [[ -n "$current_local" && "$current_local" != "$expected_type" ]]; then
        die "$component 端口 $port 已被本地类型 $current_local 占用"
    fi
    if effective_port_has_type "$expected_type" "$port"; then
        return 0
    fi

    if semanage port -a -t "$expected_type" -p tcp "$port"; then
        :
    elif semanage port -m -t "$expected_type" -p tcp "$port"; then
        # A distribution policy already owns the port under another type.
        # Because local conflicts were rejected above, -m creates only the
        # local override that can be removed cleanly during rollback.
        :
    else
        die "$component 无法为 TCP 端口 $port 设置类型 $expected_type"
    fi
    journal port-created "$port" "$expected_type" ""
    MUTATION_COUNT=$((MUTATION_COUNT + 1))
    effective_port_has_type "$expected_type" "$port" || die "$component 端口标签校验失败：$port"
    return 0
}

local_fcontext_type() {
    local pattern="$1" context
    context="$(semanage fcontext -l -C 2>/dev/null |
        FCONTEXT_PATTERN="$pattern" awk '$1 == ENVIRON["FCONTEXT_PATTERN"] {print $NF; exit}')"
    if [[ -n "$context" ]]; then
        context_type "$context"
    fi
}

ensure_fcontext() {
    local component="$1" pattern="$2" reference="$3" expected_type previous_type
    [[ "$pattern" == /* ]] || die "$component 文件标签模式不是绝对路径：$pattern"
    expected_type="$(reference_type "$reference")" || die "$component 无法解析发行版参考标签：$reference"
    previous_type="$(local_fcontext_type "$pattern" || true)"

    if [[ "$previous_type" == "$expected_type" ]]; then
        return 0
    fi
    if [[ -z "$previous_type" ]]; then
        semanage fcontext -a -t "$expected_type" "$pattern"
        journal fcontext-created "$pattern" "$expected_type" ""
    else
        semanage fcontext -m -t "$expected_type" "$pattern"
        journal fcontext-updated "$pattern" "$expected_type" "$previous_type"
    fi
    MUTATION_COUNT=$((MUTATION_COUNT + 1))
    return 0
}

safe_restorecon() {
    local component="$1" root="$2" target="$3" resolved
    resolved="$(canonical_managed_path "$root" "$target")" || die "$component 拒绝处理越界路径：$target"
    [[ -e "$resolved" ]] || return 0
    journal restore-target "$resolved" "" ""
    restorecon -RF -x "$resolved"
}

verify_file_type() {
    local component="$1" target="$2" reference="$3" expected_type actual_type
    [[ -e "$target" ]] || return 0
    expected_type="$(reference_type "$reference")" || die "$component 无法解析发行版参考标签：$reference"
    actual_type="$(stat -c %C "$target" 2>/dev/null | awk -F: 'NF >= 3 {print $3; exit}')"
    [[ "$actual_type" == "$expected_type" ]] || die "$component 标签不匹配：$target，期望 $expected_type，实际 ${actual_type:-unknown}"
}

rollback_transaction() {
    [[ "$TRANSACTION_ACTIVE" == "1" && -s "$JOURNAL_FILE" ]] || return 0
    log "回滚本次 SELinux 变更：$TRANSACTION_DIR"

    local action key applied previous
    while IFS=$'\t' read -r action key applied previous; do
        case "$action" in
            port-created)
                semanage port -d -p tcp "$key" >/dev/null 2>&1 || true
                ;;
            fcontext-created)
                semanage fcontext -d "$key" >/dev/null 2>&1 || true
                ;;
            fcontext-updated)
                semanage fcontext -m -t "$previous" "$key" >/dev/null 2>&1 || true
                ;;
        esac
    done < <(awk -F'\t' '$1 != "restore-target" {line[NR]=$0} END {for (i=NR;i>=1;i--) if (line[i] != "") print line[i]}' "$JOURNAL_FILE")

    while IFS=$'\t' read -r action key applied previous; do
        if [[ "$action" == "restore-target" && -e "$key" ]]; then
            restorecon -RF -x "$key" >/dev/null 2>&1 || true
        fi
    done <"$JOURNAL_FILE"
    TRANSACTION_ACTIVE=0
}

print_recent_avc() {
    command -v ausearch >/dev/null 2>&1 || return 0
    ausearch -m AVC,USER_AVC -ts recent 2>/dev/null | tail -n 80 || true
}

unit_fragment() {
    systemctl show -p FragmentPath --value "$1" 2>/dev/null || true
}

unit_exec_start() {
    systemctl show -p ExecStart --value "$1" 2>/dev/null || true
}

read_key_value() {
    local file="$1" key="$2"
    [[ -r "$file" ]] || return 1
    awk -F= -v want="$key" '$1 == want {print substr($0, index($0, "=") + 1); exit}' "$file"
}

read_conf_directive() {
    local file="$1" key="$2"
    [[ -r "$file" ]] || return 1
    awk -v want="$key" '$1 == want {print $2; exit}' "$file"
}

component_installed() {
    local root="$1" unit
    shift
    [[ -d "$root" ]] && return 0
    for unit in "$@"; do
        [[ -n "$(unit_fragment "$unit")" ]] && return 0
    done
    return 1
}

regex_path() {
    sed 's/[][(){}.^$*+?|\\]/\\&/g' <<<"$1"
}

apply_existing_path_mapping() {
    local component="$1" root="$2" target="$3" reference="$4" resolved pattern
    [[ -e "$target" ]] || return 0
    resolved="$(canonical_managed_path "$root" "$target")" || die "$component 拒绝处理越界路径：$target"
    pattern="$(regex_path "$resolved")"
    if [[ -d "$resolved" ]]; then
        pattern="${pattern}(/.*)?"
    fi
    ensure_fcontext "$component" "$pattern" "$reference"
    safe_restorecon "$component" "$root" "$resolved"
    verify_file_type "$component" "$resolved" "$reference"
}

discover_docker_port() {
    local exec_start port
    exec_start="$(unit_exec_start docker.service)"
    port="$(grep -oE 'tcp://[^ ,;}]+:[0-9]+' <<<"$exec_start" | sed 's/.*://' | tail -n 1 || true)"
    printf '%s\n' "${port:-2375}"
}

configure_docker() {
    local root="$MANAGED_BASE/docker" port
    component_installed "$root" docker.service containerd.service || return 10
    ensure_container_selinux_policy
    port="$(discover_docker_port)"
    ensure_port_type docker container_port_t "$port"
    apply_existing_path_mapping docker "$root" "$root/data" /var/lib/docker
    apply_existing_path_mapping docker "$root" "$root/exec" /run/docker
    apply_existing_path_mapping docker "$root" "$root/daemon" /etc/docker
}

configure_aifar_agent() {
    if [[ ! -x /usr/local/bin/aifar-agent && -z "$(unit_fragment aifar-agent.service)" ]]; then
        return 10
    fi
    local exec_start
    exec_start="$(unit_exec_start aifar-agent.service)"
    if [[ -n "$exec_start" && "$exec_start" != *"/usr/local/bin/aifar-agent"* ]]; then
        die "aifar-agent.service 不属于 AIFAR 安装"
    fi
    apply_existing_path_mapping aifar-agent /usr/local/bin/aifar-agent /usr/local/bin/aifar-agent /usr/bin/bash
    apply_existing_path_mapping aifar-agent /etc/aifar /etc/aifar /etc
    apply_existing_path_mapping aifar-agent /var/lib/aifar-agent /var/lib/aifar-agent /var/lib
    apply_existing_path_mapping aifar-agent /var/log/aifar-agent /var/log/aifar-agent /var/log
}

aifar_runtime_installed() {
    [[ -d "$MANAGED_BASE/admin/runtime" ]] && return 0
    command -v docker >/dev/null 2>&1 || return 1
    [[ -n "$(docker ps -aq --filter label=aifar.app=aifar 2>/dev/null || true)" ]]
}

discover_aifar_runtime_ports() {
    local spec="$MANAGED_BASE/admin/runtime/agent/runtime-spec.json" ports="" container
    ports="$({
        if [[ -r "$spec" ]]; then
            grep -oE '"(port|gatewayPort|webPort)"[[:space:]]*:[[:space:]]*[0-9]+' "$spec" |
                sed -E 's/.*:[[:space:]]*//' || true
        fi
        if command -v docker >/dev/null 2>&1; then
            while IFS= read -r container; do
                [[ -n "$container" ]] || continue
                docker inspect -f '{{range $bindings := .NetworkSettings.Ports}}{{range $bindings}}{{println .HostPort}}{{end}}{{end}}' \
                    "$container" 2>/dev/null || true
            done < <(docker ps -aq --filter label=aifar.app=aifar 2>/dev/null || true)
        fi
    } | awk '$1 ~ /^[0-9]+$/ && $1 >= 1 && $1 <= 65535 {print $1}' | sort -nu)"
    if [[ -n "$ports" ]]; then
        printf '%s\n' "$ports"
    else
        printf '%s\n' 8080 38000
    fi
}

container_reference_path() {
    local candidate type
    for candidate in \
        /var/lib/containers/storage/volumes/aifar/_data \
        /var/lib/docker/volumes/aifar/_data; do
        type="$(reference_type "$candidate" 2>/dev/null || true)"
        if [[ "$type" == "container_file_t" ]]; then
            printf '%s\n' "$candidate"
            return 0
        fi
    done
    return 1
}

configure_aifar_runtime_bind_mounts() {
    local root="$MANAGED_BASE/admin" reference spec source container
    reference="$(container_reference_path)" || die "无法解析 container_file_t 参考标签"
    spec="$root/runtime/agent/runtime-spec.json"

    if [[ -r "$spec" ]]; then
        while IFS= read -r source; do
            [[ -n "$source" ]] || continue
            source="$(canonical_managed_path "$root" "$source")" || die "AIFAR Runtime bind mount 越界：$source"
            apply_existing_path_mapping aifar-runtime "$root" "$source" "$reference"
        done < <(grep -oE '"source"[[:space:]]*:[[:space:]]*"[^"]+"' "$spec" |
            sed -E 's/^[^:]+:[[:space:]]*"([^"]+)"$/\1/' | sort -u || true)
    fi

    if command -v docker >/dev/null 2>&1; then
        while IFS= read -r container; do
            [[ -n "$container" ]] || continue
            while IFS= read -r source; do
                [[ -n "$source" ]] || continue
                source="$(canonical_managed_path "$root" "$source")" || die "AIFAR Runtime 容器挂载越界：$source"
                apply_existing_path_mapping aifar-runtime "$root" "$source" "$reference"
            done < <(docker inspect -f '{{range .Mounts}}{{if eq .Type "bind"}}{{println .Source}}{{end}}{{end}}' "$container" 2>/dev/null || true)
        done < <(docker ps -aq --filter label=aifar.app=aifar 2>/dev/null || true)
    fi
}

configure_aifar_runtime() {
    local port
    aifar_runtime_installed || return 10
    while IFS= read -r port; do
        [[ -n "$port" ]] || continue
        ensure_port_type aifar-runtime http_port_t "$port"
    done < <(discover_aifar_runtime_ports)
    configure_aifar_runtime_bind_mounts
}

configure_mysql() {
    local root="$MANAGED_BASE/mysql" port
    component_installed "$root" aifar-mysql.service || return 10
    port="$(read_key_value "$root/conf/my.cnf" port 2>/dev/null || true)"
    port="${port:-3306}"
    ensure_port_type mysql mysqld_port_t "$port"
    apply_existing_path_mapping mysql "$root" "$root/mysql/bin/mysqld" /usr/sbin/mysqld
    apply_existing_path_mapping mysql "$root" "$root/mysql/lib" /usr/lib64
    apply_existing_path_mapping mysql "$root" "$root/conf" /etc/mysql
    apply_existing_path_mapping mysql "$root" "$root/data" /var/lib/mysql
    apply_existing_path_mapping mysql "$root" "$root/logs" /var/log/mysqld.log
    apply_existing_path_mapping mysql "$root" "$root/run" /run/mysqld
}

discover_mysql_router_base_port() {
    local root="$MANAGED_BASE/mysql-router" config port
    for config in "$root/router/mysqlrouter.conf" "$root/mysql-router/mysqlrouter.conf"; do
        [[ -r "$config" ]] || continue
        port="$(awk -F= '$1 ~ /^[[:space:]]*bind_port[[:space:]]*$/ {gsub(/[[:space:]]/, "", $2); print $2}' "$config" |
            sort -n | head -n 1)"
        [[ -n "$port" ]] && { printf '%s\n' "$port"; return 0; }
    done
    printf '%s\n' 6446
}

configure_mysql_router() {
    local root="$MANAGED_BASE/mysql-router" base_port offset
    component_installed "$root" aifar-mysql-router.service || return 10
    base_port="$(discover_mysql_router_base_port)"
    validate_port "$base_port" || die "MySQL Router 基础端口无效：$base_port"
    for offset in 0 1 2 3; do
        ensure_port_type mysql-router mysqld_port_t "$((10#$base_port + offset))"
    done
    apply_existing_path_mapping mysql-router "$root" "$root/mysql-router/bin/mysqlrouter" /usr/bin/mysqlrouter
    apply_existing_path_mapping mysql-router "$root" "$root/router" /etc/mysqlrouter
}

discover_redis_ports() {
    local root="$MANAGED_BASE/redis" port sentinel cluster bus
    port="$(read_conf_directive "$root/conf/redis.conf" port 2>/dev/null || true)"
    port="${port:-6379}"
    printf '%s\n' "$port"
    sentinel="$(read_conf_directive "$root/conf/sentinel.conf" port 2>/dev/null || true)"
    [[ -z "$sentinel" ]] || printf '%s\n' "$sentinel"
    cluster="$(read_conf_directive "$root/conf/redis.conf" cluster-enabled 2>/dev/null || true)"
    if [[ "$cluster" == "yes" ]]; then
        bus=$((10#$port + 10000))
        printf '%s\n' "$bus"
    fi
}

configure_redis() {
    local root="$MANAGED_BASE/redis" port
    component_installed "$root" aifar-redis.service aifar-redis-sentinel.service || return 10
    while IFS= read -r port; do
        [[ -n "$port" ]] || continue
        ensure_port_type redis redis_port_t "$port"
    done < <(discover_redis_ports)
    apply_existing_path_mapping redis "$root" "$root/bin" /usr/bin/redis-server
    apply_existing_path_mapping redis "$root" "$root/conf" /etc/redis
    apply_existing_path_mapping redis "$root" "$root/data" /var/lib/redis
    apply_existing_path_mapping redis "$root" "$root/logs" /var/log/redis
    apply_existing_path_mapping redis "$root" "$root/run" /run/redis
}

discover_minio_ports() {
    local root="$MANAGED_BASE/minio" env_file="$MANAGED_BASE/minio/conf/minio.env" opts api console
    opts="$(read_key_value "$env_file" MINIO_OPTS 2>/dev/null || true)"
    api="$(grep -oE -- '--address[= ]+[^ ]*:[0-9]+' <<<"$opts" | sed 's/.*://' | tail -n 1 || true)"
    console="$(grep -oE -- '--console-address[= ]+[^ ]*:[0-9]+' <<<"$opts" | sed 's/.*://' | tail -n 1 || true)"
    printf '%s\n' "${api:-9000}" "${console:-9001}"
}

validate_minio_data_path() {
    local candidate="$1" resolved
    [[ "$candidate" == /* ]] || return 1
    resolved="$(readlink -m -- "$candidate")" || return 1
    case "$resolved" in
        "$MANAGED_BASE/minio"/* | /data/* | /mnt/* | /srv/*) ;;
        *) return 1 ;;
    esac
    [[ "$resolved" != /data && "$resolved" != /mnt && "$resolved" != /srv ]] || return 1
    printf '%s\n' "$resolved"
}

discover_minio_local_volumes() {
    local env_file="$MANAGED_BASE/minio/conf/minio.env" volumes token resolved
    volumes="$(read_key_value "$env_file" MINIO_VOLUMES 2>/dev/null || true)"
    volumes="${volumes#\"}"
    volumes="${volumes%\"}"
    [[ -n "$volumes" ]] || return 0
    while IFS= read -r token; do
        [[ -n "$token" ]] || continue
        case "$token" in
            http://* | https://*) continue ;;
            /*)
                resolved="$(validate_minio_data_path "$token")" ||
                    die "MinIO 数据目录不在允许的存储根路径内：$token"
                printf '%s\n' "$resolved"
                ;;
            *) die "MinIO 数据卷格式无效：$token" ;;
        esac
    done < <(awk '{for (i=1; i<=NF; i++) print $i}' <<<"$volumes")
}

configure_minio() {
    local root="$MANAGED_BASE/minio" port volume
    component_installed "$root" aifar-minio.service || return 10
    while IFS= read -r port; do
        [[ -n "$port" ]] || continue
        ensure_port_type minio http_port_t "$port"
    done < <(discover_minio_ports)
    apply_existing_path_mapping minio "$root" "$root/bin" /usr/local/bin
    apply_existing_path_mapping minio "$root" "$root/conf" /etc
    apply_existing_path_mapping minio "$root" "$root/data" /var/lib
    apply_existing_path_mapping minio "$root" "$root/logs" /var/log
    apply_existing_path_mapping minio "$root" "$root/run" /run
    while IFS= read -r volume; do
        [[ -n "$volume" ]] || continue
        [[ -e "$volume" ]] || die "MinIO 数据目录不存在：$volume"
        apply_existing_path_mapping minio "$volume" "$volume" /var/lib
    done < <(discover_minio_local_volumes)
}

discover_nacos_ports() {
    local root="$MANAGED_BASE/nacos" properties="$MANAGED_BASE/nacos/nacos/conf/application.properties" port
    port="$(read_key_value "$properties" server.port 2>/dev/null || true)"
    port="${port:-8848}"
    printf '%s\n' "$port" "$((10#$port + 1000))" "$((10#$port + 1001))" 7848
}

port_is_listening() {
    local port="$1"
    ss -lnt 2>/dev/null | awk '{print $4}' | grep -Eq "[:.]${port}$"
}

verify_nacos_ports() {
    local port
    command -v systemctl >/dev/null 2>&1 || return 0
    systemctl is-active --quiet aifar-nacos.service 2>/dev/null || return 0
    command -v ss >/dev/null 2>&1 || die "Nacos 运行中但缺少 ss，无法验证监听端口"
    while IFS= read -r port; do
        validate_port "$port" || die "Nacos 端口无效：$port"
        port_is_listening "$port" || die "Nacos 端口未监听：$port"
    done < <(discover_nacos_ports)
}

configure_nacos() {
    local root="$MANAGED_BASE/nacos"
    component_installed "$root" aifar-nacos.service || return 10
    verify_nacos_ports
    apply_existing_path_mapping nacos "$root" "$root/jdk" /usr/lib/jvm
    apply_existing_path_mapping nacos "$root" "$root/nacos/bin" /usr/bin
    apply_existing_path_mapping nacos "$root" "$root/nacos/conf" /etc
    apply_existing_path_mapping nacos "$root" "$root/nacos/data" /var/lib
    apply_existing_path_mapping nacos "$root" "$root/nacos/logs" /var/log
}

configure_keepalived() {
    local root="$MANAGED_BASE/keepalived" exec_start
    if [[ ! -x "$root/sbin/keepalived" && -z "$(unit_fragment keepalived.service)" ]]; then
        return 10
    fi
    exec_start="$(unit_exec_start keepalived.service)"
    if [[ -n "$exec_start" && "$exec_start" != *"$root/sbin/keepalived"* ]]; then
        die "keepalived.service 不属于 $root"
    fi
    apply_existing_path_mapping keepalived "$root" "$root/sbin/keepalived" /usr/sbin/keepalived
    apply_existing_path_mapping keepalived "$root" "$root/etc" /etc/keepalived
    apply_existing_path_mapping keepalived "$root" "$root/scripts" /usr/libexec/keepalived
    apply_existing_path_mapping keepalived "$root" "$root/var/lib" /var/lib/keepalived
    apply_existing_path_mapping keepalived "$root" "$root/var/run" /run/keepalived
}

discover_https_ingress_root() {
    local exec_start start_script root
    exec_start="$(unit_exec_start aifar-https-ingress.service)"
    start_script="$(grep -oE 'path=/[^ ;}]+' <<<"$exec_start" | sed 's/^path=//' | head -n 1 || true)"
    [[ -n "$start_script" ]] || die "无法从 aifar-https-ingress.service 解析启动脚本"
    root="$(readlink -m -- "$(dirname -- "$start_script")")" ||
        die "无法解析 HTTPS ingress 安装目录"
    if [[ "$root" != "$MANAGED_BASE/aifar-https-ingress" &&
          "$root" != "$TRANSACTION_DIR/extras/aifar-https-ingress" ]]; then
        die "aifar-https-ingress.service 启动脚本不属于受支持的 ingress 模块：$start_script"
    fi
    printf '%s\n' "$root"
}

ingress_container_name() {
    local root="$1" configured
    configured="$(read_key_value "$root/config.env" AIFAR_HTTPS_CONTAINER_NAME 2>/dev/null || true)"
    printf '%s\n' "${configured:-aifar-https-ingress}"
}

ingress_container_running() {
    local root="$1" container_name
    command -v docker >/dev/null 2>&1 || return 1
    container_name="$(ingress_container_name "$root")"
    [[ "$(docker inspect -f '{{.State.Running}}' "$container_name" 2>/dev/null || true)" == "true" ]]
}

verify_container_mount_type() {
    local component="$1" target="$2" actual_type
    [[ -d "$target" ]] || die "$component 挂载目录不存在：$target"
    actual_type="$(stat -c %C "$target" 2>/dev/null | awk -F: 'NF >= 3 {print $3; exit}')"
    [[ "$actual_type" == "container_file_t" ]] ||
        die "$component 挂载目录标签不正确：$target，期望 container_file_t，实际 ${actual_type:-unknown}"
}

container_file_reference() {
    local candidate type
    for candidate in \
        /var/lib/containers/storage/volumes/aifar/_data \
        /var/lib/docker/volumes/aifar/_data; do
        type="$(reference_type "$candidate" 2>/dev/null || true)"
        if [[ "$type" == "container_file_t" ]]; then
            printf '%s\n' "$candidate"
            return 0
        fi
    done
    return 1
}

# preserve live private MCS labels assigned by Docker/Podman for :Z mounts.
# Applying restorecon to these paths while the container runs would erase the
# categories and can immediately break ingress access.
configure_https_ingress() {
    local fragment root reference
    fragment="$(unit_fragment aifar-https-ingress.service)"
    [[ -n "$fragment" ]] || return 10
    root="$(discover_https_ingress_root)"
    [[ -d "$root" ]] || die "HTTPS ingress 安装目录不存在：$root"

    ensure_port_type https-ingress http_port_t 80
    ensure_port_type https-ingress http_port_t 443

    if ingress_container_running "$root"; then
        verify_container_mount_type https-ingress "$root/conf.d"
        verify_container_mount_type https-ingress "$root/tls"
    else
        reference="$(container_file_reference)" ||
            die "无法从发行版策略解析 container_file_t 参考路径"
        apply_existing_path_mapping https-ingress "$root" "$root/conf.d" "$reference"
        apply_existing_path_mapping https-ingress "$root" "$root/tls" "$reference"
    fi

    apply_existing_path_mapping https-ingress "$root" "$root/start.sh" /usr/bin/bash
    apply_existing_path_mapping https-ingress "$root" "$root/config.env" /etc/sysconfig
}

run_component() {
    local name="$1" function_name="$2" before rc
    before="$MUTATION_COUNT"
    if "$function_name"; then
        if (( MUTATION_COUNT > before )); then
            set_status "$name" APPLIED
        else
            set_status "$name" UNCHANGED
        fi
        return 0
    else
        rc=$?
    fi
    if [[ "$rc" == "10" ]]; then
        set_status "$name" SKIPPED
        return 0
    fi
    set_status "$name" FAILED
    print_recent_avc
    return "$rc"
}

print_summary() {
    local service
    for service in $SERVICE_ORDER; do
        printf '%-20s %s\n' "$service" "${SERVICE_STATUS[$service]:-SKIPPED}"
    done
    printf '%-20s %s (unchanged)\n' "SELinux mode" "$ENTRY_MODE"
    printf '%-20s SUCCESS\n' "Result"
}

on_exit() {
    local rc=$?
    if [[ "$rc" != "0" && "$TRANSACTION_ACTIVE" == "1" ]]; then
        rollback_transaction || true
    fi
    if [[ -n "$ENTRY_MODE" ]]; then
        local exit_mode
        exit_mode="$(getenforce 2>/dev/null || true)"
        if [[ -n "$exit_mode" && "$exit_mode" != "$ENTRY_MODE" ]]; then
            printf '[aifar-selinux] ERROR: SELinux 模式发生变化：%s -> %s\n' "$ENTRY_MODE" "$exit_mode" >&2
            rc=1
        fi
    fi
    exit "$rc"
}

main() {
    [[ $# -eq 0 ]] || die "该脚本不接受参数"
    ensure_root "$@"
    validate_platform
    ensure_selinux_tools

    ENTRY_MODE="$(getenforce)"
    [[ "$ENTRY_MODE" != "Disabled" ]] || die "SELinux 当前为 Disabled；脚本不会修改系统模式"
    begin_transaction
    trap on_exit EXIT
    trap 'exit 130' INT TERM

    run_component docker configure_docker
    run_component aifar-agent configure_aifar_agent
    run_component aifar-runtime configure_aifar_runtime
    run_component mysql configure_mysql
    run_component mysql-router configure_mysql_router
    run_component redis configure_redis
    run_component minio configure_minio
    run_component nacos configure_nacos
    run_component keepalived configure_keepalived
    run_component https-ingress configure_https_ingress

    TRANSACTION_ACTIVE=0
    print_summary
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
