# Windows 面板与 MCP 桥接部署指南

这是通用模板，不包含预先确认的机器、域名、账号或安装授权。先核对用户指定的新实例及操作范围；已有探针、代理、隧道、认证和业务服务不自动纳入部署。

## 部署前准备

- 核对 Windows 版本、Build、架构、实际服务账户和独立数据目录；交叉编译成功不是旧系统运行验证。
- 下文 `komari.example.com` 是保留示例域名，必须替换为用户自己的 HTTPS 域名。
- 使用已验证的 Windows 服务托管方式；控制台 exe 不能仅靠 `sc create` 自动实现 SCM 协议。
- 下载同版预构建成品、`release.json`、`sha256sums.txt`，不要求目标安装 Go／Node。

开发仓库为 `0ozzzii/komari-mcp-bridge`，公开分发仓库为 `0ozzzii/komari-mcp-bridge-release`。从分发仓库解析 latest 的实际 tag，再从同一固定 tag 下载整组文件。仓库存在不代表 Release 附件已发布；缺少成品就保留原服务，不下载官方原版冒充本版本。

Windows x64 下载 `komari-windows-amd64.exe`、`komari-mcp-windows-amd64.exe`；其他架构按实际选择。用 `Get-FileHash -Algorithm SHA256` 校验后才切换。升级前备份程序、服务定义、配置、桥接 state 和一致性数据库备份，保护 NTFS ACL，不盲目降级数据库。

## 配置和单域名接入

下面端口只是内部监听示例，可按实际配置覆盖。公网 HTTPS 域名不能直接拼上内部端口。

| 组件 | 示例配置 |
| --- | --- |
| 面板监听 | `KOMARI_LISTEN=127.0.0.1:18080` |
| 面板转发 | `KOMARI_MCP_UPSTREAM_URL=http://127.0.0.1:8967/mcp` |
| 面板私有管理 | `KOMARI_MCP_CONTROL_URL=http://127.0.0.1:8968` |
| 面板控制凭据 | `KOMARI_MCP_CONTROL_TOKEN=<安全注入的控制凭据>` |
| 桥接上游 | `KOMARI_BASE_URL=http://127.0.0.1:18080` |
| 桥接管理员凭据 | `KOMARI_API_KEY=<此面板的管理员APIKey>` |
| 桥接控制凭据 | `BRIDGE_CONTROL_TOKEN=<同一个控制凭据>` |
| 桥接数据 | `BRIDGE_STATE_DIR=<独立私有目录>` |

先初始化新面板并创建管理员 API Key，再配置桥接常驻服务。管理员 Key、调用方 MCP Key 和探针 Client Token 不互换，不写聊天、截图或公开日志。

已有获授权的反向代理／Tunnel 可把 `komari.example.com` 转发到面板的内部监听地址。面板 `/`、探针 `/api/clients/...` 和 `/mcp` 共用域名；不发布 `8968/control`。新增路由须先获得授权，不覆盖其他路由。核对 Authorization、WebSocket Upgrade、流式响应、Access 实际客户端认证和连接超时；浏览器可打开不证明探针／MCP可用。

## 验收、日志与回滚

核对 `/api/version`、原侧栏 MCP、开关和 Key 授权持久化。无真实节点时不预置虚构授权。新探针接入后检查节点排序／筛选／全选、URL初始化、工具列表、持续 cd/export、流式输出、非零退出码、中断和关闭。调用等待超时不杀命令；断线不自动重发。

受管理执行先提交 `komari_execution_policy`。长任务首次派发显式指定 `execution_timeout_ms`；租约只由新命令／非空交互输入续期，读日志和心跳不续期。终端需要配套桥接与探针，升级后新开工作会话。

按 [诊断说明](../bridge/DIAGNOSTICS.md) 捕获并轮转服务日志，记录实际 state 路径。故障使用同版 `komari-mcp diagnostics --state-dir <目录> --output <新的zip>` 导出有界脱敏元数据，不附私有配置或完整输出。

只回滚本次选定的程序、服务和新增路由，保留独立数据与备份。返回版本／commit／SHA256、重启范围、逐项证据、未验证项和回滚步骤。故障、断线、重启压力测试仅在获授权的隔离环境执行。

模型审核配置在开发仓库 GitHub Actions，见 [审核配置教程](AI_REVIEW_SETUP.zh-CN.md)，不在面板或探针部署模型密钥。
