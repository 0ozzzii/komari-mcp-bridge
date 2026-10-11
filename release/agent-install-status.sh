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
