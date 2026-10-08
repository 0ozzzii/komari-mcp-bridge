# 给部署 Agent：更新面板、MCP 桥接与 Windows／Android 探针

你负责把当前已部署的 Komari MCP 定制版本更新到本项目的 **v1.0.3**，并在用户指定的 Windows 与 Android／Termux 节点上验证跨平台终端。仅操作本次明确选定的面板、配套桥接及这两个节点的 Komari Agent。不要更新其他探针、清理业务程序或借机修改系统配置。

## 目标与边界

- 开发仓库：`https://github.com/0ozzzii/komari-mcp-bridge-dev`；公开分发仓库：`https://github.com/0ozzzii/komari-mcp-bridge`。使用本项目正式 Release，不能下载官方原版冒充带 MCP 优化的版本。
- 本次配套固定版本：**v1.0.3**。先确认 Release 工作流成功、附件完整，再更新。尚未发布／文件不全时只做独立准备，不停原服务，不在目标设备现场编译。
- 执行前确认用户已授权更新选定面板、桥接和目标探针，以及切换时必要的短暂重启。先备份并确保有独立控制途径；不批量处理其他设备。
- 沿用各自真实 endpoint、Client Token、节点身份、安装目录、服务名、运行用户、既有启动参数与日志托管方式。不新建重复节点，不把调用方 MCP Key 或管理员 API Key 当探针 Token。
- 不改 Cloudflare Tunnel／Access、域名、Nginx／Caddy、监听端口、防火墙、2FA、系统执行策略、PowerShell Profile、系统 PATH 或其他自启动项。不新增 SSH 中继；沿用已有可用的 HTTPS 直连接入方式。
- 不把密钥、Cookie、完整服务命令行、MCP接入URL、私有配置全文贴进聊天或日志。只从本机现有安全配置读凭据。
- 这是应用更新与验收，不是清理任务，不做进程／自启动“巡检清理”。

## 1. 一次性核对部署目标，继续完成独立准备

查清当前面板、桥接部署在哪台机器，以原生服务还是 Docker 运行；面板所在机器与被控节点分别核实。记录现有版本／commit／SHA256、工作目录、数据库和 bridge state 路径、服务名及 PID。

在面板允许访问的节点里，用节点名称和现有设备身份核对用户指定节点的 UUID、系统、架构及在线状态，以实际结果为准。如果同名、身份对不上或存在多个探针，先用弹窗集中询问目标，不能猜测后替换。

确认本地 Agent 有独立于待更新探针终端的管理通道。**不要在一个 Komari MCP 会话里把承载该会话的探针直接停掉，然后假装还可以靠同一个连接继续完成切换。** 可以使用已存在且获授权的本地／远程管理途径，不额外建立套娃隧道。缺少独立途径时，先准备好文件和回滚计划，再用弹窗说明具体缺项。

核对是否有正在运行的 MCP 命令和人工终端。不要为升级杀业务任务；有在途任务时说明等待结束或中断会话的选择。已知路径、域名和已有授权不要反复询问。

问题尽量集中使用弹窗卡片：一项推荐、三项确实可选的方案，保留自由回答。等待必要信息期间继续做下载、校验和备份方案，避免写一半就结束任务。密钥通过本机安全配置处理，不让用户粘贴到聊天。

## 2. 从正式 Release 下载预构建成品

打开 `https://github.com/0ozzzii/komari-mcp-bridge/releases/tag/v1.0.3`；该工作流运行在私有开发库，能访问时确认 **Official-aligned release binaries and images** 完整成功；不能访问时按公开附件、来源和校验值验证，不声称看过私有 CI。读取同版 `release.json` 和 `sha256sums.txt`，确认来源完整 commit，不能只看网页标题“latest”。整次更新固定这个 tag，不在每个文件下载时重新解析 latest。

下载地址格式：

```text
https://github.com/0ozzzii/komari-mcp-bridge/releases/download/v1.0.3/<文件名>
```

| 目标 | 需下载的成品 |
| --- | --- |
| Windows x64 的 Windows 节点 探针 | `komari-agent-windows-amd64.exe` |
| Android ARM64 的 Android 节点 探针 | 仅在实际为 ARM64、原探针确实运行 Linux ARM64 成品时，使用 `komari-agent-linux-arm64`；其他架构按实际选择 |
| Linux x64 原生面板／桥接 | `komari-linux-amd64`、`komari-mcp-linux-amd64` |
| Windows x64 原生面板／桥接 | `komari-windows-amd64.exe`、`komari-mcp-windows-amd64.exe` |
| Docker 面板／桥接 | `ghcr.io/0ozzzii/komari-mcp-bridge:v1.0.3` 和 `ghcr.io/0ozzzii/komari-mcp-bridge-mcp:v1.0.3`，记录实际 digest |

Android通常 `uname` 返回 Linux，但这不是二进制兼容性证明。核对旧探针实际格式、架构、权限及运行方式；在独立目录校验新文件并尝试无网络控制副作用的 `--help`。如果新成品不能运行，保留旧探针并报告真实错误，不安装 Go、不改系统库、不静默换官方旧版。

所有文件先下载到新临时目录，逐个做 SHA256 校验。Windows用 `Get-FileHash -Algorithm SHA256`，Linux／Android用可用的 `sha256sum`／`toybox sha256sum`。校验失败，不停止服务、不覆盖旧文件。模型 API 配置不属于设备更新，不需要在各节点放第三方 API Key。

## 3. 备份与配套切换

备份范围：目标程序、服务定义、私有配置、版本来源，以及面板数据库和桥接 state。数据库按实际数据库类型做一致性备份；只拷贝运行中的数据库文件不一定可靠。备份目录保护权限，禁止覆盖之前的唯一备份。

建议顺序：完成全部准备 →安全停止接收新 MCP 执行、处理在途命令 →更新面板和桥接 →先更新验证Windows 节点 →再更新验证Android 节点。如果采用MCP总开关暂停新调用，记录原开关状态，验收后恢复原状态；不要撤销已有 Key 或扩大节点授权。面板／桥接可能重启，内存 WebSocket 不会因 state 文件保存而保持存活，升级后应新开工作会话。

原生面板／桥接按现有托管方式只切换同名目标服务；Docker保留原卷、网络、配置及镜像回滚digest，不直接用示例 Compose 覆盖生产文件。缺失必要新配置时按本版文档补齐私有配置，保留现有 Key 和节点授权，不能为恢复连接关闭认证。

Windows 节点使用已有Windows服务托管方式，先备份、校验新 exe，才停止该节点确定的 Komari Agent 服务、替换、启动。保留原参数、账号、目录及服务ACL。不批量结束 powershell.exe，不动 cloudflared、Tailscale、SSH或周期任务。

Android 节点按实际的Android启动管理方式切换：可能是原生Android shell、Termux服务或已有启动脚本，不能默认有systemd或sudo。只停止已确认的探针进程／监督服务，保持旧启动参数，替换已校验的可执行文件并恢复启动。不要用 `pkill` 模糊匹配，防止监督脚本同时拉起两个探针。不为升级自动加 `-u`、改DNS或改endpoint端口。

本版Windows MCP受管理终端会优先找已安装的 `pwsh.exe`，服务 PATH 没有它时还检查 `%ProgramFiles%\PowerShell\7\pwsh.exe`，缺少时回退 `powershell.exe`。无需改服务PATH；没有PowerShell7不是强制安装理由。管理页人工终端继续原选择方式。**单独更新桥接不能改变旧探针启动的Shell，必须更新配套探针。**

本版POSIX初始化通过PATH解析sh，缺少时尝试 `/system/bin/sh`。临时目录优先级为TMPDIR、/data/local/tmp、/tmp、HOME、初始工作目录，最终目录实际创建成功且为0700，暂存脚本0600。无需自行创建 `/tmp` 软链接、改Android根分区或放宽权限。

## 4. 分层验收，不能只检查101或监控在线

先确认面板和桥接版本、前端原侧栏MCP入口、旧配置与节点授权保留，再确认两个目标探针同版、监控重新上报。然后从各自新建、独立的MCP工作会话验证：

1. `komari_nodes_list` 能选到预期目标，资源范围合理；先用 `komari_execution_policy` 提交所需工作策略。不得关闭执行保护绕过缺失ACK。
2. `komari_session_open` 后观察等待探针、Shell READY、`context_verified=true`；Windows记录实际 `shell_executable`，确认到底是pwsh还是powershell。
3. Windows 节点分两次运行 `Set-Location $env:TEMP`／`Get-Location`；设置并读取测试变量；执行 `Get-Process | Select-Object -First 5`，检查对象输出在结束前排空。
4. Windows 节点子进程 `cmd.exe /d /c exit 7` 返回7；下一条成功PowerShell命令返回0，旧LASTEXITCODE不残留。不要在父Shell直接 `exit 7`。
5. Android 节点执行 `pwd`、`cd` 后下一次 `pwd`、export后读取、中文／引号／heredoc，并在子进程产生非零退出。确认不再BOOTERR mkdir；可以读取本会话的 `__kmb_dir` 作无秘密诊断，但不修改内部变量。
6. 用无害短任务验证先有首段输出、后有结束；工具等待到时返回command_id／游标，命令继续被记录。没有输出不能判断完成。
7. 对自己启动的短暂Sleep命令请求Ctrl+C。只有可靠结束事件才记“中断成功”；没有确认记不确定，不追加普通命令，不自动重发。不要用强信号试杀其他进程。
8. 相同command_id重复调用返回原记录；跨会话／节点输入输出隔离。关闭自己新建的测试会话，记录资源释放。
9. 核对MCP日志弹窗及有界诊断记录能找到本次连接、命令和失败事件，凭据未泄露。需要导出时使用同版 `komari-mcp diagnostics`，默认不带真实命令输出和私有配置。

网络中断、探针／面板重启、OOM等破坏性验证不在生产执行。这次正常升级需要的已授权短暂重启不能被扩展成破坏性压力测试。网页能开终端、探针上报正常、WS101、健康接口成功都不等于MCP Shell已就绪。

## 5. 失败处理与返回结果

如果某一步失败，保留错误类型、时间、tag／commit／SHA256、节点／会话／command_id和脱敏日志。不要根据“没看到结束”自动重复有副作用命令。先恢复这次替换的目标程序／服务配置；数据库回滚按已确认兼容性和一致性备份操作，不盲目降级覆盖。原节点Token、面板Key、Tunnel及其他业务不纳入随意重置。

最终按表返回：组件／设备、更新前后版本与PID、实际文件路径和Shell、校验结果、重启范围、验收项目及证据、失败／未验证项、备份与日志位置、可执行回滚步骤。不要只写“全部正常”；Android真机和Windows 节点结果分别列出，明确保留期内重连是尽力恢复、未收到的日志无法凭空补回。

参考：[发布说明](https://github.com/0ozzzii/komari-mcp-bridge/blob/v1.0.3/release/README.md)、[跨平台兼容记录](https://github.com/0ozzzii/komari-mcp-bridge/blob/v1.0.3/release/platform-terminal-notes.md)、[审核API配置教程](AI_REVIEW_SETUP.zh-CN.md)。
