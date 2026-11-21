#!/bin/sh
# BEGIN KOMARI RELEASE DOWNLOAD
# Embedded verbatim into the official-derived installers by render-installers.py.
# No secret is sent to GitHub; Client Tokens are only passed to the agent.
komari_release_repository=${KOMARI_RELEASE_REPOSITORY:-0ozzzii/komari-mcp-bridge}
printf '%s\n' "$komari_release_repository" | LC_ALL=C grep -Eq '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$' || {
    echo 'Invalid KOMARI_RELEASE_REPOSITORY; expected owner/repository' >&2
    exit 1
}

komari_resolve_release() {
    komari_requested=$1
    if [ -z "$komari_requested" ] || [ "$komari_requested" = latest ]; then
        komari_api="https://api.github.com/repos/${komari_release_repository}/releases/latest"
        komari_tag=$(curl -fsSL --connect-timeout 15 --max-time 60 "$komari_api" |
            sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1) || return 1
    elif [ "$komari_requested" = snapshot ]; then
        komari_api="https://api.github.com/repos/${komari_release_repository}/releases?per_page=100"
        komari_tag=$(curl -fsSL --connect-timeout 15 --max-time 60 "$komari_api" |
            grep -o '"tag_name":[[:space:]]*"Snapshot-[A-Za-z0-9._-]*"' |
            sed 's/.*"\(Snapshot-[^"]*\)".*/\1/' | LC_ALL=C sort -r | head -n 1) || return 1
    else
        komari_tag=$komari_requested
    fi
    # Restrict path and metadata characters even when the release API is used.
    printf '%s\n' "$komari_tag" | LC_ALL=C grep -Eq '^(v[0-9]+\.[0-9]+\.[0-9]+|Snapshot-[A-Za-z0-9._-]+)$' || return 1
    printf '%s\n' "$komari_tag"
}

komari_fetch_verified() (
    set -eu
    komari_url=$1
    komari_dest=$2
    komari_name=${komari_url##*/}
    komari_dir=$(mktemp -d "${komari_dest}.download.XXXXXX") || exit 1
    trap 'rm -rf -- "$komari_dir"' EXIT
    trap 'exit 1' HUP INT TERM
    curl -fsSL --retry 2 --connect-timeout 15 --max-time 300 "$komari_url" -o "$komari_dir/binary" || exit 1
    test -s "$komari_dir/binary" || exit 1
    curl -fsSL --retry 2 --connect-timeout 15 --max-time 60 "${komari_url%/*}/sha256sums.txt" -o "$komari_dir/manifest" || exit 1
    komari_expected=$(awk -v n="$komari_name" '$2 == n || $2 == "*" n {print $1; count++} END {if(count!=1) exit 1}' "$komari_dir/manifest") || exit 1
    printf '%s\n' "$komari_expected" | LC_ALL=C grep -Eq '^[a-fA-F0-9]{64}$' || exit 1
    if command -v sha256sum >/dev/null 2>&1; then
        komari_actual=$(sha256sum "$komari_dir/binary" | awk '{print $1}')
    elif command -v shasum >/dev/null 2>&1; then
        komari_actual=$(shasum -a 256 "$komari_dir/binary" | awk '{print $1}')
    elif command -v openssl >/dev/null 2>&1; then
        komari_actual=$(openssl dgst -sha256 "$komari_dir/binary" | awk '{print $NF}')
    else
        echo 'SHA256 verification requires sha256sum, shasum or openssl' >&2; exit 1
    fi
    test "$(printf '%s' "$komari_expected" | tr A-F a-f)" = "$komari_actual" || { echo 'Release checksum mismatch; existing installation preserved' >&2; exit 1; }
    chmod 755 "$komari_dir/binary" || exit 1
    mv -f "$komari_dir/binary" "$komari_dest"
)
# END KOMARI RELEASE DOWNLOAD

# Explicit installation bases must remain HTTPS even across redirects. Keep
# this separate from the shared release helper so panel installers retain the
# byte-for-byte canonical download block.
komari_fetch_base_verified() (
    set -eu
    komari_url=$1
    komari_dest=$2
    case "$komari_url" in https://*) ;; *) echo 'Custom release base must use HTTPS' >&2; exit 1 ;; esac
    komari_name=${komari_url##*/}
    komari_dir=$(mktemp -d "${komari_dest}.download.XXXXXX") || exit 1
    trap 'rm -rf -- "$komari_dir"' EXIT HUP INT TERM
    curl -fsSL --proto '=https' --proto-redir '=https' --retry 2 --connect-timeout 15 --max-time 300 "$komari_url" -o "$komari_dir/binary" || exit 1
    test -s "$komari_dir/binary" || exit 1
    curl -fsSL --proto '=https' --proto-redir '=https' --retry 2 --connect-timeout 15 --max-time 60 "${komari_url%/*}/sha256sums.txt" -o "$komari_dir/manifest" || exit 1
    komari_expected=$(awk -v n="$komari_name" '$2 == n || $2 == "*" n {print $1; count++} END {if(count!=1) exit 1}' "$komari_dir/manifest") || exit 1
    printf '%s\n' "$komari_expected" | LC_ALL=C grep -Eq '^[a-fA-F0-9]{64}$' || exit 1
    if command -v sha256sum >/dev/null 2>&1; then
        komari_actual=$(sha256sum "$komari_dir/binary" | awk '{print $1}') || exit 1
    elif command -v shasum >/dev/null 2>&1; then
        komari_actual=$(shasum -a 256 "$komari_dir/binary" | awk '{print $1}') || exit 1
    elif command -v openssl >/dev/null 2>&1; then
        komari_actual=$(openssl dgst -sha256 "$komari_dir/binary" | awk '{print $NF}') || exit 1
    else
        echo 'SHA256 verification requires sha256sum, shasum or openssl' >&2; exit 1
    fi
    test "$(printf '%s' "$komari_expected" | tr A-F a-f)" = "$komari_actual" || { echo 'Release checksum mismatch; existing installation preserved' >&2; exit 1; }
    chmod 755 "$komari_dir/binary" || exit 1
    mv -f "$komari_dir/binary" "$komari_dest" || exit 1
)

# BEGIN KOMARI AGENT INSTALL STATUS
# Read only this systemd invocation; never print raw logs or startup arguments.
komari_status_systemctl() {
    if [ "${user_service:-false}" = true ]; then systemctl --user "$@"; else systemctl "$@"; fi
}

komari_status_journal() {
    if [ "${user_service:-false}" = true ]; then
        journalctl --user --quiet --no-pager --output=cat -n 200 "_SYSTEMD_INVOCATION_ID=$1"
    else
        journalctl --quiet --no-pager --output=cat -n 200 "_SYSTEMD_INVOCATION_ID=$1"
    fi
}

komari_install_error_reason() {
    # Only fixed diagnostic text leaves this function: logs can contain Tokens.
    case "$1" in
        *'Non-compliance ICP Filing'*) printf '%s' '入口返回备案拦截；核对既定 HTTPS 域名和反向代理配置' ;;
        *'status code: 401'*|*'status code: 403'*|*'401 Unauthorized'*|*'403 Forbidden'*) printf '%s' '认证或访问策略拒绝（401/403）；检查节点 Token 与入口策略' ;;
        *'x509:'*|*'TLS handshake'*|*'tls: '*) printf '%s' 'TLS／证书验证失败；检查证书链和设备时间' ;;
        *'no such host'*|*'server misbehaving'*|*'lookup '*|*'Temporary failure in name resolution'*) printf '%s' 'DNS 解析失败；检查设备解析器与域名' ;;
        *'connection refused'*) printf '%s' '连接被拒绝；检查服务监听与反向代理目标' ;;
        *'timeout'*|*'deadline exceeded'*) printf '%s' '请求超时；检查网络与入口可达性' ;;
        *'Error uploading basic info:'*|*'Failed to connect to WebSocket:'*) printf '%s' '探针报告接入错误；请在本机查看日志（分享前隐藏凭据）' ;;
    esac
}

komari_install_ws_summary() {
    case "${komari_status_ws:-}" in
        connected) printf '%s\n' '监控长连接：已建立（本次服务日志确认）。' ;;
        disconnected) printf '%s\n' '监控长连接：已断开（本次服务日志确认，探针将按原有机制重连）。' ;;
        *) printf '%s\n' '监控长连接：尚未确认。' ;;
    esac
}

komari_install_status() {
    printf '\n%s\n' '安装结果：文件已安装，服务启动请求已提交。'
    printf '版本：%s\n位置：%s\n运行用户：%s\n' "$version_to_install" "$komari_agent_path" "$service_user"
    printf '%s\n' '终端／MCP：未测试；上报成功不代表终端已就绪。'
    if [ "${init_system:-}" != systemd ] || ! command -v journalctl >/dev/null 2>&1; then
        printf '%s\n' '[待确认] 当前服务管理器或日志工具不支持自动接入检查，请查看节点在线状态。'
        return 0
    fi
    printf '正在等待本次启动的上报结果（最多 %s 秒）…\n' "$install_check_timeout"
    komari_status_elapsed=0
    komari_status_started=$(date +%s)
    komari_status_result=0
    komari_status_reason=''
    komari_status_ws=''
    komari_status_report=''
    komari_status_last_invocation=''
    while :; do
        komari_status_invocation=$(komari_status_systemctl show "${service_name}.service" --property=InvocationID --value 2>/dev/null) || komari_status_invocation=''
        if [ "$komari_status_invocation" != "$komari_status_last_invocation" ]; then
            komari_status_reason=''
            komari_status_ws=''
            komari_status_report=''
            komari_status_last_invocation=$komari_status_invocation
        fi
        if printf '%s\n' "$komari_status_invocation" | LC_ALL=C grep -Eq '^[a-fA-F0-9]{32}$'; then
            if ! komari_status_log=$(komari_status_journal "$komari_status_invocation" 2>/dev/null); then
                printf '%s\n' '[待确认] 无法读取本次服务日志，未验证面板接入；服务配置已保留。'
                break
            fi
            # Recheck identity and liveness after reading: a restarted/dead
            # process must not inherit a previous invocation's successful log.
            komari_status_current=$(komari_status_systemctl show "${service_name}.service" --property=InvocationID --value 2>/dev/null) || komari_status_current=''
            if [ "$komari_status_current" = "$komari_status_invocation" ] && komari_status_systemctl is-active --quiet "${service_name}.service"; then
                komari_status_ws=$(printf '%s\n' "$komari_status_log" | LC_ALL=C awk '/^[0-9][0-9][0-9][0-9]\/.* WebSocket connected using v2 protocol$|^[0-9][0-9][0-9][0-9]\/.* WebSocket recovered from POST fallback$/{state="connected"} /^[0-9][0-9][0-9][0-9]\/.* WebSocket disconnected$|^[0-9][0-9][0-9][0-9]\/.* WebSocket read error:/{state="disconnected"} END{print state}')
                if printf '%s\n' "$komari_status_log" | LC_ALL=C grep -Eq '^[0-9]{4}/[0-9]{2}/[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2} Basic info uploaded successfully$'; then
                    komari_status_report=confirmed
                fi
                if [ "$komari_status_report" = confirmed ] && [ "$komari_status_ws" = connected ]; then
                    printf '%s\n' '[成功] 服务正在运行；面板接入已确认（本次启动的基础信息上报成功）。'
                    komari_install_ws_summary
                    return 0
                fi
                komari_status_reason=$(komari_install_error_reason "$komari_status_log")
            else
                komari_status_ws=''
                komari_status_report=''
            fi
        fi
        komari_status_elapsed=$(($(date +%s) - komari_status_started))
        if [ "$komari_status_elapsed" -ge "$install_check_timeout" ]; then
            if komari_status_systemctl is-active --quiet "${service_name}.service"; then
                if [ "$komari_status_report" = confirmed ]; then
                    printf '%s\n' '[成功] 服务正在运行；面板接入已确认（本次启动的基础信息上报成功）。'
                elif [ -n "$komari_status_reason" ]; then
                    printf '[连接异常] %s。安装已完成，服务保留运行并按探针原有机制重试。\n' "$komari_status_reason"
                else
                    printf '%s\n' '[待确认] 服务正在运行，但等待期间没有确认成功上报；这不等于安装失败。'
                fi
            else
                printf '%s\n' '[失败] 服务未处于运行状态；文件已安装，请查看本机服务日志。'
                komari_status_result=1
                komari_status_ws=''
            fi
            break
        fi
        sleep 1
    done
    komari_install_ws_summary
    if [ "${user_service:-false}" = true ]; then
        printf '查看日志：journalctl --user -u "%s.service" -n 50 --no-pager\n' "$service_name"
    else
        printf '查看日志：sudo journalctl -u "%s.service" -n 50 --no-pager\n' "$service_name"
    fi
    printf '%s\n' '分享日志前请隐藏 Token、接入 URL 和其他凭据。'
    return "$komari_status_result"
}
# END KOMARI AGENT INSTALL STATUS

# Color definitions for terminal output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
CYAN='\033[0;36m'
WHITE='\033[1;37m'
NC='\033[0m' # No Color

# Logging functions
log_info() {
    echo -e "${NC} $1"
}

log_success() {
    echo -e "${GREEN}${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

log_step() {
    echo -e "${NC} $1"
}

log_config() {
    echo -e "${CYAN}[CONFIG]${NC} $1"
}

# $installer_uid 是 bash 专有变量, ash/dash 下未定义, 补 POSIX 回退
installer_uid=$(id -u)

# Default values
service_name="komari-agent"
target_dir="/opt/komari"
github_proxy=""
install_version="" # New parameter for specifying version
install_base_url=""
install_dir_specified=false
install_no_mirror=false # 关闭自动加速镜像
service_user="${SUDO_USER:-$(id -un)}"
user_service=false
install_check_timeout=30
install_no_systemd=false
keep_custom=false
replace_custom=false
auto_update_disabled=false
case "${AGENT_DISABLE_AUTO_UPDATE:-}" in
    1|[Tt][Rr][Uu][Ee]) auto_update_disabled=true ;;
esac

# Detect OS
os_type=$(uname -s)
case $os_type in
    Darwin)
        os_name="darwin"
        target_dir="/usr/local/komari"  # Use /usr/local on macOS
        # Check if we can write to /usr/local, fallback to user directory
        if [ ! -w "/usr/local" ] && [ "$installer_uid" -ne 0 ]; then
            target_dir="$HOME/.komari"
            log_info "No write permission to /usr/local, using user directory: $target_dir"
        fi
        ;;
    Linux)
        os_name="linux"
        ;;
    FreeBSD)
        os_name="freebsd"
        ;;
    MINGW*|MSYS*|CYGWIN*)
        os_name="windows"
        target_dir="/c/komari"  # Use C:\komari on Windows
        ;;
    *)
        log_error "Unsupported operating system: $os_type"
        exit 1
        ;;
esac

# Store a shell-quoted copy for the standalone launcher. Never eval user input.
quote_for_launcher() {
    local escaped
    escaped=$(printf '%s.' "$1" | sed "s/'/'\\\\''/g")
    printf "'%s'" "${escaped%.}"
}

# Parse install-specific arguments
komari_args=""
komari_manual_args=""
# [[ ]] -> [ ] (POSIX)
while [ $# -gt 0 ]; do
    case $1 in
        --install-dir)
            target_dir="$2"
            install_dir_specified=true
            shift 2
            ;;
        --install-service-name)
            service_name="$2"
            shift 2
            ;;
        --install-ghproxy)
            github_proxy="$2"
            shift 2
            ;;
        --install-version)
            install_version="$2"
            shift 2
            ;;
        --install-base-url)
            if [ "$#" -lt 2 ]; then
                log_error "--install-base-url requires an HTTPS base URL."
                exit 1
            fi
            if [ -n "$install_base_url" ] && [ "$install_base_url" != "$2" ]; then
                log_error "Conflicting --install-base-url values."
                exit 1
            fi
            install_base_url="$2"
            shift 2
            ;;
        --install-no-systemd)
            install_no_systemd=true
            shift
            ;;
        --keep-custom)
            keep_custom=true
            shift
            ;;
        --replace-custom)
            replace_custom=true
            shift
            ;;
        --install-no-mirror) # 新增: 关闭自动加速镜像
            install_no_mirror=true
            shift
            ;;
        --install-check-timeout)
            install_check_timeout="${2:-}"
            if ! printf '%s\n' "$install_check_timeout" | LC_ALL=C grep -Eq '^[0-9]{1,3}$' || [ "$install_check_timeout" -gt 120 ]; then
                log_error "--install-check-timeout must be an integer from 0 to 120 seconds"
                exit 1
            fi
            shift 2
            ;;
        --install*)
            log_warning "Unknown install parameter: $1"
            shift
            ;;
        *)
            # Non-install arguments go to komari_args
            komari_args="$komari_args $1"
            komari_manual_args="$komari_manual_args $(quote_for_launcher "$1")"
            case "$1" in
                --disable-auto-update|--disable-auto-update=[Tt][Rr][Uu][Ee]|--disable-auto-update=[Tt]|--disable-auto-update=1)
                    auto_update_disabled=true ;;
            esac
            shift
            ;;
    esac
done

if [ -n "$install_base_url" ]; then
    # A custom base is an explicit, single-source release location. Keep URL
    # syntax deliberately narrow: no credentials, query, fragment, or HTTP.
    if ! printf '%s\n' "$install_base_url" | LC_ALL=C grep -Eq '^https://[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*(:[0-9]{1,5})?(/[A-Za-z0-9._~%/-]*)?/?$'; then
        log_error "--install-base-url must be a plain HTTPS origin/path (without credentials, query or fragment)."
        exit 1
    fi
    install_base_authority=${install_base_url#https://}
    install_base_authority=${install_base_authority%%/*}
    if [ "${install_base_authority#*:}" != "$install_base_authority" ]; then
        install_base_port=$(printf '%s' "${install_base_authority##*:}" | sed 's/^0*//')
        [ -n "$install_base_port" ] || install_base_port=0
        if [ "$install_base_port" -lt 1 ] || [ "$install_base_port" -gt 65535 ]; then
            log_error "--install-base-url has an invalid HTTPS port."
            exit 1
        fi
    fi
    if [ -n "$github_proxy" ]; then
        log_error "--install-base-url and --install-ghproxy cannot be combined."
        exit 1
    fi
    if [ -z "$install_version" ] || [ "$install_version" = latest ] || [ "$install_version" = snapshot ]; then
        log_error "--install-base-url requires an exact --install-version tag."
        exit 1
    fi
    if ! printf '%s\n' "$install_version" | LC_ALL=C grep -Eq '^(v[0-9]+\.[0-9]+\.[0-9]+|Snapshot-[A-Za-z0-9._-]+)$'; then
        log_error "Invalid --install-version for --install-base-url."
        exit 1
    fi
    while [ "${install_base_url%/}" != "$install_base_url" ]; do install_base_url=${install_base_url%/}; done
fi

# Remove leading space from komari_args if present
komari_args="${komari_args# }"

# A direct, unprivileged installation belongs entirely to the invoking user.
if [ "$installer_uid" -ne 0 ] && [ "$install_dir_specified" = false ]; then
    case "$os_name" in
        linux|freebsd)
            target_dir="${XDG_DATA_HOME:-$HOME/.local/share}/komari"
            ;;
    esac
fi

komari_agent_path="${target_dir}/agent"

# Function to detect actual init system
detect_init_system() {
    if [ "$os_name" = linux ]; then
        local pid1_process
        pid1_process=$(ps -p 1 -o comm= 2>/dev/null | tr -d ' ')
        if [ "$install_no_systemd" = true ] || [ "$pid1_process" = tini ] ||
            [ -e /system/build.prop ] || [ -e /system/bin/app_process ]; then
            echo manual
            return
        fi
        if [ "$installer_uid" -ne 0 ]; then
            if command -v systemctl >/dev/null 2>&1 && systemctl --user show-environment >/dev/null 2>&1; then
                echo systemd-user
            else
                echo manual
            fi
            return
        fi
    fi

    # Check if running on NixOS (special case)
    if [ -f /etc/NIXOS ]; then
        echo "nixos"
        return
    fi

    # Alpine Linux MUST be checked first
    # Alpine always uses OpenRC, even in containers where PID 1 might be different
    if [ -f /etc/alpine-release ]; then
        if command -v rc-service >/dev/null 2>&1 || [ -f /sbin/openrc-run ]; then
            echo "openrc"
            return
        fi
    fi

    # Get PID 1 process for other detection
    pid1_process=$(ps -p 1 -o comm= 2>/dev/null | tr -d ' ')

    # If PID 1 is systemd, use systemd
    if [ "$pid1_process" = "systemd" ] || [ -d /run/systemd/system ]; then
        if command -v systemctl >/dev/null 2>&1; then
            # Additional verification that systemd is actually functioning
            if systemctl list-units >/dev/null 2>&1; then
                echo "systemd"
                return
            fi
        fi
    fi

    # Check for Gentoo OpenRC (PID 1 is openrc-init)
    if [ "$pid1_process" = "openrc-init" ]; then
        if command -v rc-service >/dev/null 2>&1; then
            echo "openrc"
            return
        fi
    fi

    # Check for other OpenRC systems (not Alpine, already handled)
    # Some systems use traditional init with OpenRC
    if [ "$pid1_process" = "init" ] && [ ! -f /etc/alpine-release ]; then
        # Check if OpenRC is actually managing services
        if [ -d /run/openrc ] && command -v rc-service >/dev/null 2>&1; then
            echo "openrc"
            return
        fi
        # Check for OpenRC files
        if [ -f /sbin/openrc ] && command -v rc-service >/dev/null 2>&1; then
            echo "openrc"
            return
        fi
    fi

    # Check for OpenWrt's procd
    if command -v uci >/dev/null 2>&1 && [ -f /etc/rc.common ]; then
        echo "procd"
        return
    fi

    # Check for macOS launchd
    if [ "$os_name" = "darwin" ] && command -v launchctl >/dev/null 2>&1; then
        echo "launchd"
        return
    fi

    # Fallback: if systemctl exists and appears functional, assume systemd
    if command -v systemctl >/dev/null 2>&1; then
        if systemctl list-units >/dev/null 2>&1; then
            echo "systemd"
            return
        fi
    fi

    # Last resort: check for OpenRC without other indicators
    if command -v rc-service >/dev/null 2>&1 && [ -d /etc/init.d ]; then
        echo "openrc"
        return
    fi

    # check for Upstart (CentOS 6)
    if command -v initctl >/dev/null 2>&1 && [ -d /etc/init ]; then
        echo "upstart"
        return
    fi

    if [ "$os_name" = linux ]; then
        echo manual
    else
        echo unknown
    fi
}

init_system=$(detect_init_system)
case "$init_system" in
    systemd-user) user_service=true ;;
    manual)
        if [ "$installer_uid" -eq 0 ] && [ "$service_user" != root ]; then
            log_error "Run the manual installer directly as the intended user, without sudo."
            exit 1
        fi
        if ! command -v nohup >/dev/null 2>&1; then
            log_error "Manual mode requires nohup (Termux: pkg install coreutils)."
            exit 1
        fi
        ;;
    unknown)
        log_error "Unsupported or unknown init system: $init_system"
        exit 1
        ;;
esac
if [ "$install_no_systemd" = true ] && [ "$os_name" != linux ]; then
    log_error "--install-no-systemd is available on Linux and Termux."
    exit 1
fi
log_info "Detected init system: $init_system"

is_custom_agent() {
    # Inspect bytes only. Calling an older agent with an unknown option could
    # start a second instance. Match the fork and the known legacy du build too.
    LC_ALL=C grep -aFq -e 'komari-agent:custom-runtime' -e '0ozzzii/komari-mcp-bridge' "$1" ||
        { command -v sha256sum >/dev/null 2>&1 &&
          [ "$(sha256sum < "$1" | awk '{print $1}')" = fe03651ae81b744436aa0a382927a7444f7852607770ce2a2f7772b6eaf3664b ]; }
}

if [ "$keep_custom" = true ] && [ "$replace_custom" = true ]; then
    log_error "--keep-custom and --replace-custom are mutually exclusive."
    exit 1
fi
if [ -f "$komari_agent_path" ] && [ "$replace_custom" = false ]; then
    if [ "$keep_custom" = true ]; then
        log_info "Keeping existing agent and service (--keep-custom)."
        exit 0
    fi
    if [ "$auto_update_disabled" = true ] || is_custom_agent "$komari_agent_path"; then
        log_error "Existing custom or pinned agent preserved. Use --keep-custom to leave it in place, or --replace-custom for a deliberate replacement."
        exit 1
    fi
fi

echo -e "${WHITE}===========================================${NC}"
echo -e "${WHITE}    Komari Agent Installation Script     ${NC}"
echo -e "${WHITE}===========================================${NC}"
echo ""
log_config "Installation configuration:"
log_config "  Service name: ${GREEN}$service_name${NC}"
log_config "  Service user: ${GREEN}$service_user${NC}"
log_config "  Install directory: ${GREEN}$target_dir${NC}"
if [ -n "$github_proxy" ]; then
    log_config "  GitHub proxy: (configured; address hidden)"
else
    log_config "  GitHub proxy: (direct)"
fi
log_config "  Binary arguments: (not displayed; may contain Client Token)"
if [ -n "$install_version" ]; then
    log_config "  Specified agent version: ${GREEN}$install_version${NC}"
else
    log_config "  Agent version: ${GREEN}Latest${NC}"
fi
echo ""

# Function to uninstall the previous installation
uninstall_previous() {
    log_step "Checking for previous installation..."

    # Stop and disable service if it exists
    if [ "$init_system" = manual ]; then
        if [ -x "$target_dir/stop-komari-agent.sh" ]; then
            "$target_dir/stop-komari-agent.sh" || return 1
        fi
        if [ "$os_name" = linux ]; then
            manual_unit="${service_name}.service"
            manual_user_unit="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/${manual_unit}"
            if command -v systemctl >/dev/null 2>&1; then
                if [ -f "$manual_user_unit" ] && systemctl --user list-unit-files --no-legend "$manual_unit" 2>/dev/null | awk -v n="$manual_unit" '$1 == n {found=1} END {exit !found}'; then
                    log_info "Stopping and disabling previous systemd user service for manual migration..."
                    systemctl --user stop "$manual_unit" || return 1
                    systemctl --user disable "$manual_unit" || return 1
                    systemctl --user daemon-reload || return 1
                fi
                if [ -f "/etc/systemd/system/${manual_unit}" ] ||
                    systemctl list-unit-files --no-legend "$manual_unit" 2>/dev/null | awk -v n="$manual_unit" '$1 == n {found=1} END {exit !found}'; then
                    log_info "Stopping and disabling previous systemd service for manual migration..."
                    systemctl stop "$manual_unit" || return 1
                    systemctl disable "$manual_unit" || return 1
                    systemctl daemon-reload || return 1
                fi
            fi
            if command -v rc-service >/dev/null 2>&1 && [ -f "/etc/init.d/${service_name}" ]; then
                manual_openrc_status=$(rc-service "$service_name" status 2>&1)
                case "$manual_openrc_status" in
                    *"status: started"*|*"status: running"*) previous_legacy_active=true ;;
                    *"status: stopped"*|*"status: inactive"*) previous_legacy_active=false ;;
                    *) log_error "Cannot safely migrate OpenRC service: status is unreadable."; return 1 ;;
                esac
                if [ ! -d /etc/runlevels ]; then
                    log_error "Cannot safely migrate OpenRC service: runlevel state is unreadable."
                    return 1
                fi
                previous_legacy_kind=openrc
                previous_legacy_runlevels=""
                for manual_runlevel_dir in /etc/runlevels/*; do
                    if [ -d "$manual_runlevel_dir" ] && { [ -e "$manual_runlevel_dir/$service_name" ] || [ -L "$manual_runlevel_dir/$service_name" ]; }; then
                        manual_runlevel=${manual_runlevel_dir##*/}
                        previous_legacy_runlevels="$previous_legacy_runlevels $manual_runlevel"
                        previous_legacy_enabled=true
                    fi
                done
                log_info "Stopping and disabling previous OpenRC service for manual migration..."
                if [ "$previous_legacy_active" = true ]; then rc-service "$service_name" stop || return 1; fi
                for manual_runlevel in $previous_legacy_runlevels; do
                    rc-update del "$service_name" "$manual_runlevel" || return 1
                done
            elif command -v uci >/dev/null 2>&1 && [ -f "/etc/init.d/${service_name}" ]; then
                if [ ! -d /etc/rc.d ]; then
                    log_error "Cannot safely migrate procd service: enable state is unreadable."
                    return 1
                fi
                manual_procd_status=$("/etc/init.d/${service_name}" status 2>&1)
                case "$manual_procd_status" in
                    *"not running"*|*"status: stopped"*|*"status: inactive"*) previous_legacy_active=false ;;
                    *"running"*) previous_legacy_active=true ;;
                    *) log_error "Cannot safely migrate procd service: status is unreadable."; return 1 ;;
                esac
                previous_legacy_kind=procd
                previous_legacy_enabled=false
                for manual_procd_link in /etc/rc.d/S*"$service_name"; do
                    if [ -L "$manual_procd_link" ]; then previous_legacy_enabled=true; break; fi
                done
                log_info "Stopping and disabling previous procd service for manual migration..."
                if [ "$previous_legacy_active" = true ]; then "/etc/init.d/${service_name}" stop || return 1; fi
                if [ "$previous_legacy_enabled" = true ]; then "/etc/init.d/${service_name}" disable || return 1; fi
            elif command -v initctl >/dev/null 2>&1 && [ -f "/etc/init/${service_name}.conf" ]; then
                log_info "Stopping previous Upstart service for manual migration..."
                manual_upstart_conf="/etc/init/${service_name}.conf"
                manual_upstart_backup="${target_dir}/.${service_name}.upstart.disabled"
                if initctl status "$service_name" 2>/dev/null | grep -q 'start/running'; then
                    previous_upstart_active=true
                    initctl stop "$service_name" || return 1
                fi
                if [ -e "$manual_upstart_backup" ]; then
                    log_error "Cannot disable Upstart service: recovery file already exists at $manual_upstart_backup"
                    return 1
                fi
                cp -p "$manual_upstart_conf" "$manual_upstart_backup" || return 1
                rm -f "$manual_upstart_conf" || return 1
            fi
        fi
        return 0
    elif [ "$user_service" = true ]; then
        if [ -f "${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/${service_name}.service" ] ||
            systemctl --user list-unit-files --no-legend "${service_name}.service" 2>/dev/null | awk -v n="${service_name}.service" '$1 == n {found=1} END {exit !found}'; then
            log_info "Stopping and disabling existing systemd user service..."
            systemctl --user stop "${service_name}.service" || return 1
            systemctl --user disable "${service_name}.service" || return 1
            systemctl --user daemon-reload || return 1
        fi
    elif command -v systemctl >/dev/null 2>&1 && { [ -f "/etc/systemd/system/${service_name}.service" ] ||
        systemctl list-unit-files --no-legend "${service_name}.service" 2>/dev/null | awk -v n="${service_name}.service" '$1 == n {found=1} END {exit !found}'; }; then
        log_info "Stopping and disabling existing systemd service..."
        systemctl stop "${service_name}.service" || return 1
        systemctl disable "${service_name}.service" || return 1
        systemctl daemon-reload || return 1
    elif command -v rc-service >/dev/null 2>&1 && [ -f "/etc/init.d/${service_name}" ]; then
        log_info "Stopping and disabling existing OpenRC service..."
        rc-service "${service_name}" stop || return 1
        rc-update del "${service_name}" default || return 1
    elif command -v uci >/dev/null 2>&1 && [ -f "/etc/init.d/${service_name}" ]; then
        log_info "Stopping and disabling existing procd service..."
        "/etc/init.d/${service_name}" stop || return 1
        "/etc/init.d/${service_name}" disable || return 1
    elif command -v initctl >/dev/null 2>&1 && [ -f "/etc/init/${service_name}.conf" ]; then
        log_info "Stopping and removing existing upstart service..."
        initctl stop "${service_name}" || return 1
    elif [ "$os_name" = "darwin" ] && command -v launchctl >/dev/null 2>&1; then
        # macOS launchd service - check both system and user locations
        system_plist="/Library/LaunchDaemons/com.komari.${service_name}.plist"
        user_plist="$HOME/Library/LaunchAgents/com.komari.${service_name}.plist"

        if [ -f "$system_plist" ]; then
            log_info "Stopping and removing existing system launchd service..."
            launchctl bootout system "$system_plist" 2>/dev/null || true
            rm -f "$system_plist"
        fi

        if [ -f "$user_plist" ]; then
            log_info "Stopping and removing existing user launchd service..."
            launchctl bootout gui/$(id -u) "$user_plist" 2>/dev/null || true
            rm -f "$user_plist"
        fi
    fi

    return 0
}

# Existing installation remains running until a candidate is verified.

install_dependencies() {
    log_step "Checking and installing dependencies..."

    local deps="curl"
    local missing_deps=""
    for cmd in $deps; do
        if ! command -v $cmd >/dev/null 2>&1; then
            missing_deps="$missing_deps $cmd"
        fi
    done

    if [ -n "$missing_deps" ]; then
        if [ "$installer_uid" -ne 0 ]; then
            log_error "Missing required dependencies:$missing_deps"
            log_info "Install them with your system package manager, then run this script again."
            exit 1
        fi
        # Check package manager and install dependencies
        if command -v apt >/dev/null 2>&1; then
            log_info "Using apt to install dependencies..."
            apt update
            apt install -y $missing_deps
        elif command -v yum >/dev/null 2>&1; then
            log_info "Using yum to install dependencies..."
            yum install -y $missing_deps
        elif command -v apk >/dev/null 2>&1; then
            log_info "Using apk to install dependencies..."
            apk add $missing_deps
        elif command -v opkg >/dev/null 2>&1; then # OpenWrt / iStoreOS
            log_info "Using opkg to install dependencies (OpenWrt/iStoreOS)..."
            opkg update
            opkg install $missing_deps
        elif command -v brew >/dev/null 2>&1; then
            log_info "Using Homebrew to install dependencies..."
            brew install $missing_deps
        else
            log_error "No supported package manager found (apt/yum/apk/opkg/brew)"
            exit 1
        fi

        # Verify installation
        for cmd in $missing_deps; do
            if ! command -v $cmd >/dev/null 2>&1; then
                log_error "Failed to install $cmd"
                exit 1
            fi
        done
        log_success "Dependencies installed successfully"
    else
        log_success "Dependencies already satisfied"
    fi
}


# Install dependencies
install_dependencies



# Architecture detection with platform-specific support
arch=$(uname -m)
case $arch in
    x86_64)
        arch="amd64"
        ;;
    aarch64|arm64)
        arch="arm64"
        ;;
    loongarch64|loong64)
        arch="loong64"
        ;;
    i386|i686)
        # x86 (32-bit) support
        case $os_name in
            freebsd|linux|windows)
                arch="386"
                ;;
            *)
                log_error "32-bit x86 architecture not supported on $os_name"
                exit 1
                ;;
        esac
        ;;
    armv7*|armv6*)
        # ARM 32-bit support
        case $os_name in
            freebsd|linux)
                arch="arm"
                ;;
            *)
                log_error "32-bit ARM architecture not supported on $os_name"
                exit 1
                ;;
        esac
        ;;
    *)
        log_error "Unsupported architecture: $arch on $os_name"
        exit 1
        ;;
esac
log_info "Detected OS: ${GREEN}$os_name${NC}, Architecture: ${GREEN}$arch${NC}"

file_name="komari-agent-${os_name}-${arch}"

resolve_snapshot_version() {
    snapshot_api_url="https://api.github.com/repos/${komari_release_repository}/releases?per_page=100"
    if [ -n "$github_proxy" ]; then
        snapshot_api_urls="${github_proxy}/${snapshot_api_url} ${snapshot_api_url}"
    else
        snapshot_api_urls="$snapshot_api_url"
    fi

    for api_url in $snapshot_api_urls; do
        if ! releases_json=$(curl -fsSL --connect-timeout 15 \
            -H "Accept: application/vnd.github+json" \
            -H "User-Agent: komari-agent-installer" \
            "$api_url"); then
            releases_json=""
        fi

        if [ -n "$releases_json" ]; then
            RESOLVED_SNAPSHOT_VERSION=$(printf '%s\n' "$releases_json" |
                grep -o '"tag_name":[[:space:]]*"Snapshot-[^"]*"' |
                sed 's/.*"\(Snapshot-[^"]*\)".*/\1/' |
                LC_ALL=C sort -r |
                head -n 1)
            if [ -n "$RESOLVED_SNAPSHOT_VERSION" ]; then
                return 0
            fi
        fi

        if [ "$api_url" != "$snapshot_api_url" ]; then
            log_warning "Failed to resolve snapshot releases through GitHub proxy, retrying directly."
        fi
    done

    return 1
}

if [ -n "$install_base_url" ]; then
    version_to_install=$install_version
else
    version_to_install=$(komari_resolve_release "$install_version") || {
        log_error "Cannot resolve an installable release; existing installation preserved"; exit 1;
    }
fi
log_info "Installing version: $version_to_install"
file_name="komari-agent-${os_name}-${arch}"
[ "$os_name" != windows ] || file_name="${file_name}.exe"
if [ -n "$install_base_url" ]; then
    download_url="${install_base_url}/${version_to_install}/${file_name}"
else
    download_url="https://github.com/${komari_release_repository}/releases/download/${version_to_install}/${file_name}"
    if [ -n "$github_proxy" ]; then download_url="${github_proxy}/${download_url}"; fi
fi

log_step "Creating installation directory: ${GREEN}$target_dir${NC}"
mkdir -p "$target_dir"
if [ "$installer_uid" -eq 0 ] && [ "$service_user" != "root" ]; then
    chown "$service_user" "$target_dir"
fi

# Download with automatic mirror fallback.
# 直连失败自动依次尝试常见 GitHub 加速镜像, 可用 --install-no-mirror 关闭.
if [ -n "$install_base_url" ] || [ -n "$github_proxy" ] || [ "$install_no_mirror" = "true" ]; then
    download_urls="$download_url"
else
    download_urls="
${download_url}
https://ghfast.top/${download_url}
https://gh-proxy.com/${download_url}
https://ghproxy.net/${download_url}
"
fi

candidate=$(mktemp "${target_dir}/.agent-candidate.XXXXXX") || exit 1
trap 'rm -f -- "$candidate"' EXIT HUP INT TERM
dl_ok=""
for u in $download_urls; do
    log_step "Downloading and verifying $file_name ..."
    if [ -n "$install_base_url" ]; then
        if komari_fetch_base_verified "$u" "$candidate"; then dl_ok=1; break; fi
    elif komari_fetch_verified "$u" "$candidate"; then
        dl_ok=1
        break
    fi
done
if [ -z "$dl_ok" ]; then
    log_error "Release download or SHA256 verification failed; existing installation preserved"
    exit 1
fi

# Keep a private recovery point for the binary and any locally managed
# systemd unit. It is removed only after the replacement service is healthy.
rollback_dir=$(mktemp -d "${target_dir}/.install-rollback.XXXXXX") || exit 1
chmod 700 "$rollback_dir" || { rm -rf -- "$rollback_dir"; exit 1; }
previous_unit_file=""
previous_unit_kind=""
previous_unit_active=false
previous_unit_enabled=false
previous_upstart_active=false
previous_legacy_kind=""
previous_legacy_active=false
previous_legacy_enabled=false
previous_legacy_runlevels=""
if [ "$os_name" = linux ]; then
    possible_user_unit="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/${service_name}.service"
    possible_system_unit="/etc/systemd/system/${service_name}.service"
    if [ -f "$possible_user_unit" ]; then
        previous_unit_file=$possible_user_unit
        previous_unit_kind=systemd-user
        if command -v systemctl >/dev/null 2>&1; then
            systemctl --user is-active --quiet "${service_name}.service" && previous_unit_active=true
            systemctl --user is-enabled --quiet "${service_name}.service" && previous_unit_enabled=true
        fi
    elif [ -f "$possible_system_unit" ]; then
        previous_unit_file=$possible_system_unit
        previous_unit_kind=systemd
        if command -v systemctl >/dev/null 2>&1; then
            systemctl is-active --quiet "${service_name}.service" && previous_unit_active=true
            systemctl is-enabled --quiet "${service_name}.service" && previous_unit_enabled=true
        fi
    fi
fi
if [ -n "$previous_unit_file" ]; then
    cp -p "$previous_unit_file" "$rollback_dir/old.service" || { rm -rf -- "$rollback_dir"; exit 1; }
fi
if [ -f "$komari_agent_path" ]; then
    cp -p "$komari_agent_path" "${komari_agent_path}.backup" || { rm -rf -- "$rollback_dir"; exit 1; }
    cp -p "$komari_agent_path" "$rollback_dir/old-agent" || { rm -rf -- "$rollback_dir"; exit 1; }
fi

rollback_install() {
    log_error "Replacement failed; restoring the previous agent and service state."
    rollback_preserved=false
    if [ "$init_system" = systemd-user ]; then
        systemctl --user stop "${service_name}.service" >/dev/null 2>&1 || :
        systemctl --user disable "${service_name}.service" >/dev/null 2>&1 || :
    elif [ "$init_system" = systemd ]; then
        systemctl stop "${service_name}.service" >/dev/null 2>&1 || :
        systemctl disable "${service_name}.service" >/dev/null 2>&1 || :
    elif [ "$init_system" = manual ]; then
        if [ -x "$target_dir/stop-komari-agent.sh" ]; then "$target_dir/stop-komari-agent.sh" >/dev/null 2>&1 || :; fi
        if [ "$previous_unit_kind" = systemd-user ]; then
            systemctl --user stop "${service_name}.service" >/dev/null 2>&1 || :
            systemctl --user disable "${service_name}.service" >/dev/null 2>&1 || :
        elif [ "$previous_unit_kind" = systemd ]; then
            systemctl stop "${service_name}.service" >/dev/null 2>&1 || :
            systemctl disable "${service_name}.service" >/dev/null 2>&1 || :
        fi
        if [ -n "${manual_upstart_conf:-}" ] && [ -f "${manual_upstart_backup:-}" ]; then
            restore_upstart_tmp="${manual_upstart_conf}.restore.$$"
            if cp -p "$manual_upstart_backup" "$restore_upstart_tmp" && mv -f "$restore_upstart_tmp" "$manual_upstart_conf"; then
                initctl reload-configuration || log_error "Could not reload the restored Upstart configuration."
                if [ "$previous_upstart_active" = true ]; then initctl start "$service_name" || log_error "Could not restart the previous Upstart service."; fi
            else
                rm -f -- "$restore_upstart_tmp"
                log_error "Could not restore the previous Upstart job; recovery copy remains at $manual_upstart_backup"
                rollback_preserved=true
            fi
        fi
    fi
    if [ -f "$komari_agent_path.backup" ] && [ -f "$rollback_dir/old-agent" ]; then
        restore_agent_tmp="${komari_agent_path}.restore.$$"
        if cp -p "$rollback_dir/old-agent" "$restore_agent_tmp" && mv -f "$restore_agent_tmp" "$komari_agent_path"; then :; else
            rm -f -- "$restore_agent_tmp"
            log_error "Could not restore the previous agent binary; recovery copy remains at $rollback_dir/old-agent"
            rollback_preserved=true
        fi
    elif [ ! -f "$rollback_dir/old-agent" ]; then
        rm -f -- "$komari_agent_path"
    fi
    if [ -n "$previous_unit_file" ]; then
        restore_unit_tmp="${previous_unit_file}.restore.$$"
        if cp -p "$rollback_dir/old.service" "$restore_unit_tmp" && mv -f "$restore_unit_tmp" "$previous_unit_file"; then :; else
            rm -f -- "$restore_unit_tmp"
            log_error "Could not restore the previous service unit; recovery copy remains at $rollback_dir/old.service"
            rollback_preserved=true
        fi
    elif [ -n "${service_file:-}" ]; then
        rm -f -- "$service_file"
    fi
    if [ "$init_system" = manual ] && [ "$previous_legacy_kind" = openrc ]; then
        for restore_runlevel in $previous_legacy_runlevels; do
            rc-update add "$service_name" "$restore_runlevel" || { log_error "Could not restore OpenRC runlevel $restore_runlevel."; rollback_preserved=true; }
        done
        if [ "$previous_legacy_active" = true ]; then
            rc-service "$service_name" start || { log_error "Could not restart the previous OpenRC service."; rollback_preserved=true; }
        fi
    elif [ "$init_system" = manual ] && [ "$previous_legacy_kind" = procd ]; then
        if [ "$previous_legacy_enabled" = true ]; then
            "/etc/init.d/${service_name}" enable || { log_error "Could not re-enable the previous procd service."; rollback_preserved=true; }
        fi
        if [ "$previous_legacy_active" = true ]; then
            "/etc/init.d/${service_name}" start || { log_error "Could not restart the previous procd service."; rollback_preserved=true; }
        fi
    fi
    if [ "$init_system" = systemd-user ]; then
        systemctl --user daemon-reload || log_error "Could not reload the restored user unit state."
    elif [ "$init_system" = systemd ]; then
        systemctl daemon-reload || log_error "Could not reload the restored system unit state."
    elif [ "$init_system" = manual ] && [ "$previous_unit_kind" = systemd-user ]; then
        systemctl --user daemon-reload || log_error "Could not reload the restored user unit state."
    elif [ "$init_system" = manual ] && [ "$previous_unit_kind" = systemd ]; then
        systemctl daemon-reload || log_error "Could not reload the restored system unit state."
    fi
    if [ -n "$previous_unit_file" ] && command -v systemctl >/dev/null 2>&1; then
        if [ "$previous_unit_kind" = systemd-user ]; then
            if [ "$previous_unit_enabled" = true ]; then systemctl --user enable "${service_name}.service" || log_error "Could not re-enable the previous user service."; fi
            if [ "$previous_unit_active" = true ]; then systemctl --user start "${service_name}.service" || log_error "Could not restart the previous user service."; fi
        else
            if [ "$previous_unit_enabled" = true ]; then systemctl enable "${service_name}.service" || log_error "Could not re-enable the previous service."; fi
            if [ "$previous_unit_active" = true ]; then systemctl start "${service_name}.service" || log_error "Could not restart the previous service."; fi
        fi
    fi
    if [ "$rollback_preserved" = false ]; then
        rm -rf -- "$rollback_dir"
    else
        log_error "Recovery files preserved at $rollback_dir"
    fi
}

# Back up the binary before the official service replacement sequence.
uninstall_previous || { rollback_install; exit 1; }
mv -f "$candidate" "$komari_agent_path" || { rollback_install; exit 1; }
trap - EXIT HUP INT TERM

# Set executable permissions
chmod +x "$komari_agent_path" || { rollback_install; exit 1; }
if [ "$installer_uid" -eq 0 ] && [ "$service_user" != "root" ]; then
    chown "$service_user" "$komari_agent_path" || { rollback_install; exit 1; }
fi
log_success "Komari-agent installed to ${GREEN}$komari_agent_path${NC}"

# Detect init system and configure service
log_step "Configuring system service..."

install_manual_launchers() (
    set -e
    umask 077
    tmp=$(mktemp -d "$target_dir/.manual-launchers.XXXXXX")
    trap 'rm -f -- "$tmp/start-komari-agent.sh" "$tmp/stop-komari-agent.sh"; rmdir -- "$tmp"' EXIT
    trap 'exit 1' HUP INT TERM
    # Use the running shell path on Android, whose /bin/sh may be absent.
    interpreter=/bin/sh
    if [ -n "${PREFIX:-}" ] && [ -x "$PREFIX/bin/sh" ]; then
        interpreter=$PREFIX/bin/sh
    fi
    {
        printf '#!%s\n' "$interpreter"
        cat <<'KOMARI_MANUAL'
set -eu
umask 077
cd -- "$(dirname -- "$0")"
pid_file=./komari-agent.pid
log_file=./komari-agent.log
process_stamp() {
    # The stat suffix after the last ") " starts at field 3 (state).
    # Field 22 identifies this process even if a PID is reused.
    case "$1" in ''|*[!0-9]*|0|1) return 1 ;; esac
    stamp_line=$(cat "/proc/$1/stat" 2>/dev/null) || return 1
    printf '%s\n' "${stamp_line##*) }" | awk 'NF >= 20 && $1 != "Z" {print $20; found=1} END {exit !found}'
}

lock_dir=./.komari-agent.lock
lock_owner=$lock_dir/owner
acquire_lock() {
    if mkdir "$lock_dir" 2>/dev/null; then
        lock_started=$(process_stamp "$$") || { rmdir "$lock_dir"; return 1; }
        printf '%s %s\n' "$$" "$lock_started" > "$lock_owner" || { rmdir "$lock_dir"; return 1; }
        return 0
    fi

    lock_pid=''
    lock_expected=''
    if [ -r "$lock_owner" ]; then
        read -r lock_pid lock_expected < "$lock_owner" || :
    fi
    lock_legacy=false
    case "$lock_pid" in ''|*[!0-9]*|0|1) lock_pid='' ;; esac
    case "$lock_expected" in ''|*[!0-9]*) lock_expected='' ;; esac
    if [ -z "$lock_pid" ] || [ -z "$lock_expected" ]; then
        # Older launchers left an empty directory lock. Reclaim it only after
        # a full minute, which is longer than the launcher's stop wait window.
        lock_mtime=$(stat -c %Y "$lock_dir" 2>/dev/null || stat -f %m "$lock_dir" 2>/dev/null || echo '')
        lock_now=$(date +%s)
        case "$lock_mtime:$lock_now" in *[!0-9:]*) lock_mtime='' ;; esac
        if [ -z "$lock_mtime" ] || [ $((lock_now - lock_mtime)) -lt 60 ]; then
            echo "Another Komari start or stop is in progress (legacy lock has not expired)." >&2
            return 1
        fi
        lock_legacy=true
        lock_pid='legacy'
        lock_expected=$lock_mtime
    else
        lock_current=$(process_stamp "$lock_pid" 2>/dev/null) || lock_current=''
        if [ -n "$lock_current" ] && [ "$lock_current" = "$lock_expected" ]; then
            echo "Another Komari start or stop is in progress (PID $lock_pid)." >&2
            return 1
        fi
    fi

    # Move the stale directory out of the canonical name before deleting it.
    # Recheck its owner after the rename so a concurrent replacement is kept.
    stale_lock="${lock_dir}.stale.$$"
    if ! mv "$lock_dir" "$stale_lock" 2>/dev/null; then
        echo "Another Komari start or stop is in progress." >&2
        return 1
    fi
    stale_pid=''
    stale_expected=''
    if [ -r "$stale_lock/owner" ]; then read -r stale_pid stale_expected < "$stale_lock/owner" || :; fi
    if [ "$lock_legacy" = true ] && [ ! -e "$stale_lock/owner" ]; then
        stale_mtime=$(stat -c %Y "$stale_lock" 2>/dev/null || stat -f %m "$stale_lock" 2>/dev/null || echo '')
        [ "$stale_mtime" = "$lock_expected" ] || { mv "$stale_lock" "$lock_dir" 2>/dev/null || :; echo "Another Komari start or stop is in progress." >&2; return 1; }
    elif [ "$stale_pid" != "$lock_pid" ] || [ "$stale_expected" != "$lock_expected" ]; then
        mv "$stale_lock" "$lock_dir" 2>/dev/null || :
        echo "Another Komari start or stop is in progress." >&2
        return 1
    fi
    [ ! -e "$stale_lock/owner" ] || rm -f -- "$stale_lock/owner" || return 1
    rmdir "$stale_lock" || return 1
    if ! mkdir "$lock_dir" 2>/dev/null; then
        echo "Another Komari start or stop is in progress." >&2
        return 1
    fi
    lock_started=$(process_stamp "$$") || { rmdir "$lock_dir"; return 1; }
    printf '%s %s\n' "$$" "$lock_started" > "$lock_owner" || { rmdir "$lock_dir"; return 1; }
}
if ! acquire_lock; then exit 1; fi
trap 'rm -f -- "$lock_owner"; rmdir "$lock_dir" 2>/dev/null || :' EXIT
trap 'exit 1' HUP INT TERM

agent_is_running() {
    [ -r "$pid_file" ] && read -r pid started < "$pid_file" &&
        [ -n "$started" ] && [ "$(process_stamp "$pid")" = "$started" ]
}

if [ "${1:-}" = --stop ]; then
    if agent_is_running; then
        kill "$pid" || exit 1
        for attempt in 1 2 3 4 5 6 7 8 9 10; do
            agent_is_running || break
            sleep 1
        done
        if agent_is_running; then
            echo "Komari Agent PID $pid did not stop." >&2
            exit 1
        fi
    fi
    rm -f -- "$pid_file"
    echo "Komari Agent stopped."
    exit 0
fi
if agent_is_running; then
    printf 'Komari Agent already running. PID %s, log %s/%s\n' "$pid" "$PWD" "komari-agent.log"
    exit 0
fi
: >> "$log_file"
chmod 600 "$log_file"
KOMARI_MANUAL
        printf 'nohup ./agent%s >> "$log_file" 2>&1 < /dev/null &\n' "$komari_manual_args"
        cat <<'KOMARI_MANUAL'
pid=$!
if ! started=$(process_stamp "$pid"); then
    echo "Komari Agent exited during startup. See $PWD/komari-agent.log." >&2
    exit 1
fi
printf '%s %s\n' "$pid" "$started" > "$pid_file"
sleep 1
if ! agent_is_running; then
    rm -f -- "$pid_file"
    echo "Komari Agent exited during startup. See $PWD/komari-agent.log." >&2
    exit 1
fi
printf 'Komari Agent running. PID %s, log %s/%s\n' "$pid" "$PWD" "komari-agent.log"
KOMARI_MANUAL
    } > "$tmp/start-komari-agent.sh"
    {
        printf '#!%s\n' "$interpreter"
        printf '%s\n' 'exec "$(dirname -- "$0")/start-komari-agent.sh" --stop'
    } > "$tmp/stop-komari-agent.sh"
    chmod 700 "$tmp/start-komari-agent.sh" "$tmp/stop-komari-agent.sh"
    mv -f -- "$tmp/start-komari-agent.sh" "$tmp/stop-komari-agent.sh" "$target_dir/"
)

# Handle each init system
if [ "$init_system" = "nixos" ]; then
    log_warning "NixOS detected. System services must be configured declaratively."
    log_info "Please add the following to your NixOS configuration:"
    echo ""
    echo -e "${CYAN}systemd.services.${service_name} = {${NC}"
    echo -e "${CYAN}  description = \"Komari Agent Service\";${NC}"
    echo -e "${CYAN}  after = [ \"network.target\" ];${NC}"
    echo -e "${CYAN}  wantedBy = [ \"multi-user.target\" ];${NC}"
    echo -e "${CYAN}  serviceConfig = {${NC}"
    echo -e "${CYAN}    Type = \"simple\";${NC}"
    echo -e "${CYAN}    ExecStart = \"${komari_agent_path} <PRIVATE_AGENT_ARGUMENTS>\";${NC}"
    echo -e "${CYAN}    WorkingDirectory = \"${target_dir}\";${NC}"
    echo -e "${CYAN}    Restart = \"always\";${NC}"
    echo -e "${CYAN}    User = \"${service_user}\";${NC}"
    echo -e "${CYAN}  };${NC}"
    echo -e "${CYAN}};${NC}"
    echo ""
    log_info "Replace PRIVATE_AGENT_ARGUMENTS locally with your endpoint and token before rebuilding; private arguments are not printed."
    log_info "Then run: sudo nixos-rebuild switch"
    log_warning "Service not started automatically on NixOS. Please rebuild your configuration."
elif [ "$init_system" = manual ]; then
    install_manual_launchers || { rollback_install; exit 1; }
    "$target_dir/start-komari-agent.sh" || { rollback_install; exit 1; }
elif [ "$init_system" = "openrc" ]; then
    # OpenRC service configuration
    log_info "Using OpenRC for service management"
    service_file="/etc/init.d/${service_name}"
    cat > "$service_file" << EOF
#!/sbin/openrc-run

name="Komari Agent Service"
description="Komari monitoring agent"
command="${komari_agent_path}"
command_args="${komari_args}"
command_user="${service_user}"
directory="${target_dir}"
pidfile="/run/${service_name}.pid"
retry="SIGTERM/30"
supervisor=supervise-daemon

depend() {
    need net
    after network
}
EOF

    # Set permissions and enable service
    chmod 700 "$service_file"
    rc-update add ${service_name} default
    rc-service ${service_name} start
    log_success "OpenRC service configured and started"
elif [ "$init_system" = "systemd-user" ]; then
    log_info "Using systemd user service management"
    service_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
    service_file="${service_dir}/${service_name}.service"
    mkdir -p "$service_dir" || { rollback_install; exit 1; }
    service_tmp=$(mktemp "${service_dir}/.${service_name}.service.XXXXXX") || { rollback_install; exit 1; }
    cat > "$service_tmp" << EOF
[Unit]
Description=Komari Agent Service
After=network.target

[Service]
Type=simple
ExecStart=${komari_agent_path} ${komari_args}
WorkingDirectory=${target_dir}
Restart=always

[Install]
WantedBy=default.target
EOF
    if [ "$?" -ne 0 ]; then rm -f -- "$service_tmp"; rollback_install; exit 1; fi
    chmod 600 "$service_tmp" && mv -f "$service_tmp" "$service_file" || { rm -f -- "$service_tmp"; rollback_install; exit 1; }
    systemctl --user daemon-reload || { rollback_install; exit 1; }
    systemctl --user enable "${service_name}.service" || { rollback_install; exit 1; }
    systemctl --user start "${service_name}.service" || { rollback_install; exit 1; }
    systemctl --user is-active --quiet "${service_name}.service" || { rollback_install; exit 1; }
    log_success "Systemd user service configured and started"
elif [ "$init_system" = "systemd" ]; then
    # Systemd service configuration
    log_info "Using systemd for service management"
    service_file="/etc/systemd/system/${service_name}.service"
    service_tmp=$(mktemp "/etc/systemd/system/.${service_name}.service.XXXXXX") || { rollback_install; exit 1; }
    cat > "$service_tmp" << EOF
[Unit]
Description=Komari Agent Service
After=network.target

[Service]
Type=simple
ExecStart=${komari_agent_path} ${komari_args}
WorkingDirectory=${target_dir}
Restart=always
User=${service_user}

[Install]
WantedBy=multi-user.target
EOF
    if [ "$?" -ne 0 ]; then rm -f -- "$service_tmp"; rollback_install; exit 1; fi

    # Reload systemd and start service
    chmod 600 "$service_tmp" && mv -f "$service_tmp" "$service_file" || { rm -f -- "$service_tmp"; rollback_install; exit 1; }
    systemctl daemon-reload || { rollback_install; exit 1; }
    systemctl enable "${service_name}.service" || { rollback_install; exit 1; }
    systemctl start "${service_name}.service" || { rollback_install; exit 1; }
    systemctl is-active --quiet "${service_name}.service" || { rollback_install; exit 1; }
    log_success "Systemd service configured and started"
elif [ "$init_system" = "procd" ]; then
    # procd service configuration (OpenWrt)
    log_info "Using procd for service management"
    service_file="/etc/init.d/${service_name}"
    cat > "$service_file" << EOF
#!/bin/sh /etc/rc.common

START=99
STOP=10

USE_PROCD=1

PROG="${komari_agent_path}"
ARGS="${komari_args}"

start_service() {
    procd_open_instance
    # 参数逐个追加, 避免整串拼接可能导致的引号/转义问题
    procd_set_param command "\$PROG"
    # shellcheck disable=SC2086
    procd_append_param command \$ARGS
    procd_set_param respawn
    procd_set_param stdout 1
    procd_set_param stderr 1
    procd_set_param user ${service_user}
    procd_close_instance
}

# 移除 killall 版 stop_service:
# USE_PROCD=1 时 rc.common 默认 stop 会通过 procd 正确终止实例,
# 按进程名 killall 反而可能误杀同名进程, 且无法阻止 respawn.

reload_service() {
    stop
    start
}
EOF

    # Set permissions and enable service
    chmod 700 "$service_file"
    /etc/init.d/${service_name} enable
    /etc/init.d/${service_name} start
    log_success "procd service configured and started"
elif [ "$init_system" = "launchd" ]; then
    # macOS launchd service configuration
    log_info "Using launchd for service management"

    # [[ =~ ]] -> case (POSIX); 判定用户级还是系统级安装
    is_user_install=false
    case "$target_dir" in
        /Users/*) is_user_install=true ;;
    esac
    [ "$installer_uid" -ne 0 ] && is_user_install=true

    if [ "$is_user_install" = true ]; then
        # User-level service (LaunchAgent)
        plist_dir="$HOME/Library/LaunchAgents"
        plist_file="$plist_dir/com.komari.${service_name}.plist"
        log_info "Installing as user-level service (LaunchAgent)"
        mkdir -p "$plist_dir"
        service_user="$(whoami)"
        log_dir="$HOME/Library/Logs"
    else
        # System-level service (LaunchDaemon)
        plist_dir="/Library/LaunchDaemons"
        plist_file="$plist_dir/com.komari.${service_name}.plist"
        log_info "Installing as system-level service (LaunchDaemon)"
        log_dir="/var/log"
    fi

    # Create the launchd plist file
    cat > "$plist_file" << EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.komari.${service_name}</string>
    <key>ProgramArguments</key>
    <array>
        <string>${komari_agent_path}</string>
EOF

    # Add program arguments if provided
    if [ -n "$komari_args" ]; then
        echo "$komari_args" | xargs -n1 printf "        <string>%s</string>\n" >> "$plist_file"
    fi

    cat >> "$plist_file" << EOF
    </array>
    <key>WorkingDirectory</key>
    <string>${target_dir}</string>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>UserName</key>
    <string>${service_user}</string>
    <key>StandardOutPath</key>
    <string>${log_dir}/${service_name}.log</string>
    <key>StandardErrorPath</key>
    <string>${log_dir}/${service_name}.log</string>
</dict>
</plist>
EOF

    # Load and start the service
    if [ "$is_user_install" = true ]; then
        # User-level service
        if launchctl bootstrap gui/$(id -u) "$plist_file"; then
            log_success "User-level launchd service configured and started"
        else
            log_error "Failed to load user-level launchd service"
            exit 1
        fi
    else
        # System-level service
        if launchctl bootstrap system "$plist_file"; then
            log_success "System-level launchd service configured and started"
        else
            log_error "Failed to load system-level launchd service"
            exit 1
        fi
    fi
elif [ "$init_system" = "upstart" ]; then
    # Upstart service configuration
    log_info "Using upstart for service management"
    service_file="/etc/init/${service_name}.conf"
    cat > "$service_file" << EOF
# KOMARI Agent
description "Komari Agent Service"

chdir ${target_dir}
start on filesystem or runlevel [2345]
stop on runlevel [!2345]

respawn
respawn limit 10 5
umask 022

console none

setuid ${service_user}

pre-start script
    test -x ${komari_agent_path} || { stop; exit 0; }
end script

# Start
script
    exec ${komari_agent_path} ${komari_args}
end script
EOF
    # enable Upstart unit
    initctl reload-configuration
    initctl start ${service_name}
    log_success "Upstart service configured and started"
else
    log_error "Unsupported or unknown init system detected: $init_system"
    log_error "Supported init systems: systemd, openrc, procd, launchd"
    exit 1
fi

echo ""
echo -e "${WHITE}===========================================${NC}"
if [ "$init_system" = nixos ]; then
    log_success "Komari-agent binary installed!"
    log_warning "NixOS requires declarative service configuration."
    log_info "Please add the service configuration to your NixOS config and rebuild."
else
    log_success "Komari-agent installation completed!"
fi
log_config "Service: ${GREEN}$service_name${NC}"
log_config "Arguments: (not displayed; may contain Client Token)"
echo -e "${WHITE}===========================================${NC}"
if [ "$init_system" != nixos ] && [ "$init_system" != manual ]; then
    komari_install_status || { rollback_install; exit 1; }
fi
rm -rf -- "$rollback_dir"
