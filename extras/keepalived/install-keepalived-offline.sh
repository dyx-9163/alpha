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
WORK_DIR=""

log() {
    printf '[keepalived-installer] %s\n' "$*"
}

die() {
    printf '[keepalived-installer] ERROR: %s\n' "$*" >&2
    exit 1
}

cleanup() {
    local status=$?
    trap - EXIT

    if [[ -n "$WORK_DIR" && "$WORK_DIR" == /tmp/keepalived-offline.* && -d "$WORK_DIR" ]]; then
        rm -rf -- "$WORK_DIR"
    fi

    exit "$status"
}

trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

require_command() {
    command -v "$1" >/dev/null 2>&1 || die "缺少必要命令：$1"
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
    )

    log "使用服务器当前启用的 DNF 仓库安装编译依赖"
    dnf --assumeyes --setopt=install_weak_deps=False --setopt=keepcache=False install "${packages[@]}"

    local command_name
    for command_name in gcc make autoconf automake autoreconf aclocal pkg-config; do
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
    local unit_source="$APP_ROOT/systemd/keepalived.service"
    local unit_link="/etc/systemd/system/keepalived.service"
    local existing_fragment=""
    local resolved_fragment=""

    [[ -f "$unit_source" ]] || die "未生成 systemd 单元：$unit_source"

    if [[ -e "$unit_link" || -L "$unit_link" ]]; then
        resolved_fragment="$(readlink -f -- "$unit_link" 2>/dev/null || true)"
        if [[ "$resolved_fragment" != "$unit_source" ]]; then
            die "系统已存在其他 Keepalived unit：$unit_link；为避免覆盖，本次安装已停止"
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

configure_selinux_if_enabled() {
    if command -v getenforce >/dev/null 2>&1 && [[ "$(getenforce)" != "Disabled" ]]; then
        [[ -f "$SELINUX_SCRIPT" ]] || die "SELinux 已启用，但缺少脚本：$SELINUX_SCRIPT"
        bash "$SELINUX_SCRIPT"
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

脚本没有自动复制示例配置，也没有启动服务，避免误绑定示例 VIP。
完成主备配置后执行：

  $APP_ROOT/sbin/keepalived -t -f $APP_ROOT/etc/keepalived/keepalived.conf
  systemctl enable --now keepalived
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

    for command_name in readlink grep tar dnf systemctl ldd sha256sum stat; do
        require_command "$command_name"
    done

    [[ -f "$SOURCE_ARCHIVE" ]] || die "请将 $SOURCE_ARCHIVE_NAME 放到脚本同一目录：$SCRIPT_DIR"
    WORK_DIR="$(mktemp -d /tmp/keepalived-offline.XXXXXX)"
    verify_source_archive
    install_build_dependencies
    build_and_install_keepalived
    register_systemd_unit
    configure_selinux_if_enabled
    verify_installation
    print_next_steps
}

main "$@"
