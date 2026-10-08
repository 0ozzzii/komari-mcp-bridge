# Komari MCP Bridge · MCP 适配与优化版

基于 [Komari](https://github.com/komari-monitor/komari)、[Komari Agent](https://github.com/komari-monitor/komari-agent) 和 [Komari Web](https://github.com/komari-monitor/komari-web) 的独立维护版本。保留原监控面板和探针，在原后台增加 **MCP** 页面，让 AI 客户端直接使用已有远程终端与文件通道。

**这是本仓库的适配版本，不是 Komari 官方发行版。** 上游项目、作者和第三方依赖的署名与许可保留；我们的修改范围、来源和许可说明见 [ATTRIBUTION.md](ATTRIBUTION.md)。问题请提交到 [本仓库 Issues](https://github.com/0ozzzii/komari-mcp-bridge/issues)，不要把本版特有问题归因于官方。

[下载最新正式版](https://github.com/0ozzzii/komari-mcp-bridge-release/releases/latest) · [安装与更新](release/README.md) · [MCP 工具与配置](bridge/README.md) · [执行保护](bridge/EXECUTION_POLICY.md) · [日志与排障](bridge/DIAGNOSTICS.md)

## 我们增加和优化了什么

| 部分 | 本版适配与优化 |
| --- | --- |
| 原后台 MCP 页面 | 一级 MCP 菜单；总开关；多调用方 Key；按设备与能力授权；名称、国旗、在线状态和服务器列表排序；筛选后全选；已有 Key 默认折叠 |
| AI 接入 | 创建后一次显示完整接入 URL，也提供标准 Bearer 方式；15 个合并工具覆盖节点、持续终端、文件与状态查询 |
| 持续执行 | 常驻桥接持有 WebSocket，工具返回后继续接收输出；工作会话隔离；受支持 POSIX／PowerShell 会话保留目录、环境变量等状态 |
| 状态与恢复 | 增量输出游标、命令状态和可确认的退出码；有限重连；明确报告上下文丢失、输出缺口和执行不确定；不自动重发未知结果的命令 |
| 探针增量 | 容器资源范围自动识别与显式覆盖；连接保活与资源清理；共享执行槽位、准入和期限保护。原探针监控与主动回连机制继续使用 |
| 执行策略 | 所有管理选项留在 MCP 页弹窗；设备／Key 上限、临时策略、低内存暂停新增；查询和中断不被普通执行槽位堵住 |
| 日志查看 | MCP 页“日志记录”弹窗，按设备、Key、时间、记录类型和异常筛选；分页查看事件及会话已保存回显，提示轮转、截断和输出缺口 |
| 诊断 | 有上限的审计与会话输出；默认脱敏诊断包；连接与命令状态变化可追溯；不把凭据和命令原文专门写入审计 |
| 发布与维护 | 官方兼容命名的预构建二进制、安装器和容器镜像；锁定三处上游 SHA；每日东八区12点同步候选 PR、CI 与可配置的主备模型审查 |

这是对现有工程的增量适配。MCP 工具集中在桥接服务，不把整套工具搬进每台探针；也没有逐一移植其他项目的65个工具。

## 通信与运行方式

```text
AI 客户端
  → 面板域名 /mcp/<调用方 Key>
  → 常驻 MCP 桥接
  → Komari 面板管理员终端 WebSocket
  → 探针主动建立的终端 WebSocket
  → 被控环境的 shell / PTY
```

输出反向经过 **探针 → 面板中继 → 桥接缓存与解析 → AI**。监控／事件连接通知探针开启终端，终端字节流走独立 WebSocket。无需为了 MCP 增加被控端 SSH 服务或入站端口；两侧仍须能够访问面板。

桥接与面板可以同机、同容器网络或位于可访问面板的其他常驻机器。只需一个面板域名：面板页面和 `/mcp/...` 按路径区分；私有控制口不对外发布。调用方请求结束不等于终端关闭，桥接必须由服务管理器长期托管。

## 安装本版本

公开成品就绪后，从 [分发仓库 Release](https://github.com/0ozzzii/komari-mcp-bridge-release/releases/latest) 下载，**部署设备无需拉源码编译**。安装方式与官方保持一致，源码构建用于开发与 CI。私有开发库和公开分发库已分离；仓库存在不代表附件已发布，具体边界见 [分发说明](release/PUBLIC_DISTRIBUTION.md)。

| 组件 | 成品和作用 |
| --- | --- |
| 面板 | `komari-<系统>-<架构>`；Windows 带 `.exe`。保留原后台，增加 MCP 管理入口与代理 |
| 探针 | `komari-agent-<系统>-<架构>`；安装命令由本面板生成，沿用节点 Client Token |
| 桥接 | `komari-mcp-<系统>-<架构>`；`serve` 常驻运行，配置管理员 API Key 和私有控制凭据 |
| 容器 | `ghcr.io/0ozzzii/komari-mcp-bridge:latest`、`...-agent:latest`、`...-mcp:latest`；[Compose 示例](release/compose.yml) |

安装步骤、Windows 服务注意事项、校验、升级和回退见 [发布与安装](release/README.md)。安装时记录实际 tag、commit、SHA256／镜像 digest；`latest` 会变化，回退选历史固定版本，并单独备份数据库和桥接配置／状态。

面板初始化后配置桥接服务，在后台 **MCP → 启用 → 选择节点与能力 → 创建密钥**。支持自定义远程 Streamable HTTP MCP 的客户端可使用生成的完整 URL；该 URL 含凭据，应按密码保存。不同 AI 产品的 MCP 支持与联网限制仍需核对，能调用 `curl` 不等于已自动获得 MCP 工具注册能力。

探针 Client Token、管理员 API Key、调用方 MCP Key 和私有控制 Token 是四种不同凭据。管理员 API Key 只留在服务端，节点范围授权由桥接执行，不声称官方 Key 原生具备细粒度节点权限。

## 能力边界与验证范围

- **完成状态**：PTY 本身没有命令完成事件。受管理 POSIX／PowerShell 使用开始／结束协议与退出码；原始交互、`exit`、`exec`、特殊 trap 等情况可能只能报告不确定。不能用提示符或静默时间判断成功。
- **断线**：尽力恢复原 PTY，ID 相同不证明 shell 连续。桥接只保存实际收到的输出；未收到的断线输出不能凭空补回，不保证端到端恰好执行一次。
- **内存与取消**：并发上限和执行期限减少累积，不是进程内存强制隔离，也不能保证避免 OOM。Ctrl+C 是中断请求，不保证后台进程退出。
- **日志**：审计默认两份各8MiB；会话输出默认最多2MiB，保存前段，超限标记截断；死会话默认24小时后清理。页面按需加载，上限和保护见 [诊断说明](bridge/DIAGNOSTICS.md)。原始回显可能含敏感信息，默认诊断包不包含它。没有新增自动配置备份或完整远端日志回放。
- **权限**：命令沿用探针运行用户、文件系统与命名空间。普通容器的探针不自动获得宿主机权限；从 root 目录运行命令也不等于以 root 用户运行。
- **平台**：Release 构建平台由 [release/config.json](release/config.json) 列出。Linux 本地隔离真实 PTY 链路和 Chromium 管理页有验证；Windows 成品有 CI 执行检查。构建成功不等于所有平台、Windows 服务、Android／Termux、Cloudflare 或用户生产环境已端到端实测。持续 shell 支持 Linux POSIX 与匹配探针的 Windows PowerShell；真实 Windows ConPTY／PowerShell 有隔离 CI 验证；Windows 优先已安装的 PowerShell 7，POSIX 支持自适应可写临时目录。Android／Termux 的真实设备与 Windows 测试节点 LTSC Build17763 的面板中继链路仍需目标验收。具体范围见 [桥接说明](bridge/README.md)。
- **网络**：国内节点沿用已验证 HTTPS 公网入口，别误加内部端口或更换协议。HTTPS/443 不保证免于备案或接入限制；保留 TLS 校验。详见 [接入注意事项](release/README.md#国内节点与域名接入注意事项)。

没有依据宣称比官方“快十倍”、固定内存仅几MB，或免疫所有断线、防火墙和 OOM；这些需要针对设备、网络和负载实测。

## 上游同步和模型审查

基线来自 [release/config.json](release/config.json)，每个发行版的 `release.json` 记录真实构建来源。官方更新先生成同步 PR，通过 CI 后再进行模型／外部 Agent 审查；冲突、拒绝或不确定不强行合并。它不会自动升级用户设备，也不把源码 main 与已发布成品混为一谈。

主、备模型的 URL／模型 ID 放 GitHub Actions Variables，API Key 放 Secrets。默认串行、并发1；429 有限重试。明确启用 `UPSTREAM_AI_AUTOMERGE=true` 才允许审查通过后自动合并。故障 Issue 配合 GitHub 自带通知；邮件取决于账号 Watch 与通知设置。[逐项配置与故障恢复](release/README.md#内置主备-ai-审查推荐)。

## 开发、来源与反馈

开发环境使用 [dev/setup.sh](dev/setup.sh) 和锁定补丁准备上游探针／前端；Go工具链、测试与隔离验证说明见 [桥接文档](bridge/README.md)。不把开发环境的路径当成生产安装目录。

[功能架构](bridge/ARCHITECTURE.md) · [连接稳定性](bridge/STABILITY.md) · [探针补丁](agent-patches/README.md) · [前端补丁](web-patches/README.md) · [许可证与来源](ATTRIBUTION.md)

感谢 Komari 官方作者、贡献者及第三方依赖维护者。官方使用与原有功能请参考 [Komari 官方文档](https://www.komari.wiki/)。本版本未获官方背书，名称用于说明来源与兼容关系；按保留的许可证使用和分发，仅管理自己拥有或获授权的设备。
