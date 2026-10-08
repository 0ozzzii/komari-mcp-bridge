# 跨平台终端兼容记录

调研日期：2026-10-08。候选代码见 [PR #5](https://github.com/0ozzzii/komari-mcp-bridge-dev/pull/5)。v1.0.1 不含这批修复；本记录不表示生产服务已经更新。

发布复核曾在冷启动的 Windows PowerShell5.1 上发现单次初始化输入未返回 READY（原生 CI，PowerShell7 同次通过）。ConPTY 创建和探针 ACK 不能证明 Shell 已开始接受输入。桥接因此仅对首次 Windows 初始化的幂等设置／READY 探测按1秒间隔、有就绪期限地重试；收到 READY、连接变化或会话状态变化即停止。业务命令、分块脚本和断线恢复均不走此重试路径。迟到／重复 READY 不能覆盖运行中或重连后的上下文状态。原生测试使用同一初始化等待逻辑，冷启动预算与桥接默认60秒一致，不承诺所有机器都在1～3秒内就绪。

| 环境 | 证据与限制 | 当前适配 |
| --- | --- | --- |
| 标准 Linux | 本地真实 PTY、桥接状态机和官方面板／探针源码中继可隔离验证 | 持续 POSIX Shell；命令继续在同一父作用域执行 |
| Android／Termux、受限容器 | 用户报告 BOOTERR mkdir；旧代码确实写死 /tmp 与 /bin/sh。受限路径测试为 Linux 本地模拟，不能称 Android 实测 | PATH 找 sh，缺少时尝试 /system/bin/sh；按候选目录创建私有暂存目录 |
| Windows PowerShell 5.1 | 较新 Windows CI 的真实 ConPTY 曾吞掉 RS/US、窄终端折行；用户 Windows 测试节点 Build17763 报告的无秘密原始切片则保留两端控制字节 | 使用可打印标记、单独一行、至少256列，不能依赖所有 ConPTY 都保留 C0 字节 |
| PowerShell 7 | 用户报告 Windows 测试节点 已安装7.6.6，但未提供该 Shell 的完整受管理中继验收；CI 必须分别跑5.1与7 | 已安装 pwsh 优先，缺少时回退 powershell；服务 PATH 未更新时检查标准安装路径 |

POSIX 的候选顺序为 TMPDIR、/data/local/tmp、/tmp、HOME、初始工作目录。只选实际能新建目录的候选；目录0700、脚本0600，不接受已存在的同名目录／链接。绝对目录保存在远端父 Shell 的 `__kmb_dir`，后续 cd、独立工具调用和恢复使用同一值。没有任何可用目录就明确失败，不在根目录盲目创建，不修改系统权限。暂存失败返回125且不执行半截源码；保存用户命令退出码，删除暂存脚本，再发 END。

桥接没有远端路径配置的全局副作用。新的 Bootstrap 变量只存在于它新建的工作会话；旧版已存在的 Shell 缺少变量，应新开工作会话。新探针的 pwsh 选择仅用于 MCP 受管理会话，人工网页终端的 Shell 选择不变。没有每次命令重新启动 Shell，没有自动安装 pwsh，没有修改系统 Profile、ExecutionPolicy、PATH 或现有服务。

## 如何理解 Windows 测试节点 报告

- 报告声明 Windows10 LTSC Build17763、PowerShell5.1.17763.8755、pwsh7.6.6。开发环境没有直接核验这些安装状态。
- 提供的诊断切片包含 `1e4b4d423a70726f62653132333a52454144593a323936341f`，即 `RS KMB:probe123:READY:2964 US`。这是一段报告中的原始字节证据，不证明所有 Windows 版本和不同 Shell 的行为相同。
- 报告的 Shell 启动方式主要来自源码审查，不等于实际抓到了该 LocalSystem 探针子进程的完整启动身份。用户会话里的本地 ConPTY 也不等于 LocalSystem／真实 WebSocket 中继链路。
- 发现 LASTEXITCODE 残留符合 PowerShell 语义。桥接每条命令先清空它，结合本条状态判断；任意命令都有原生程序退出码与 PowerShell 语句状态的不同语义，不能仅从后续成功文本推断成功。
- Ctrl+C 检测只查是否出现后续文本，缺少开始 ACK、完整时间线和成功返回标记；有时输入回显本身也会出现目标文本。它不足以证明所有 ConPTY 的0x03均无效，也不能将请求中断直接当作确认结束。当前保持独立中断请求、结果不确定与不重发机制，不对生产进程试用更强信号。
- “Profile 默认参与”应理解为裸启动允许加载存在的 Profile，是否真的存在并执行、PSReadLine 是否真的加载仍须观察。不能因 PID 前后相同就推断所有配置和二进制从未变动。

## 后续设备验收

先用校验过 SHA256 的独立诊断附件分别测试 powershell.exe 与 pwsh.exe；该附件只操作自己创建的进程，不替换现有服务。诊断程序输出 Shell 名称、自己的 PID、READY 标记 hex/base64、各测试结果。未安装 pwsh 时明确跳过；CI 使用 require-pwsh 防止遗漏7的运行测试。

真实链路还需在隔离的新工作会话核实：探针 ACK 的实际 shell_executable、READY、目录／变量／函数保留、首段日志早于 END、非零与残留退出码、短暂断线、Ctrl+C。Android 需用它实际的 PATH/TMPDIR、权限和 PTY 验证。不得把本地模拟、构建成功或 WebSocket101 描述为目标设备验收成功。

Ctrl+C、网络断开、超时后，未收到可靠结束标记的命令仍占用执行记录并报告不确定；不会自动重发。关闭保留窗口、命令级日志完整回放、后台任务归属和强制进程隔离不是本补丁提供的能力。
