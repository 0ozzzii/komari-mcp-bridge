#!/bin/sh
# BEGIN KOMARI RELEASE DOWNLOAD
# Embedded verbatim into the official-derived installers by render-installers.py.
# No secret is sent to GitHub; Client Tokens are only passed to the agent.
komari_release_repository=${KOMARI_RELEASE_REPOSITORY:-0ozzzii/komari-mcp-bridge-release}
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
    komari_dir=$(mktemp -d "${komari_dest}.download.XXXXXX")
    trap 'rm -rf -- "$komari_dir"' EXIT HUP INT TERM
    curl -fsSL --retry 2 --connect-timeout 15 --max-time 300 "$komari_url" -o "$komari_dir/binary"
    test -s "$komari_dir/binary"
    curl -fsSL --retry 2 --connect-timeout 15 --max-time 60 "${komari_url%/*}/sha256sums.txt" -o "$komari_dir/manifest"
    komari_expected=$(awk -v n="$komari_name" '$2 == n || $2 == "*" n {print $1; count++} END {if(count!=1) exit 1}' "$komari_dir/manifest")
    printf '%s\n' "$komari_expected" | LC_ALL=C grep -Eq '^[a-fA-F0-9]{64}$'
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
    chmod 755 "$komari_dir/binary"
    mv -f "$komari_dir/binary" "$komari_dest"
)
# END KOMARI RELEASE DOWNLOAD

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
install_dir_specified=false
install_no_mirror=false # 关闭自动加速镜像
service_user="${SUDO_USER:-$(id -un)}"
user_service=false

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

# Parse install-specific arguments
komari_args=""
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
        --install-no-mirror) # 新增: 关闭自动加速镜像
            install_no_mirror=true
            shift
            ;;
        --install*)
            log_warning "Unknown install parameter: $1"
            shift
            ;;
        *)
            # Non-install arguments go to komari_args
            komari_args="$komari_args $1"
            shift
            ;;
    esac
done

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

# User services are the only service type a non-root Linux installation can manage.
if [ "$installer_uid" -ne 0 ] && [ "$os_name" = "linux" ]; then
    if command -v systemctl >/dev/null 2>&1 && systemctl --user show-environment >/dev/null 2>&1; then
        user_service=true
    else
        log_error "A non-root Linux installation requires a running systemd user session"
        log_info "Log in through systemd or install with elevated privileges."
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
    if [ "$user_service" = true ]; then
        if systemctl --user list-unit-files | grep -q "${service_name}.service"; then
            log_info "Stopping and disabling existing systemd user service..."
            systemctl --user stop "${service_name}.service" || true
            systemctl --user disable "${service_name}.service" || true
            rm -f "${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/${service_name}.service"
            chmod 600 "$service_file"
    systemctl --user daemon-reload
        fi
    elif command -v systemctl >/dev/null 2>&1 && systemctl list-unit-files | grep -q "${service_name}.service"; then
        log_info "Stopping and disabling existing systemd service..."
        systemctl stop ${service_name}.service
        systemctl disable ${service_name}.service
        rm -f "/etc/systemd/system/${service_name}.service"
        systemctl daemon-reload
    elif command -v rc-service >/dev/null 2>&1 && [ -f "/etc/init.d/${service_name}" ]; then
        log_info "Stopping and disabling existing OpenRC service..."
        rc-service ${service_name} stop
        rc-update del ${service_name} default
        rm -f "/etc/init.d/${service_name}"
    elif command -v uci >/dev/null 2>&1 && [ -f "/etc/init.d/${service_name}" ]; then
        log_info "Stopping and disabling existing procd service..."
        /etc/init.d/${service_name} stop
        /etc/init.d/${service_name} disable
        rm -f "/etc/init.d/${service_name}"
    elif command -v initctl >/dev/null 2>&1 && [ -f "/etc/init/${service_name}.conf" ]; then
        log_info "Stopping and removing existing upstart service..."
        initctl stop ${service_name}
        rm -f "/etc/init/${service_name}.conf"
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

    # Remove old binary if it exists
    if [ -f "$komari_agent_path" ]; then
        log_info "Removing old binary..."
        rm -f "$komari_agent_path"
    fi
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

version_to_install=$(komari_resolve_release "$install_version") || {
    log_error "Cannot resolve an installable release; existing installation preserved"; exit 1;
}
log_info "Installing version: $version_to_install"
file_name="komari-agent-${os_name}-${arch}"
[ "$os_name" != windows ] || file_name="${file_name}.exe"
download_url="https://github.com/${komari_release_repository}/releases/download/${version_to_install}/${file_name}"
if [ -n "$github_proxy" ]; then download_url="${github_proxy}/${download_url}"; fi

log_step "Creating installation directory: ${GREEN}$target_dir${NC}"
mkdir -p "$target_dir"
if [ "$installer_uid" -eq 0 ] && [ "$service_user" != "root" ]; then
    chown "$service_user" "$target_dir"
fi

# Download with automatic mirror fallback.
# 直连失败自动依次尝试常见 GitHub 加速镜像, 可用 --install-no-mirror 关闭.
if [ -n "$github_proxy" ] || [ "$install_no_mirror" = "true" ]; then
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
    if komari_fetch_verified "$u" "$candidate"; then dl_ok=1; break; fi
done
if [ -z "$dl_ok" ]; then
    log_error "Release download or SHA256 verification failed; existing installation preserved"
    exit 1
fi
# Back up the binary before the official service replacement sequence.
if [ -f "$komari_agent_path" ]; then
    cp -p "$komari_agent_path" "${komari_agent_path}.backup" || exit 1
fi
uninstall_previous
mv -f "$candidate" "$komari_agent_path" || exit 1
trap - EXIT HUP INT TERM

# Set executable permissions
chmod +x "$komari_agent_path"
if [ "$installer_uid" -eq 0 ] && [ "$service_user" != "root" ]; then
    chown "$service_user" "$komari_agent_path"
fi
log_success "Komari-agent installed to ${GREEN}$komari_agent_path${NC}"

# Detect init system and configure service
log_step "Configuring system service..."

# Function to detect actual init system
detect_init_system() {
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
    local pid1_process=$(ps -p 1 -o comm= 2>/dev/null | tr -d ' ')

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

    echo "unknown"
}

init_system=$(detect_init_system)
if [ "$user_service" = true ]; then
    init_system="systemd-user"
fi
log_info "Detected init system: ${GREEN}$init_system${NC}"

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
    mkdir -p "$service_dir"
    cat > "$service_file" << EOF
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
    chmod 600 "$service_file"
    systemctl --user daemon-reload
    systemctl --user enable --now "${service_name}.service"
    log_success "Systemd user service configured and started"
elif [ "$init_system" = "systemd" ]; then
    # Systemd service configuration
    log_info "Using systemd for service management"
    service_file="/etc/systemd/system/${service_name}.service"
    cat > "$service_file" << EOF
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

    # Reload systemd and start service
    chmod 600 "$service_file"
    systemctl daemon-reload
    systemctl enable ${service_name}.service
    systemctl start ${service_name}.service
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
if [ -f /etc/NIXOS ]; then
    log_success "Komari-agent binary installed!"
    log_warning "NixOS requires declarative service configuration."
    log_info "Please add the service configuration to your NixOS config and rebuild."
else
    log_success "Komari-agent installation completed!"
fi
log_config "Service: ${GREEN}$service_name${NC}"
log_config "Arguments: (not displayed; may contain Client Token)"
echo -e "${WHITE}===========================================${NC}"
