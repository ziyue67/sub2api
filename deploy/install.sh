#!/bin/bash
#
# Sub2API Installation Script (Docker)
# Sub2API 安装脚本（Docker 版）
# Usage: curl -sSL https://raw.githubusercontent.com/ziyue67/sub2api/main/deploy/install.sh | bash
#
# 本 Fork 只分发 Docker 镜像（linux/amd64），不再发布 tar.gz / zip 二进制包。
# This fork ships Docker images only (linux/amd64); no tar.gz / zip archives are published.

set -e

# Bash 4+ is required for associative arrays used by the localized message table.
# Keep this guard before any Bash 4-only syntax so older shells fail with a clear hint.
if [ -z "${BASH_VERSION:-}" ]; then
    echo "Error: This installer must be run with Bash 4.0 or later." >&2
    echo "Please install Bash 4+ and run it with that interpreter." >&2
    exit 1
fi

BASH_MAJOR_VERSION="${BASH_VERSION%%.*}"
if [ "$BASH_MAJOR_VERSION" -lt 4 ]; then
    echo "Error: Bash 4.0 or later is required. Current version: $BASH_VERSION" >&2
    echo "Please install Bash 4+ and retry with that interpreter." >&2
    exit 1
fi

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Configuration
GITHUB_REPO="${SUB2API_GITHUB_REPO:-ziyue67/sub2api}"
RAW_BASE="${SUB2API_RAW_BASE:-https://raw.githubusercontent.com/${GITHUB_REPO}}"
INSTALL_DIR="${SUB2API_INSTALL_DIR:-/opt/sub2api}"
SERVICE_NAME="sub2api"
COMPOSE_FILE="${INSTALL_DIR}/docker-compose.yml"
ENV_FILE="${INSTALL_DIR}/.env"

# Published image (this fork publishes linux/amd64 only).
PRIMARY_IMAGE="${SUB2API_IMAGE:-ghcr.io/ziyue67/sub2api}"
FALLBACK_IMAGE="${SUB2API_IMAGE_FALLBACK:-ziyue67/sub2api}"
IMAGE_REPO="$PRIMARY_IMAGE"

# Optional Codex ticket exit pool. The kernel is installed on the host (systemd)
# and is independent of how the application itself is deployed.
MIHOMO_CODEX_SUBSCRIPTION_URL="${MIHOMO_CODEX_SUBSCRIPTION_URL:-}"
MIHOMO_CODEX_USER_AGENT="${MIHOMO_CODEX_USER_AGENT:-clash.meta}"
MIHOMO_CODEX_PORT="${MIHOMO_CODEX_PORT:-3101}"
MIHOMO_CODEX_SECRET="${MIHOMO_CODEX_SECRET:-}"

# Server configuration (will be set by user)
SERVER_HOST="0.0.0.0"
SERVER_PORT="8080"

# Language (default: zh = Chinese)
LANG_CHOICE="zh"

# ============================================================
# Language strings / 语言字符串
# ============================================================

declare -A MSG_ZH=(
    ["info"]="信息"
    ["success"]="成功"
    ["warning"]="警告"
    ["error"]="错误"
    ["select_lang"]="请选择语言 / Select language"
    ["lang_zh"]="中文"
    ["lang_en"]="English"
    ["enter_choice"]="请输入选项"
    ["install_title"]="Sub2API 安装程序（Docker）"
    ["server_config_title"]="服务器配置"
    ["server_config_desc"]="请配置对外监听地址与端口（Docker 端口映射）。"
    ["server_host_hint"]="提示：0.0.0.0 表示监听所有网卡，127.0.0.1 表示仅本机访问。"
    ["server_host_prompt"]="监听地址"
    ["server_port_hint"]="提示：默认 8080，请确保端口未被占用。"
    ["server_port_prompt"]="监听端口"
    ["invalid_port"]="端口无效，请输入 1-65535"
    ["server_config_summary"]="监听配置"
    ["run_as_root"]="请使用 root 权限运行（sudo）"
    ["arm64_unavailable"]="本 Fork 只发布 linux/amd64 镜像，当前架构为 arm64，无法安装"
    ["unsupported_arch"]="不支持的架构"
    ["unsupported_os"]="不支持的操作系统"
    ["detected_platform"]="检测到平台"
    ["missing_deps"]="缺少依赖"
    ["install_deps_first"]="请先安装缺失的依赖后重试"
    ["docker_missing"]="未检测到 Docker，请先安装 Docker Engine"
    ["compose_missing"]="未检测到 Docker Compose v2 插件（docker compose）"
    ["docker_install_hint"]="安装指引：https://docs.docker.com/engine/install/"
    ["fetching_version"]="正在获取最新版本..."
    ["failed_get_version"]="获取版本信息失败"
    ["latest_version"]="最新版本"
    ["fetching_versions"]="正在获取版本列表..."
    ["available_versions"]="可用版本"
    ["validating_version"]="正在校验版本"
    ["version_not_found"]="版本不存在"
    ["not_installed"]="未检测到已安装的 Sub2API"
    ["fresh_install_hint"]="请先执行全新安装"
    ["upgrading"]="正在升级..."
    ["current_version"]="当前版本"
    ["stopping_service"]="正在停止服务..."
    ["downloading_compose"]="正在下载 docker-compose.yml..."
    ["download_failed"]="下载失败"
    ["generating_secrets"]="正在生成安全密钥..."
    ["writing_compose"]="正在写入部署文件..."
    ["dirs_configured"]="目录已就绪"
    ["pulling_image"]="正在拉取镜像..."
    ["image_pulled"]="镜像已就绪"
    ["docker_pull_failed"]="镜像拉取失败"
    ["starting_service"]="正在启动服务..."
    ["service_started"]="服务已启动"
    ["service_start_failed"]="服务启动失败"
    ["upgrade_complete"]="升级完成"
    ["installing_version"]="正在安装指定版本"
    ["same_version"]="目标版本与当前版本相同，无需操作"
    ["install_version_complete"]="指定版本安装完成"
    ["install_complete"]="安装完成"
    ["install_dir"]="安装目录"
    ["step4_open_wizard"]="请打开浏览器完成初始化向导"
    ["wizard_guide"]="向导中请填写："
    ["wizard_db"]="数据库：容器内 postgres（已在 .env 中生成密码）"
    ["wizard_redis"]="Redis：容器内 redis"
    ["wizard_admin"]="管理员账号与密码"
    ["useful_commands"]="常用命令"
    ["cmd_status"]="查看状态"
    ["cmd_logs"]="查看日志"
    ["cmd_restart"]="重启服务"
    ["cmd_stop"]="停止服务"
    ["cmd_pull"]="更新到最新镜像"
    ["uninstall_confirm"]="即将卸载 Sub2API"
    ["are_you_sure"]="确认继续？(y/N) "
    ["uninstall_cancelled"]="已取消卸载"
    ["removing_files"]="正在移除容器与编排文件..."
    ["removing_install_dir"]="正在移除安装目录..."
    ["removing_config_dir"]="正在移除数据目录..."
    ["config_not_removed"]="数据目录已保留"
    ["remove_manually"]="如需彻底删除，请手动移除该目录"
    ["uninstall_complete"]="卸载完成"
    ["usage"]="用法"
    ["cmd_none"]="（默认）"
    ["cmd_install"]="安装最新版本"
    ["cmd_upgrade"]="升级到最新版本"
    ["cmd_install_version"]="安装/回滚到指定版本"
    ["cmd_list_versions"]="列出可用版本"
    ["cmd_uninstall"]="卸载"
    ["opt_version"]="指定版本，例如 v0.2.81"
    ["ready_for_setup"]="准备就绪，请在浏览器中完成初始化"
    ["getting_public_ip"]="正在获取公网 IP..."
    ["public_ip_failed"]="获取公网 IP 失败，请手动确认访问地址"
    ["mihomo_configuring"]="正在配置 Mihomo Codex 门票出口..."
    ["mihomo_skip"]="未配置 Mihomo Codex 出口（如需启用请设置 MIHOMO_CODEX_SUBSCRIPTION_URL）"
    ["mihomo_installer_missing"]="未找到 Mihomo 安装脚本，跳过"
    ["compose_missing_file"]="未找到部署文件，请先执行 install"
    ["port_hint"]="如果无法访问，请检查防火墙与端口占用"
    ["no_archives"]="本 Fork 不再提供二进制包，请勿使用旧版安装脚本的下载参数"
)

declare -A MSG_EN=(
    ["info"]="INFO"
    ["success"]="SUCCESS"
    ["warning"]="WARNING"
    ["error"]="ERROR"
    ["select_lang"]="Select language / 请选择语言"
    ["lang_zh"]="中文 (default)"
    ["lang_en"]="English"
    ["enter_choice"]="Enter choice"
    ["install_title"]="Sub2API Installer (Docker)"
    ["server_config_title"]="Server Configuration"
    ["server_config_desc"]="Configure the published address and port (Docker port mapping)."
    ["server_host_hint"]="Hint: 0.0.0.0 listens on all interfaces, 127.0.0.1 is local only."
    ["server_host_prompt"]="Bind address"
    ["server_port_hint"]="Hint: default 8080, make sure the port is free."
    ["server_port_prompt"]="Bind port"
    ["invalid_port"]="Invalid port, expected 1-65535"
    ["server_config_summary"]="Bind configuration"
    ["run_as_root"]="Please run as root (sudo)"
    ["arm64_unavailable"]="This fork publishes linux/amd64 images only; arm64 is not available"
    ["unsupported_arch"]="Unsupported architecture"
    ["unsupported_os"]="Unsupported operating system"
    ["detected_platform"]="Detected platform"
    ["missing_deps"]="Missing dependencies"
    ["install_deps_first"]="Please install the missing dependencies and retry"
    ["docker_missing"]="Docker was not found. Please install Docker Engine first"
    ["compose_missing"]="Docker Compose v2 plugin (docker compose) was not found"
    ["docker_install_hint"]="Install guide: https://docs.docker.com/engine/install/"
    ["fetching_version"]="Fetching the latest version..."
    ["failed_get_version"]="Failed to fetch version information"
    ["latest_version"]="Latest version"
    ["fetching_versions"]="Fetching release list..."
    ["available_versions"]="Available versions"
    ["validating_version"]="Validating version"
    ["version_not_found"]="Version not found"
    ["not_installed"]="No existing Sub2API installation detected"
    ["fresh_install_hint"]="Run a fresh install first"
    ["upgrading"]="Upgrading..."
    ["current_version"]="Current version"
    ["stopping_service"]="Stopping services..."
    ["downloading_compose"]="Downloading docker-compose.yml..."
    ["download_failed"]="Download failed"
    ["generating_secrets"]="Generating secure secrets..."
    ["writing_compose"]="Writing deployment files..."
    ["dirs_configured"]="Directories are ready"
    ["pulling_image"]="Pulling image..."
    ["image_pulled"]="Image is ready"
    ["docker_pull_failed"]="Failed to pull the image"
    ["starting_service"]="Starting services..."
    ["service_started"]="Services started"
    ["service_start_failed"]="Failed to start services"
    ["upgrade_complete"]="Upgrade complete"
    ["installing_version"]="Installing the requested version"
    ["same_version"]="Target version equals the current version, nothing to do"
    ["install_version_complete"]="Requested version installed"
    ["install_complete"]="Installation complete"
    ["install_dir"]="Install directory"
    ["step4_open_wizard"]="Open the setup wizard in your browser"
    ["wizard_guide"]="Fill in the wizard with:"
    ["wizard_db"]="Database: bundled postgres (password generated in .env)"
    ["wizard_redis"]="Redis: bundled redis"
    ["wizard_admin"]="Administrator account and password"
    ["useful_commands"]="Useful commands"
    ["cmd_status"]="Status"
    ["cmd_logs"]="Logs"
    ["cmd_restart"]="Restart"
    ["cmd_stop"]="Stop"
    ["cmd_pull"]="Update to the latest image"
    ["uninstall_confirm"]="Sub2API is about to be uninstalled"
    ["are_you_sure"]="Continue? (y/N) "
    ["uninstall_cancelled"]="Uninstall cancelled"
    ["removing_files"]="Removing containers and compose files..."
    ["removing_install_dir"]="Removing install directory..."
    ["removing_config_dir"]="Removing data directories..."
    ["config_not_removed"]="Data directories are preserved"
    ["remove_manually"]="Remove them manually to delete all data"
    ["uninstall_complete"]="Uninstall complete"
    ["usage"]="Usage"
    ["cmd_none"]="(default)"
    ["cmd_install"]="Install the latest version"
    ["cmd_upgrade"]="Upgrade to the latest version"
    ["cmd_install_version"]="Install/rollback to a specific version"
    ["cmd_list_versions"]="List available versions"
    ["cmd_uninstall"]="Uninstall"
    ["opt_version"]="Target version, e.g. v0.2.81"
    ["ready_for_setup"]="Ready. Finish the setup wizard in your browser"
    ["getting_public_ip"]="Resolving the public IP..."
    ["public_ip_failed"]="Could not resolve the public IP, please check the address manually"
    ["mihomo_configuring"]="Configuring the Mihomo Codex ticket sidecar..."
    ["mihomo_skip"]="Mihomo Codex sidecar not configured (set MIHOMO_CODEX_SUBSCRIPTION_URL to enable)"
    ["mihomo_installer_missing"]="Mihomo installer not found, skipping"
    ["compose_missing_file"]="Deployment files not found, run install first"
    ["port_hint"]="If the UI is unreachable, check the firewall and port usage"
    ["no_archives"]="This fork no longer ships binary archives; drop the old download flags"
)

msg() {
    local key="$1"
    if [ "$LANG_CHOICE" = "en" ]; then
        echo "${MSG_EN[$key]}"
    else
        echo "${MSG_ZH[$key]}"
    fi
}

# Print functions
print_info() {
    echo -e "${BLUE}[$(msg 'info')]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[$(msg 'success')]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[$(msg 'warning')]${NC} $1"
}

print_error() {
    echo -e "${RED}[$(msg 'error')]${NC} $1"
}

# Check if running interactively (can access terminal)
# When piped (curl | bash), stdin is not a terminal, but /dev/tty may still be available.
# Some container/CI environments expose a /dev/tty node that cannot be opened, so an
# actual open is required before we ever prompt.
is_interactive() {
    if [ ! -e /dev/tty ]; then
        return 1
    fi
    ( : < /dev/tty ) 2>/dev/null || return 1
    ( : > /dev/tty ) 2>/dev/null || return 1
    return 0
}

# Select language
select_language() {
    # If not interactive (piped), use default language
    if ! is_interactive; then
        LANG_CHOICE="zh"
        return
    fi

    echo ""
    echo -e "${CYAN}=============================================="
    echo "  $(msg 'select_lang')"
    echo "==============================================${NC}"
    echo ""
    echo "  1) $(msg 'lang_zh') (默认/default)"
    echo "  2) $(msg 'lang_en')"
    echo ""

    read -p "$(msg 'enter_choice'): " lang_input < /dev/tty

    case "$lang_input" in
        2|en|EN|english|English)
            LANG_CHOICE="en"
            ;;
        *)
            LANG_CHOICE="zh"
            ;;
    esac

    echo ""
}

# Validate port number
validate_port() {
    local port="$1"
    if [[ "$port" =~ ^[0-9]+$ ]] && [ "$port" -ge 1 ] && [ "$port" -le 65535 ]; then
        return 0
    fi
    return 1
}

# Configure server settings
configure_server() {
    # If not interactive (piped), use default settings
    if ! is_interactive; then
        print_info "$(msg 'server_config_summary'): ${SERVER_HOST}:${SERVER_PORT} (default)"
        return
    fi

    echo ""
    echo -e "${CYAN}=============================================="
    echo "  $(msg 'server_config_title')"
    echo "==============================================${NC}"
    echo ""
    echo -e "${BLUE}$(msg 'server_config_desc')${NC}"
    echo ""

    # Server host
    echo -e "${YELLOW}$(msg 'server_host_hint')${NC}"
    read -p "$(msg 'server_host_prompt') [${SERVER_HOST}]: " input_host < /dev/tty
    if [ -n "$input_host" ]; then
        SERVER_HOST="$input_host"
    fi

    echo ""

    # Server port
    echo -e "${YELLOW}$(msg 'server_port_hint')${NC}"
    while true; do
        read -p "$(msg 'server_port_prompt') [${SERVER_PORT}]: " input_port < /dev/tty
        if [ -z "$input_port" ]; then
            # Use default
            break
        elif validate_port "$input_port"; then
            SERVER_PORT="$input_port"
            break
        else
            print_error "$(msg 'invalid_port')"
        fi
    done

    echo ""
    print_info "$(msg 'server_config_summary'): ${SERVER_HOST}:${SERVER_PORT}"
    echo ""
}

# Check if running as root
check_root() {
    # Use 'id -u' instead of $EUID for better compatibility
    # $EUID may not work reliably when script is piped to bash
    if [ "$(id -u)" -ne 0 ]; then
        print_error "$(msg 'run_as_root')"
        exit 1
    fi
}

# Detect OS and architecture
detect_platform() {
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m)

    case "$ARCH" in
        x86_64)
            ARCH="amd64"
            ;;
        aarch64|arm64)
            # 本 Fork 的镜像只发布 linux/amd64（见 .goreleaser.yaml 的 goarch）。
            # 明确报错，避免走到 docker pull 才报一个难懂的 manifest 错误。
            print_error "$(msg 'arm64_unavailable')"
            exit 1
            ;;
        *)
            print_error "$(msg 'unsupported_arch'): $ARCH"
            exit 1
            ;;
    esac

    case "$OS" in
        linux)
            OS="linux"
            ;;
        *)
            print_error "$(msg 'unsupported_os'): $OS"
            exit 1
            ;;
    esac

    print_info "$(msg 'detected_platform'): ${OS}_${ARCH}"
}

# Docker Compose command (v2 plugin is required by this installer)
compose_cmd() {
    echo "docker compose"
}

# Check dependencies
check_dependencies() {
    local missing=()

    if ! command -v curl &> /dev/null; then
        missing+=("curl")
    fi

    if ! command -v docker &> /dev/null; then
        print_error "$(msg 'docker_missing')"
        print_info "$(msg 'docker_install_hint')"
        exit 1
    fi

    if ! docker compose version &> /dev/null; then
        print_error "$(msg 'compose_missing')"
        print_info "$(msg 'docker_install_hint')"
        exit 1
    fi

    if [ ${#missing[@]} -gt 0 ]; then
        print_error "$(msg 'missing_deps'): ${missing[*]}"
        print_info "$(msg 'install_deps_first')"
        exit 1
    fi
}

# Authenticate only GitHub REST API requests. Release metadata lookups must stay anonymous.
github_api_curl() {
    local arg
    local expect_value=false
    local url

    if [ "$#" -lt 1 ]; then
        echo "github_api_curl requires exactly one GitHub API URL" >&2
        return 2
    fi
    url="${!#}"

    # Keep authenticated invocations constrained to the options used below. In
    # particular, curl config, --url, and --next could add another destination.
    for arg in "${@:1:$#-1}"; do
        if [ "$expect_value" = true ]; then
            expect_value=false
            continue
        fi
        case "$arg" in
            -s|--silent)
                ;;
            --connect-timeout|--max-time|-o|--output|-w|--write-out)
                expect_value=true
                ;;
            *)
                echo "Unsafe github_api_curl argument: $arg" >&2
                return 2
                ;;
        esac
    done

    if [ "$expect_value" = true ] || [[ "$url" != https://api.github.com/* ]]; then
        echo "github_api_curl requires exactly one GitHub API URL" >&2
        return 2
    fi

    if [ -n "${UPDATE_GITHUB_TOKEN:-}" ]; then
        if [[ "$UPDATE_GITHUB_TOKEN" == *$'\n'* || "$UPDATE_GITHUB_TOKEN" == *$'\r'* || "$UPDATE_GITHUB_TOKEN" == *'"'* || "$UPDATE_GITHUB_TOKEN" == *'\'* ]]; then
            echo "UPDATE_GITHUB_TOKEN contains unsupported characters" >&2
            return 2
        fi
        printf 'header = "Authorization: Bearer %s"\n' "$UPDATE_GITHUB_TOKEN" | UPDATE_GITHUB_TOKEN= GITHUB_TOKEN= GH_TOKEN= curl -q --globoff --config - "$@"
    else
        UPDATE_GITHUB_TOKEN= GITHUB_TOKEN= GH_TOKEN= curl -q --globoff "$@"
    fi
}

# Get latest release version
get_latest_version() {
    print_info "$(msg 'fetching_version')"
    LATEST_VERSION=$(github_api_curl -s --connect-timeout 10 --max-time 30 "https://api.github.com/repos/${GITHUB_REPO}/releases/latest" 2>/dev/null | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')

    if [ -z "$LATEST_VERSION" ]; then
        print_error "$(msg 'failed_get_version')"
        print_info "Please check your network connection or try again later."
        exit 1
    fi

    print_info "$(msg 'latest_version'): $LATEST_VERSION"
}

# List available versions
list_versions() {
    print_info "$(msg 'fetching_versions')"

    local versions
    versions=$(github_api_curl -s --connect-timeout 10 --max-time 30 "https://api.github.com/repos/${GITHUB_REPO}/releases" 2>/dev/null | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/' | head -20)

    if [ -z "$versions" ]; then
        print_error "$(msg 'failed_get_version')"
        print_info "Please check your network connection or try again later."
        exit 1
    fi

    echo ""
    echo "$(msg 'available_versions'):"
    echo "----------------------------------------"
    echo "$versions" | while read -r version; do
        echo "  $version"
    done
    echo "----------------------------------------"
    echo ""
}

# Validate if a version exists
validate_version() {
    local version="$1"

    # Check for empty version
    if [ -z "$version" ]; then
        print_error "$(msg 'opt_version')" >&2
        exit 1
    fi

    # Ensure version starts with 'v'
    if [[ ! "$version" =~ ^v ]]; then
        version="v$version"
    fi

    print_info "$(msg 'validating_version') $version" >&2

    # Check if the release exists
    local http_code
    http_code=$(github_api_curl -s -o /dev/null -w "%{http_code}" --connect-timeout 10 --max-time 30 "https://api.github.com/repos/${GITHUB_REPO}/releases/tags/${version}" 2>/dev/null)

    # Check for network errors (empty or non-numeric response)
    if [ -z "$http_code" ] || ! [[ "$http_code" =~ ^[0-9]+$ ]]; then
        print_error "Network error: Failed to connect to GitHub API" >&2
        exit 1
    fi

    if [ "$http_code" != "200" ]; then
        print_error "$(msg 'version_not_found'): $version" >&2
        echo "" >&2
        list_versions >&2
        exit 1
    fi

    # Return the normalized version (to stdout)
    echo "$version"
}

# Pin the image tag inside the compose file
pin_image_tag() {
    local version="$1"
    local tag="${version#v}"

    if [ ! -f "$COMPOSE_FILE" ]; then
        print_error "$(msg 'compose_missing_file')"
        exit 1
    fi

    # The compose file ships `image: <repo>:latest`; rewrite it to the requested tag.
    sed -i -E "s|^([[:space:]]*image:[[:space:]]*)[^[:space:]]*sub2api:[^[:space:]]*|\1${IMAGE_REPO}:${tag}|" "$COMPOSE_FILE"

    if ! grep -q "${IMAGE_REPO}:${tag}" "$COMPOSE_FILE"; then
        # Fall back to the secondary registry when the default repository is not present.
        IMAGE_REPO="$FALLBACK_IMAGE"
        sed -i -E "s|^([[:space:]]*image:[[:space:]]*)[^[:space:]]*sub2api:[^[:space:]]*|\1${IMAGE_REPO}:${tag}|" "$COMPOSE_FILE"
    fi

    if [ ! -f "$ENV_FILE" ]; then
        : > "$ENV_FILE"
    fi
    if grep -q '^SUB2API_VERSION=' "$ENV_FILE"; then
        sed -i -E "s|^SUB2API_VERSION=.*|SUB2API_VERSION=${version}|" "$ENV_FILE"
    else
        printf 'SUB2API_VERSION=%s\n' "$version" >> "$ENV_FILE"
    fi
}

# Get current installed version
get_current_version() {
    if [ -f "$ENV_FILE" ]; then
        local pinned
        pinned=$(grep -E '^SUB2API_VERSION=' "$ENV_FILE" | head -1 | cut -d= -f2-)
        if [ -n "$pinned" ]; then
            echo "$pinned"
            return 0
        fi
    fi

    if [ -f "$COMPOSE_FILE" ]; then
        local from_compose
        from_compose=$(grep -E '^[[:space:]]*image:.*sub2api:' "$COMPOSE_FILE" | head -1 | sed -E 's|.*sub2api:([^[:space:]]+).*|\1|')
        if [ -n "$from_compose" ] && [ "$from_compose" != "latest" ]; then
            echo "v${from_compose#v}"
            return 0
        fi
    fi

    echo "not_installed"
}

# Generate a random secret
generate_secret() {
    if command -v openssl &> /dev/null; then
        openssl rand -hex 32
    else
        head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'
    fi
}

# Download the compose file for the requested release
download_compose() {
    local version="$1"
    local url="${RAW_BASE}/${version}/deploy/docker-compose.local.yml"

    print_info "$(msg 'downloading_compose')"
    if ! curl -fsSL "$url" -o "$COMPOSE_FILE"; then
        print_error "$(msg 'download_failed'): $url"
        exit 1
    fi
}

# Setup directories, compose file and .env
setup_directories() {
    local version="$1"

    print_info "$(msg 'writing_compose')"

    mkdir -p "$INSTALL_DIR"
    mkdir -p "$INSTALL_DIR/data" "$INSTALL_DIR/postgres_data" "$INSTALL_DIR/redis_data"

    download_compose "$version"
    pin_image_tag "$version"

    if [ ! -f "$ENV_FILE" ]; then
        print_info "$(msg 'generating_secrets')"
        local jwt_secret totp_key pg_password
        jwt_secret=$(generate_secret)
        totp_key=$(generate_secret)
        pg_password=$(generate_secret)

        cat > "$ENV_FILE" << EOF
# Sub2API Docker deployment - generated by deploy/install.sh
SERVER_PORT=${SERVER_PORT}
BIND_HOST=${SERVER_HOST}
POSTGRES_USER=sub2api
POSTGRES_PASSWORD=${pg_password}
POSTGRES_DB=sub2api
REDIS_PASSWORD=
JWT_SECRET=${jwt_secret}
TOTP_ENCRYPTION_KEY=${totp_key}
SUB2API_VERSION=${version}
EOF
        chmod 600 "$ENV_FILE"
    else
        # Keep user edits, only sync the published port/host and pinned version.
        if grep -q '^SERVER_PORT=' "$ENV_FILE"; then
            sed -i -E "s|^SERVER_PORT=.*|SERVER_PORT=${SERVER_PORT}|" "$ENV_FILE"
        else
            printf 'SERVER_PORT=%s\n' "$SERVER_PORT" >> "$ENV_FILE"
        fi
        if grep -q '^BIND_HOST=' "$ENV_FILE"; then
            sed -i -E "s|^BIND_HOST=.*|BIND_HOST=${SERVER_HOST}|" "$ENV_FILE"
        else
            printf 'BIND_HOST=%s\n' "$SERVER_HOST" >> "$ENV_FILE"
        fi
    fi

    print_success "$(msg 'dirs_configured')"
}

# Pull the pinned image
pull_image() {
    local version="$1"
    local tag="${version#v}"

    print_info "$(msg 'pulling_image') ${IMAGE_REPO}:${tag}"
    if ! docker pull "${IMAGE_REPO}:${tag}"; then
        if [ "$IMAGE_REPO" != "$FALLBACK_IMAGE" ]; then
            print_warning "$(msg 'docker_pull_failed'): ${IMAGE_REPO}:${tag}"
            IMAGE_REPO="$FALLBACK_IMAGE"
            pin_image_tag "$version"
            if ! docker pull "${IMAGE_REPO}:${tag}"; then
                print_error "$(msg 'docker_pull_failed'): ${IMAGE_REPO}:${tag}"
                exit 1
            fi
        else
            print_error "$(msg 'docker_pull_failed'): ${IMAGE_REPO}:${tag}"
            exit 1
        fi
    fi
    print_success "$(msg 'image_pulled')"
}

# Install or reuse the optional Mihomo sidecar used only for Codex ticket
# harvesting. The kernel runs on the host; the application container is not
# affected by it.
configure_mihomo_codex() {
    local installer="$INSTALL_DIR/install-mihomo-codex.sh"

    if [ -f "$INSTALL_DIR/migrate-mihomo-managed.sh" ] && [ -f /etc/mihomo-codex/config.yaml ]; then
        INSTALL_DIR="$INSTALL_DIR" bash "$INSTALL_DIR/migrate-mihomo-managed.sh"
        # The managed kernel now owns this configuration. Do not start another
        # systemd instance on the same ports during an application upgrade.
        if [ -f "${DATA_DIR:-$INSTALL_DIR/data}/mihomo-codex/settings.json" ]; then
            return 0
        fi
    fi

    if [ -z "$MIHOMO_CODEX_SUBSCRIPTION_URL" ]; then
        if systemctl is-active --quiet mihomo-codex.service 2>/dev/null; then
            print_info "Mihomo Codex sidecar is already active on 127.0.0.1:${MIHOMO_CODEX_PORT}"
        else
            print_info "$(msg 'mihomo_skip')"
        fi
        return 0
    fi

    if [ ! -f "$installer" ]; then
        local version
        version=$(get_current_version)
        curl -fsSL "${RAW_BASE}/${version}/deploy/install-mihomo-codex.sh" -o "$installer" 2>/dev/null || true
        curl -fsSL "${RAW_BASE}/${version}/deploy/migrate-mihomo-managed.sh" -o "$INSTALL_DIR/migrate-mihomo-managed.sh" 2>/dev/null || true
    fi

    if [ ! -f "$installer" ]; then
        print_warning "$(msg 'mihomo_installer_missing')"
        return 0
    fi

    print_info "$(msg 'mihomo_configuring')"
    MIHOMO_CODEX_SUBSCRIPTION_URL="$MIHOMO_CODEX_SUBSCRIPTION_URL" \
        MIHOMO_CODEX_USER_AGENT="$MIHOMO_CODEX_USER_AGENT" \
        MIHOMO_CODEX_PORT="$MIHOMO_CODEX_PORT" \
        MIHOMO_CODEX_SECRET="$MIHOMO_CODEX_SECRET" \
        bash "$installer"
    if [ -f "$INSTALL_DIR/migrate-mihomo-managed.sh" ]; then
        INSTALL_DIR="$INSTALL_DIR" bash "$INSTALL_DIR/migrate-mihomo-managed.sh"
    fi
}

# Start (or recreate) the compose stack
start_service() {
    print_info "$(msg 'starting_service')"

    if (cd "$INSTALL_DIR" && $(compose_cmd) up -d); then
        print_success "$(msg 'service_started')"
        return 0
    fi

    print_error "$(msg 'service_start_failed')"
    print_info "cd $INSTALL_DIR && $(compose_cmd) logs --tail 50"
    return 1
}

# Prepare for setup wizard (no config file needed - setup wizard will create it)
prepare_for_setup() {
    print_success "$(msg 'ready_for_setup')"
}

# Get public IP address
get_public_ip() {
    print_info "$(msg 'getting_public_ip')"

    # Try to get public IP from ipinfo.io
    local response
    response=$(curl -s --connect-timeout 5 --max-time 10 "https://ipinfo.io/json" 2>/dev/null)

    if [ -n "$response" ]; then
        # Extract IP from JSON response using grep and sed (no jq dependency)
        PUBLIC_IP=$(echo "$response" | grep -o '"ip": *"[^"]*"' | sed 's/"ip": *"\([^"]*\)"/\1/')
        if [ -n "$PUBLIC_IP" ]; then
            print_success "Public IP: $PUBLIC_IP"
            return 0
        fi
    fi

    # Fallback to local IP
    print_warning "$(msg 'public_ip_failed')"
    PUBLIC_IP=$(hostname -I 2>/dev/null | awk '{print $1}' || echo "YOUR_SERVER_IP")
    return 1
}

# Print completion message
print_completion() {
    local display_host="${PUBLIC_IP:-YOUR_SERVER_IP}"
    if [ "$SERVER_HOST" = "127.0.0.1" ]; then
        display_host="127.0.0.1"
    fi

    echo ""
    echo "=============================================="
    print_success "$(msg 'install_complete')"
    echo "=============================================="
    echo ""
    echo "$(msg 'install_dir'): $INSTALL_DIR"
    echo "$(msg 'server_config_summary'): ${SERVER_HOST}:${SERVER_PORT}"
    echo "$(msg 'current_version'): $(get_current_version)"
    echo ""
    echo "=============================================="
    echo "  $(msg 'step4_open_wizard')"
    echo "=============================================="
    echo ""
    print_info "     http://${display_host}:${SERVER_PORT}"
    echo ""
    echo "     $(msg 'wizard_guide')"
    echo "     - $(msg 'wizard_db')"
    echo "     - $(msg 'wizard_redis')"
    echo "     - $(msg 'wizard_admin')"
    echo ""
    echo "=============================================="
    echo "  $(msg 'useful_commands')"
    echo "=============================================="
    echo ""
    echo "  $(msg 'cmd_status'):   cd $INSTALL_DIR && $(compose_cmd) ps"
    echo "  $(msg 'cmd_logs'):     cd $INSTALL_DIR && $(compose_cmd) logs -f sub2api"
    echo "  $(msg 'cmd_restart'):  cd $INSTALL_DIR && $(compose_cmd) restart sub2api"
    echo "  $(msg 'cmd_stop'):     cd $INSTALL_DIR && $(compose_cmd) down"
    echo "  $(msg 'cmd_pull'):     $0 upgrade"
    echo ""
    echo "  $(msg 'port_hint')"
    echo ""
    echo "=============================================="
}

# Install a full stack (compose + env) for a given version
install_stack() {
    local version="$1"

    setup_directories "$version"
    pull_image "$version"
    configure_mihomo_codex
    prepare_for_setup
    get_public_ip || true
    start_service
    print_completion
}

# Upgrade function
upgrade() {
    if [ ! -f "$COMPOSE_FILE" ]; then
        print_error "$(msg 'not_installed')"
        print_info "$(msg 'fresh_install_hint'): $0 install"
        exit 1
    fi

    print_info "$(msg 'upgrading')"
    print_info "$(msg 'current_version'): $(get_current_version)"

    get_latest_version
    install_version "$LATEST_VERSION"
}

# Install specific version (for upgrade or rollback)
install_version() {
    local target_version="$1"

    # Validate and normalize version
    target_version=$(validate_version "$target_version")

    if [ ! -f "$COMPOSE_FILE" ]; then
        print_error "$(msg 'not_installed')"
        print_info "$(msg 'fresh_install_hint'): $0 install -v $target_version"
        exit 1
    fi

    print_info "$(msg 'installing_version'): $target_version"

    local current_version
    current_version=$(get_current_version)
    print_info "$(msg 'current_version'): $current_version"

    if [ "$current_version" = "$target_version" ] || [ "$current_version" = "${target_version#v}" ]; then
        print_warning "$(msg 'same_version')"
        exit 0
    fi

    pin_image_tag "$target_version"
    pull_image "$target_version"
    configure_mihomo_codex
    start_service

    echo ""
    echo "=============================================="
    print_success "$(msg 'install_version_complete')"
    echo "=============================================="
    echo ""
    echo "  $(msg 'current_version'): $target_version"
    echo ""
}

# Uninstall function
uninstall() {
    print_warning "$(msg 'uninstall_confirm')"

    # If not interactive (piped), require -y flag or skip confirmation
    if ! is_interactive; then
        if [ "${FORCE_YES:-}" != "true" ]; then
            print_error "Non-interactive mode detected. Use 'curl ... | bash -s -- uninstall -y' to confirm."
            exit 1
        fi
    else
        read -p "$(msg 'are_you_sure') " -n 1 -r < /dev/tty
        echo
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            print_info "$(msg 'uninstall_cancelled')"
            exit 0
        fi
    fi

    if [ -f "$COMPOSE_FILE" ]; then
        print_info "$(msg 'removing_files')"
        (cd "$INSTALL_DIR" && $(compose_cmd) down) || true
    fi

    # Preserve data unless --purge was requested.
    if [ "${PURGE:-}" = "true" ]; then
        print_info "$(msg 'removing_config_dir')"
        rm -rf "$INSTALL_DIR"
    else
        print_info "$(msg 'removing_install_dir')"
        rm -f "$COMPOSE_FILE"
        print_warning "$(msg 'config_not_removed'): $INSTALL_DIR/data, $INSTALL_DIR/postgres_data, $INSTALL_DIR/redis_data"
        print_warning "$(msg 'remove_manually')"
    fi

    print_success "$(msg 'uninstall_complete')"
}

# Main
main() {
    # Parse flags first
    local target_version=""
    local positional_args=()

    while [[ $# -gt 0 ]]; do
        case "$1" in
            -y|--yes)
                FORCE_YES="true"
                shift
                ;;
            --purge)
                PURGE="true"
                shift
                ;;
            -v|--version)
                if [ -n "${2:-}" ] && [[ ! "$2" =~ ^- ]]; then
                    target_version="$2"
                    shift 2
                else
                    echo "Error: --version requires a version argument"
                    exit 1
                fi
                ;;
            --version=*)
                target_version="${1#*=}"
                if [ -z "$target_version" ]; then
                    echo "Error: --version requires a version argument"
                    exit 1
                fi
                shift
                ;;
            *)
                positional_args+=("$1")
                shift
                ;;
        esac
    done

    # Restore positional arguments
    set -- "${positional_args[@]}"

    # Select language first
    select_language

    echo ""
    echo "=============================================="
    echo "       $(msg 'install_title')"
    echo "=============================================="
    echo ""

    # Parse commands
    case "${1:-}" in
        upgrade|update)
            check_root
            detect_platform
            check_dependencies
            if [ -n "$target_version" ]; then
                # Upgrade to specific version
                install_version "$target_version"
            else
                # Upgrade to latest
                upgrade
            fi
            exit 0
            ;;
        install)
            # Install with optional version
            check_root
            detect_platform
            check_dependencies
            if [ -n "$target_version" ]; then
                # Install specific version (fresh install or version change)
                if [ -f "$COMPOSE_FILE" ]; then
                    install_version "$target_version"
                else
                    configure_server
                    local requested_version
                    requested_version=$(validate_version "$target_version")
                    install_stack "$requested_version"
                fi
            else
                # Fresh install with latest version
                configure_server
                get_latest_version
                install_stack "$LATEST_VERSION"
            fi
            exit 0
            ;;
        rollback)
            # Rollback to a specific version (alias for install with version)
            if [ -z "$target_version" ] && [ -n "${2:-}" ]; then
                target_version="$2"
            fi
            if [ -z "$target_version" ]; then
                print_error "$(msg 'opt_version')"
                echo ""
                echo "Usage: $0 rollback -v <version>"
                echo "       $0 rollback <version>"
                echo ""
                list_versions
                exit 1
            fi
            check_root
            detect_platform
            check_dependencies
            install_version "$target_version"
            exit 0
            ;;
        list-versions|versions)
            list_versions
            exit 0
            ;;
        uninstall|remove)
            check_root
            uninstall
            exit 0
            ;;
        --help|-h)
            echo "$(msg 'usage'): $0 [command] [options]"
            echo ""
            echo "Commands:"
            echo "  $(msg 'cmd_none')            $(msg 'cmd_install')"
            echo "  install              $(msg 'cmd_install')"
            echo "  upgrade              $(msg 'cmd_upgrade')"
            echo "  rollback <version>   $(msg 'cmd_install_version')"
            echo "  list-versions        $(msg 'cmd_list_versions')"
            echo "  uninstall            $(msg 'cmd_uninstall')"
            echo ""
            echo "Options:"
            echo "  -v, --version <ver>  $(msg 'opt_version')"
            echo "  -y, --yes            Skip confirmation prompts (for uninstall)"
            echo "  --purge              Also remove data directories on uninstall"
            echo ""
            echo "Examples:"
            echo "  $0                        # Install the latest version"
            echo "  $0 install -v v0.2.81     # Install a specific version"
            echo "  $0 upgrade                # Upgrade to the latest version"
            echo "  $0 rollback v0.2.80       # Roll back to v0.2.80"
            echo "  $0 list-versions          # List available versions"
            echo ""
            exit 0
            ;;
    esac

    # Default: Fresh install with latest version
    check_root
    detect_platform
    check_dependencies

    if [ -n "$target_version" ]; then
        # Install specific version
        if [ -f "$COMPOSE_FILE" ]; then
            install_version "$target_version"
        else
            configure_server
            install_stack "$(validate_version "$target_version")"
        fi
    else
        # Install latest version
        configure_server
        get_latest_version
        install_stack "$LATEST_VERSION"
    fi
}

main "$@"
