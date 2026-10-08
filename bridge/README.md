# Komari 常驻 MCP 桥接

目标：把 Komari 已有终端、文件接口提供给 AI 客户端。持续连接在常驻 daemon 内，不依赖浏览器操作，不安装 SSH，不新增被控端入站端口。MCP 是工具适配层，工作会话独立于 MCP transport。2026-10-06 已完成本地实现、稳定性补充与隔离源码链路测试，尚未接入用户面板或生产节点。

[中文架构图与职责说明](./ARCHITECTURE.md) · [执行策略与资源保护](./EXECUTION_POLICY.md)

## 构成与源码版本

| 部分 | 实现与依据 |
| --- | --- |
| MCP daemon | 本目录独立 Go module；官方 MCP Go SDK v1.8.0，Gorilla WebSocket v1.5.3，Go 1.25.0 |
| 原有面板能力 | Komari Server 固定上游基线（当前以 `release/config.json` 为准）；复用管理员终端、RPC2、原生文件传输 |
| 面板增量 | `internal/mcpbridge/` 新增管理页面、私有管理代理及可选同域名 `/mcp` 转发；路由注册及 `/mcp` 身份分离；管理沿用官方管理员认证与 `RequireSensitive2FA()` |
| 原后台侧栏入口 | `web-patches/` 在账户下面增加一级 MCP，路由 `/admin/mcp`，保留原后台布局；旧 `panel-mcp-menu/` 插件仅作兼容备选 |
| 探针修复 | `agent-patches/`，基线 main SHA `828afafe7fb5b1b58c1232094e1e46320719016e`，容器统计、长连接和共享槽位／命令期限增量；MCP 工具实现仍在桥接 |
| 官方前端构建 | `0321789bc1989e53df729dfc98bed2a2800c39c6`，加 `web-patches/` 的一级 MCP 菜单补丁后构建真实内嵌资源 |

用户实际面板、探针、代理和 AI 产品版本仍未知。较旧版本可能没有原生文件 API，历史 API Key 终端修复须包含 #482（1.1.9 起）；不能根据本仓库 SHA 宣称生产已兼容。

## 15 个合并工具

| 类别 | 工具 |
| --- | --- |
| 节点与能力 | `komari_nodes_list`, `komari_capabilities`, `komari_execution_policy` |
| 工作会话 | `komari_session_open`, `komari_session_status`, `komari_sessions_list`, `komari_session_close` |
| 命令与日志 | `komari_command_run`, `komari_output_read` |
| 交互与控制 | `komari_terminal_input`, `komari_command_interrupt`, `komari_terminal_resize` |
| 文件 | `komari_filesystem`（list/roots/stat/search/mkdir/delete/move/copy/chmod/write），`komari_file_read` |
| 状态查询 | `komari_host_inspect`（system/processes/capabilities），在指定工作会话中执行固定常见命令 |

这是按当前能力合并后的工具集，没有声称逐个移植 RHMCP 的 65 个工具。RHMCP 的本地文件工具不能直接在桥接主机执行来假装操作被控节点。Agent 编排、浏览器、专门服务管理、持久作业、完整 TUI 和强制进程组取消不在本版范围。

文件工具复用 `admin:file*` RPC 和 `/api/admin/client/:uuid/file/download|upload`；write 最多 128 KiB，通过 init/chunk/merge 上传，read 最多 128 KiB/次，基于 byte offset 返回原始 base64。文件搜索提供 query/content。远程实际权限和路径边界由 Komari Agent 的用户/命名空间决定。terminal 权限本身允许任意 shell 命令，**file.write 禁用并不能阻止获授 terminal 权限的调用方用 shell 写文件**。

## 运行和客户端配置

云环境准备：仓库根目录执行 `bash dev/setup.sh`。它验证官方 Go 1.25.0 下载 SHA256、保留 TLS/模块校验、构建真实官方前端资源、构建桥接并执行相关测试；不安装/启动生产服务。已有开发 checkout 的修改不会被覆盖。以后工作使用 `/workspace/.tools/komari-go1.25.0/go/bin/go`，缓存 `GOPATH=/workspace/.cache/go GOCACHE=/workspace/.cache/go-build`。

普通开发机器：安装 Go 1.25.0 后在 bridge 中执行：

```bash
go mod download
go test ./... -count=1
go build -trimpath -ldflags='-s -w' -o bin/komari-mcp ./cmd/komari-mcp
```

配置参考 `deploy/example.env`；真实密钥通过运行环境安全注入，实际配置文件 0600、不入 Git。执行 `bin/komari-mcp serve`。默认 MCP 监听 `127.0.0.1:8967/mcp`，管理监听 **独立** `127.0.0.1:8968/control/...`。管理员 API Key 和控制密钥不进入普通工具参数。`GET /healthz` 只表示本地服务就绪，明确返回 `upstream_verified:false`，不是远程终端已验收。

服务初始为停用状态，创建独立 MCP 密钥并启用后才允许工具调用。每个密钥保存 SHA256 哈希、节点集合、权限、有效期和启停状态；明文只在创建响应显示一次。逻辑会话只能由该密钥的身份操作，不依赖猜不到 session_id。

Komari 管理员 API Key 仍具有原生全局管理权限；节点授权由桥接层实施，不会把该原生 key 改造成节点范围凭据。使用 CF-MCP-HUB 中继时，每个独立调用方/路由应使用不同桥接 key；旧 Hub 的共享上游凭据不会自动携带下游调用方身份。15 个工具低于已查明的 Hub 单路由32工具限制。

如果采用面板管理增量，在 **面板进程** 环境中设置 `KOMARI_MCP_CONTROL_URL` 与 `KOMARI_MCP_CONTROL_TOKEN`，后者与 daemon 的控制密钥相同。管理员点击原后台“账户”下面的 **MCP**（`/admin/mcp`），在右侧管理开关、多 Key、节点和工具授权。前端用 `web-patches/` 锁定的微小补丁集成；底层管理页为 `/api/admin/mcp`。敏感 POST 使用原 `RequireSensitive2FA()`：开启 2FA 的 Cookie 管理员填写验证码，未开启者沿用原规则，管理员 API Key 沿用官方豁免。管理代理不向 daemon 转发 Cookie 或用户 Authorization。旧 `panel-mcp-menu/` 只会进入“插件”子菜单，不满足本次一级菜单要求，无需再安装。没有修改已有管理员身份或 2FA 设置。

创建流程：启用 MCP → 填名称 → 全选当前节点或单独勾选 → 选择能力 → 创建密钥。列表来自面板全部登记节点（包括离线节点），每个 Key 最多128个；“全选”保存的是当前选择，新节点不会自动获得权限。已有 Key 可以重新选择节点并保存授权，或独立停用。

创建成功弹出接入窗口，默认提供一条完整 URL：

```text
https://komari.example.com/mcp/<本次生成的kmb_密钥>
```

支持自定义远程 Streamable HTTP MCP 的客户端可以把它作为服务地址，不再另外提供 Bearer 头。这个链接包含凭据，按密码保存；窗口关闭后清除明文，服务端仍只保存哈希，无法再次找回原 Key。窗口折叠区保留标准 `/mcp` 地址与独立 Bearer Key，供要求分开配置的客户端使用。复制按钮在浏览器不允许剪贴板访问时提示手动复制。

URL 鉴权只在面板公开 MCP 入口提供：`/mcp/<key>`（默认）和 `/mcp?key=<key>`（兼容）转为内部 `/mcp` + Bearer。daemon 直接监听的 `/mcp` 仍使用 Bearer；私有控制入口不接受 URL key。请求中多个 key、重复 Authorization 或相互冲突的凭据会被拒绝。面板访问/异常日志脱敏 `/mcp/…` 且不记录查询串，上游 URL 不携带 key；外部代理、Cloudflare 和客户端自身的 URL 记录规则仍须另行核对。

URL 负责地址和认证，不代替 MCP 协议。能够运行 curl 的 Agent 可向该 URL 发送 MCP JSON-RPC POST（`Content-Type: application/json`、`Accept: application/json, text/event-stream`），完成 initialize 后 tools/list、tools/call；单纯浏览器 GET 不会执行命令。ChatGPT、自定义 Claude 客户端和 Codex 网页等产品是否允许添加远程 MCP、需要怎样的配置及网络策略，均需在目标产品验证；一条 URL 不能让没有此接入能力的聊天页面自动获得工具。本地实际官方 Go SDK 已验证 URL-only initialize/tools/list 和授权变更。

不使用面板增量时，可通过私有管理 API `GET /control/keys|nodes|status`、`POST /control/enabled`、`POST /control/keys/create|update` 配置。它们只接受独立控制凭据，拒绝普通 MCP key；不把控制监听器开放到公网。

Codex CLI 的当前环境版本 0.159.0-alpha.3 及实际读取的 `mcp_cmd.rs` 支持 URL 与 bearer-token-env-var；**没有更改用户配置**。未改动用户配置；可按已读取的当前 CLI 格式配置一次。面板提供完整 URL 时不需要 bearer-token-env-var：

```bash
codex mcp add komari --url "<创建弹窗中的完整URL>"
```

标准地址与独立 Bearer 的兼容写法：

```bash
codex mcp add komari --url <reachable-mcp-url>/mcp --bearer-token-env-var KOMARI_MCP_CALLER_KEY
```

`KOMARI_MCP_CALLER_KEY` 是新建的范围密钥，不是 Komari 管理员 API Key。也可用对应配置概念：

```toml
[mcp_servers.komari]
url = "https://<reachable-mcp-host>/mcp"
bearer_token_env_var = "KOMARI_MCP_CALLER_KEY"
```

只支持本地 stdio MCP 的客户端也可使用同一二进制：启动命令 `komari-mcp stdio`，通过安全环境注入 `BRIDGE_MCP_URL` 和 `KOMARI_MCP_CALLER_KEY`。stdio 进程只做协议适配；实际持有远端终端的仍是独立 `serve` daemon。已用官方 SDK 验证退出并重启 stdio 适配进程后，原 shell 的 cd/export 仍然保留。

本地 Codex 可访问 loopback；远程客户端需要支持自定义远程 MCP 和可达 HTTPS 地址；使用标准 `/mcp` 地址时还需 Bearer 头，使用面板完整 URL 时由面板转换认证。普通受限制云聊天产品不一定支持这些配置。本会话未动态安装自定义 MCP 工具，不宣称所有产品已经能调用。通过已有反向代理转发 MCP listener 就能沿用域名，无需默认新增公网端口；代理应保留 Authorization，管理员终端路径支持 WebSocket Upgrade。未修改任何线上代理配置。

官方 MCP Go SDK v1.8.0 默认保护 loopback listener 的 Host，来自 loopback 连接但携带公网 Host 会被拒绝403。反向代理到该 listener 时将上游 Host 设为正确 loopback 地址；示例在 `deploy/nginx-mcp-location.conf`，保留默认保护，不关闭认证或 DNS 重绑定保护。具体代理空闲期限仍以目标配置为准。

**单域名部署（2026-10-07）**：在面板进程配置 `KOMARI_MCP_UPSTREAM_URL=http://127.0.0.1:8967/mcp`。外部全部请求通过一条 Cloudflare Tunnel 路由进入面板，例如 `komari.example.com → HTTP 127.0.0.1:18080`，路径留空。面板 `/`、探针 `/api/clients/...` 和 MCP `/mcp` 共用域名；只有公开 MCP 路由 `/mcp` 与 `/mcp/<key>` 在面板内部转给桥接。CF 保留面板公共 Host，内部 MCP Host 由转发代码重写，不需要 CF 配置第二个域名或路由。

`/mcp` 的授权只由桥接范围 Key 决定：公开转发不注入管理员 Key 或控制 Token，剥离 Cookie/2FA 头，并保留 MCP 协议头、查询参数和增量响应。面板身份中间件在已注册 MCP 路由不解析原探针 Token 或读取整个正文；请求上限 256 KiB。不配置上游时带调用方凭据请求返回503；缺少凭据返回401。只转发公开 MCP 路由，不转发私有 `/control/...`。携带 Origin 的请求限定同源；非浏览器客户端可不带 Origin。公网 MCP/探针不能被整站交互式 Access 登录阻断，具体路径策略与认证头需在目标环境验证。

通用 Windows 部署说明见 [Windows 部署指南](../release/windows-deployment-guide.md)，使用本次交付的固定提交替换占位符。当前单域名转发、真实 SDK initialize/tools/list、范围 Key 拒绝与流式首段响应已通过本地 Linux 测试；Windows 运行、CF 和真实探针接入尚未实测。停用 MCP 总开关拒绝工具执行，协议初始化/工具列表仍可读取；撤销 Key 才会拒绝该身份的协议请求。

部署参考 systemd/Compose 文件。bridge 用户不需 root、privileged 或宿主机挂载；控制端口仅 loopback/受控容器内部网络。Compose 的 host publication 也限于 loopback。**Docker 和 systemd 文件是待目标环境验证的示例，不声称已经启动验证。** 当前验证的是实际 Go 二进制与本地隔离测试。

## 执行语义与状态

首次建立管理员 WebSocket →取得 request_id →通知探针/等待回连 →按 PATH 解析的受控 `sh -i` 发出 READY 标记。首次 bootstrap 切到可控 Linux POSIX shell 并关闭回显；后续每条命令 dot-source 进相同父 shell，保留 cd/export/函数。不为每条命令运行 bash -lc。

Windows PowerShell 适配见 [PR #5](https://github.com/0ozzzii/komari-mcp-bridge-dev/pull/5)，v1.0.1 及更早版本不包含该修复。节点 OS 与匹配探针的 OS/shell/marker_protocol ACK 用于选择执行方式；只有 PowerShell 会话采用该路径，cmd-only 返回 unsupported_shell。受管理 Windows 探针优先使用已安装的 `pwsh.exe`（PowerShell 7）；服务 PATH 不包含它时还检查 `%ProgramFiles%\PowerShell\7\pwsh.exe`，缺少时回退 `powershell.exe`。不自动安装 Shell，均不可用时拒绝受管理会话；人工网页终端保持原选择方式。受管理会话使用 NoProfile 并关闭本会话 PSReadLine，不修改 Profile、系统环境或服务配置。状态、执行返回和诊断日志的 `shell_executable` 来自探针 ACK，显示实际选择，而不是只根据节点 OS 猜测。Base64 分块脚本以 dot-source ScriptBlock 在相同父作用域执行，再用 Out-Default 排空对象格式化输出；没有每条命令的新 PowerShell，也不修改系统 ExecutionPolicy。受限 LanguageMode 仍可能拒绝执行。

较新 Windows CI 的 ConPTY 会删除 POSIX 的 RS/US 控制字节；用户提供的 Windows 测试节点 Build17763 诊断报告则显示保留，不能将任一种结果推广至所有 Windows 版本。窄终端还可能插入折行/光标序列。Windows 因此使用运行时生成的可打印帧 `~KMB:nonce:type:data~`，避免输入回显包含完整帧前缀；标记单独占行，受管理终端最少256列。新的桥接和 Windows 探针必须配套，探针也按此格式解除已完成命令的期限。Linux 保留原 RS/US 帧。随机标记不构成抵御恶意命令的安全边界。

PowerShell 每条命令先清空 LASTEXITCODE；若本条执行过原生命令，返回最后一个原生命令的有符号32位退出码，否则按最后语句成功状态返回0或1，终止异常返回1。与 POSIX 管道语义不同：先运行非零原生命令后打印成功文本，仍可能返回该原生退出码。普通变量、函数、环境和目录保留；exit、修改保留内部变量或终端模式、长时间后台输出等仍可造成结果不确定。恢复以 nonce 与父 PID 检查，而非只看 request_id。

独立 ConPTY 测试分别启动 Windows PowerShell 5.1 与已安装的 PowerShell 7，覆盖流式输出、上下文、退出码7/3010、随后纯PowerShell成功、throw/Write-Error、多行中文、进程输出和恢复检查；这不等于 Windows 测试节点 Build17763 的实际面板—探针—MCP链路已验收。[Windows 测试节点只读探测提示词](../release/windows-terminal-probe-prompt.txt) 用于补充该环境证据；[跨平台兼容记录](../release/platform-terminal-notes.md) 区分源码、本地模拟、Windows CI 和现场报告。

官方 Unix 探针先按 HOME 查 `/etc/passwd` 中的 shell，再回退 zsh/bash/sh；本版 bootstrap 要求其初始 shell 接受 POSIX 形式的启动语句，且 PATH 中的 `sh` 可用（缺少时尝试 `/system/bin/sh`）。特殊初始 shell 仍需目标验证，不承诺任意 shell。命令的 started_at/first_output_at/ended_at 是桥接收到相应事件的时间，不是远端精确时钟时间。

POSIX 初始化按 `$TMPDIR` → `/data/local/tmp` → `/tmp` → `$HOME` → 初始工作目录选择可用基目录，以实际 `mkdir` 成功为准；路径转为绝对路径，私有目录不复用已存在的目录或链接。最终路径存于原 Shell 的 `__kmb_dir`，后续 cd 和重连不重新选择目录。目录0700、暂存脚本0600，暂存时的077 umask在执行前恢复，避免改变用户命令环境。所有候选失败时报告 `bootstrap_temp_directory` 并拒绝执行。恢复检查也确认临时目录仍可用。

命令源码用 octal 编码通过限长 printf 行写入私有 `<已选基目录>/.komari-mcp-<nonce>/<command_id>.sh`，解决 heredoc、引号、多行和 PTY canonical line 长度限制。任一写入失败都不 source 半截脚本，返回非零125并输出暂存错误；保存命令退出码后先 `rm -f`，再发结束标记。异常断开、`exit` 或删除内部变量仍可能留下脚本，只能由后续受控清理处理，不承诺任意退出都完成清理。空会话目录暂留，不递归清理用户目录。旧桥接创建的持续会话没有 `__kmb_dir`，升级后应新开工作会话，不能仅凭原 request_id 假装完整兼容。BEGIN/END 采用运行时生成的 RS/US 标记，输入回显不含这些实际字节，解析在 UTF-8 清洗前，支持跨帧/同帧拼接。结束时先记录 `$?`。随机 nonce 用于减少意外碰撞，**不是对恶意远端程序的密码学证明**。

支持普通 POSIX shell 语法与简单交互；`exit`、`exec`、`set -e`、恶意修改 `__kmb_*` 内部变量、改变 shell 的解析/信号/终端选项等可能使结束标记缺失。没有 END 就保持未知/不确定，不用提示符、静默或网络关闭判断成功。管道退出码沿用该 POSIX shell 最后一项的语义，不假装 pipefail。后台输出也可能混入后续命令流，不能可靠单独归属或分离 stderr。

`command_run` 接受 session_id、调用方 command_id、command 或 argv（二选一）、execution_timeout_ms（默认30分钟，可在授权上限内调整）、wait_timeout_ms（0..10000）、max_output_bytes（256..131072，默认32768）。一条前台命令未结束时返回 BUSY，不暗中将另一条命令输入到运行程序；不同工作会话/节点并行。相同 ID 与相同内容返回原记录，不再派发；不同内容报冲突。持久化意图再发字节，但原 PTY 没有远端去重 ACK，不能承诺端到端恰好一次。

返回状态包括 dispatched_unconfirmed、running、completed、interrupt_requested、uncertain；正常 END 才给退出码。session_status 展示 waiting_agent、ready、reconnecting、attached_context_unverified、expired、context_lost、当前命令以及创建/开始/首输出/结束时间。无法从原协议可靠区分的离线/控制禁用/启动失败，保持就绪未确认或连接失败，不编造更精确原因。

`output_read` 游标为 `generation:byte-offset`，代表此前已消费的字节数量，下一段从该偏移开始，边界为排他。丢失或淘汰字节明确 output_gap；旧 generation 游标拒绝。返回 output（显示文本）、raw_base64（精确保留返回字节）、next_cursor、has_more、状态。末尾拆开的 UTF-8 字符延后到下一次返回，最终非 UTF-8 数据原始字节仍可获取。ANSI 和 CR 保留原样，不宣称完整 TUI 或终端屏幕重建。

wait_timeout/read_timeout 只影响工具等待，调用方断开不会关闭上游或杀命令。配置 idle_timeout 只回收 idle 且没有未完成命令的会话；受管理命令期限默认30分钟、最长默认6小时，可在派发前用 `execution_timeout_ms` 显式调整；计时器由兼容的新探针持有，详见 [执行策略](EXECUTION_POLICY.md)。Ctrl+C 是前台终端输入，只请求中断，后台进程/raw-mode/忽略信号程序不保证结束。close 请求官方 close 控制消息后释放本地连接，但官方没有销毁 ACK，返回 remote_termination_confirmed:false；普通网络断开与销毁 PTY不同。

## 重连、重启和日志

每个 session 单个重连流程，500ms 起退避，最大15s并带抖动，截止4m30s，遇401/403不盲目继续。WebSocket Ping 每20s、Pong/数据更新75s读期限，初次等待探针/READY 默认60s，可用 `CONNECTION_READY_TIMEOUT` 配置；不往 shell 塞空行充当心跳。

管理员短暂断线会复用 request_id；空闲时检查随机 shell 内变量与 PID，确认恢复原 shell才设 context_verified。运行交互程序时不注入探测命令，保持 attached_context_unverified；没有正常完成标记可能只能由调用方明确处置或关闭。面板/探针重启和保留窗口过期不能完整恢复上下文，相同 request_id 不能当证明。桥接重启只恢复磁盘元数据，未完成记录设 uncertain，尝试附着，不恢复 WebSocket 对象或自动重发命令。

恢复尚未证明 shell 连续性时拒绝普通交互输入，避免把旧程序的输入发送进重建的 shell；可以请求 Ctrl+C 或关闭，并如实报告不确定性。

桥接持续读取数据，而不是调用读取工具时才读。AI→bridge 断开可保留已接收数据；bridge→panel 或 Agent→panel 的终端断开会存在上游丢输出，当前探针 readOutput 不缓存断线数据，无法由 bridge 补回。

默认64活跃会话、每身份8个、最多128条逻辑会话记录、每会话1000命令。并发 HTTP 请求最多64个、每 key 最多8个，超限返回429。会话内存环形缓冲默认2MiB，上限8MiB；磁盘日志保留最初同样容量，之后 output_log_truncated:true，重启时不从该截断日志重构完整输出游标，明确 output_gap。关闭/过期/已失效记录默认24h清理；回收前再次检查空闲和命令状态，未完成长任务不按 idle 误杀。文件变更幂等记录最多10000条、总量最多64MiB，每条响应最多64KiB，超大响应明确 response_omitted；达到上限拒绝新增，当前不自动删除以免旧 ID 被重放。审计两份轮转日志，各8MiB，只记录工具/身份/节点/ID/结果，不保存凭据或命令正文。

关键状态文件/原始日志0600、目录0700；输出可能含用户秘密，遵循该有限保留范围。远端临时 staging 目录内单条脚本正常结束后删除；异常退出留下的目录/文件尚无远端 TTL 清理，应由部署后的维护策略补齐。停用 key 或 MCP 拒绝后续工具请求，已经开始的进程不会因此自动取消，读连接仍可持续缓存。

## 验证范围与升级

Linux 隔离测试用模拟 Komari 协议服务器配合真实本地 PTY，MCP 客户端使用官方 Go SDK。覆盖持久 cd/export、非零退出、多行 heredoc、中文跨帧与游标、流式/无输出任务、等待超时、交互输入、中断未知状态、独立工作会话、短断线原 shell恢复、同ID新shell检测、桥接重启不重发、缓冲溢出、保留期失效、密钥撤销、节点/会话越权、控制入口分离、文件重试不重复及原生文件 HTTP 请求格式。

```bash
go test -race ./... -count=1
# 根目录：
go test ./internal/mcpbridge ./web/router -count=1
```

管理代理测试注入认证/2FA middleware 以验证没有旁路及没有转发 Cookie；真实已开启2FA账户和网页完整流程仍需隔离官方面板集成验收。容器指标测试是 cgroup 文件夹具，实际容器 quota/host-network/hierarchy 配置尚未验证。未操作生产节点。

升级前锁定面板/探针SHA，验证管理员 Bearer、request_id帧、独立探针 terminal WS、close/resize、native file API 与5分钟保留。桥接和导航可独立撤回；停止桥接保留窗口内可能仍有远端 PTY/命令，回滚并非自动取消任务。生产配置、反向代理、probe替换和上线另行确认。

本轮原有终端和监控连接的局部修复、开销及验证边界见 [长连接稳定性说明](./STABILITY.md)。监控持续保活，终端只按工作会话建立并复用；没有生产上线。

## 正式发布部署

正式设备使用 GitHub Release 中的 `komari-mcp-<os>-<arch>` 或预构建 `ghcr.io/0ozzzii/komari-mcp-bridge-mcp:<TAG>`。本目录 Dockerfile 仅保留为开发源码构建入口，Compose 使用预构建镜像，不要求正式设备有 Go。统一发布、安装、自更新和上游审查见 [说明](../release/README.md)。

## 节点选择与密钥列表

MCP 管理页节点顺序与原服务器列表一致，按 `weight` 稳定排序；显示原节点名称、本地国旗图标和在线/离线，不常驻显示 UUID。在线状态复用面板原有 WebSocket/HTTP presence，页面加载时读取，刷新页面更新。离线显示最后一次成功上报时间；没有记录（如面板刚重启）明确显示未知，不伪造精确断线时间。

“全部／在线／离线”筛选只影响显示。“全选当前节点”追加选择筛选后的现存节点，不清除其他已选项；“清空选择”清除全部选择，包括筛选隐藏项。已移除的授权节点保留可取消的条目，全选不会重新选择它；新节点不会自动授权。每密钥上限仍为 128 个节点。

已建密钥默认折叠，摘要显示名称、状态和授权节点数；展开才显示编辑控件，保存时保留当前展开状态，重新进入页面恢复默认折叠。内嵌页自动匹配内容高度，由原后台容器统一滚动，节点列表和弹窗沿用细滚动条。

本地浏览器验收（需要 Python Playwright 和 Chromium）可执行：

```bash
python3 dev/validate-mcp-ui.py --panel /tmp/komari-server-mcp-dev
```

只启动临时 loopback 面板、数据库和桥接，结束后关闭并清理；探针上报是本地模拟，不等于远端执行验收。证据见 [mcp-node-picker-validation.json](mcp-node-picker-validation.json)。

日志、诊断包导出、运行告警与开发流水线通知的区别，以及断线后不重复执行的边界，见 [DIAGNOSTICS.md](DIAGNOSTICS.md)。本机执行 `komari-mcp diagnostics --state-dir <实际桥接目录> --output <新zip路径>` 可导出有限、去除敏感负载的状态包；无需停止服务或连接远端。
