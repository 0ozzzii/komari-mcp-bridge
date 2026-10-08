# 运行记录与故障取证

已有工具审计、会话元数据和受限终端输出；现新增不依赖 AI 轮询的连接/命令状态事件，以及离线诊断包导出。记录说明发生了什么，不等于自动判定根因，也不能还原没有收到的输出。首次使用时先核对实际安装 tag/commit。

| 记录 | 位置 | 内容和上限 |
| --- | --- | --- |
| 工具与控制操作 | `<BRIDGE_STATE_DIR>/audit.jsonl`、`.1` | 调用方 Key ID、工具、会话/命令 ID、耗时、成功/失败分类；两份各 8 MiB 轮转，不保存命令正文/凭据 |
| 后台连接事件 | 同上 | connecting/waiting_agent/ready/reconnecting、连接失败次数和 HTTP 状态、上下文验证/丢失、输出缺口/截断；状态变化才记录，不为每段输出/Pong 写日志 |
| 命令状态事件 | 同上 | 请求期限、探针截止时间、超时标志； 派发未确认、运行、完成（退出码）、不确定、中断请求；由接收循环记录，AI 客户端不在线时也记录 |
| 会话快照 | `sessions/<session_id>.json` | 最新连接/命令状态、时间、代次、错误分类和关联标识；元数据含内部连续性 nonce，不直接对外发送整个文件 |
| 合并终端输出 | `sessions/<session_id>.log` | 默认每会话最多 2 MiB，超过即标记截断；可能包含秘密，默认诊断包排除 |
| 面板/探针运行与崩溃日志 | 现有进程 stdout/stderr 或服务日志 | 沿用原日志；部署方必须把 Windows 服务输出/systemd/Docker 日志接住并设轮转，不能只在临时终端运行 |
| 原面板审计与告警 | 原数据库、通知页面及配置的通知渠道 | 原功能继续使用，通知是否实际投递取决于用户配置；新桥接状态事件不自动向邮箱发运行告警 |
| 上游同步/构建/模型故障 | GitHub 故障 Issue、Actions 和 artifact | 开发流水线通知，与正在运行面板的故障告警不同；邮件由 GitHub 账号通知设置决定 |

审计写入、权限或轮转失败时，桥接 stderr 输出节流警告（同类日志路径最多每分钟一次），不静默声称记录成功。日志是正常文件写入，突然断电不保证末尾事件持久化。部署方应监测服务退出、磁盘余量和这类警告。Windows 的文件 mode 不代替 NTFS ACL。

## 在面板查看

更新配套面板与桥接后，在后台 **MCP → 日志记录** 打开只读弹窗；不需要把私有控制凭据交给浏览器。日志只向已认证管理员提供，调用方 MCP URL 不提供管理员日志接口。

- 设备选项沿用服务器列表排序和名称。可组合筛选设备、调用方 Key、最近1小时／24小时／7天或全部保留记录、命令／连接／工具／管理／策略事件，以及异常或不确定状态。
- 每次最多加载50条，按文件写入顺序从新到旧；每个窗口最多500条。单次扫描最多2MiB，筛选稀疏时可继续“检索更早记录”，不能把一页无匹配当成整个历史没有记录。查询开始后使用固定快照；新增事件需刷新，快照被轮转淘汰会提示重新查询。
- 展开记录可看状态、退出码、时间、耗时、关联 ID、输出缺口等允许字段。已删除 Key／设备仍可在全部记录中出现，以未知标签显示；过滤选择来自当前面板列表。
- 有会话关联的记录可按需读“会话回显”。每次最多16KiB，当前窗口最多显示128KiB；按原始字节偏移继续读取，跨页中文不会被切坏。原始非UTF-8或最终截断字符替换显示并提示，ANSI清理只用于展示。
- 回显是整个会话的合并终端流，可能含后台输出和敏感信息，不是独立每条命令 stdout/stderr。已清理、截断或未收到的内容不能补回；查看日志不续期会话、不重新派发命令，也不延长执行策略。

页面为有上限的排查窗口，不是完整长期归档。默认保存日志的前段，内存环形缓冲保留近段，两者不是同一份完整历史。新的管理接口对查询长度、分页、文件类型和读取量限制；日志读取有独立单通道限流，不与中断／状态接口争用准入槽位。故障告警渠道和自动配置备份没有因此自动开启。

## 默认诊断包

在桥接安装目录，用**同一个桥接版本**运行；不需停止服务，也不连接被控端：

```bash
./komari-mcp diagnostics --state-dir /path/to/bridge-data --output ./komari-diagnostics.zip
```

Windows PowerShell：

```powershell
.\komari-mcp-windows-amd64.exe diagnostics --state-dir "C:\ProgramData\KomariMCP-Test\data\bridge" --output ".\komari-diagnostics.zip"
```

目录须使用实际 `BRIDGE_STATE_DIR`；示例路径不是强制配置。未传 `--state-dir` 时使用环境变量，缺省 `./bridge-data`。文件已存在会拒绝覆盖。

导出只包含版本/commit/系统架构、审计允许字段和最多 128 个会话的状态摘要；每会话保留最近 100 条命令元数据、标注省略数量。每份审计读取末尾最多 2 MiB，数据条目总计保留在 12 MiB 上限内（为 manifest 预留空间）；过大、损坏或无法读取的资料在 manifest 中标注跳过/截断，不能把这种包称为完整历史。

**默认排除**：管理员 API Key、调用 Key/Token、Cookie、配置和环境变量、命令正文/哈希、连续性 nonce、原始终端输出、cloudflared/系统日志。包仍有节点/会话/调用方标识等运维资料，只发给授权排查方，不公开发布。运行中的文件可能变化，是尽力采集的快照。不要把密码放入 command_id 等关联字段。

反馈时同时说明：发生时间和时区、节点名称、执行的操作类别、看到的状态、是否断线/重启、是否已手工重跑。具体命令如含密码先脱敏。如需 stdout/stderr、探针或代理日志，由部署 Agent 针对事件时间窗口单独取证、删除 Authorization/Cookie/URL Key/Client Token 等敏感信息；不要打包整个目录、数据库或配置。

## 面板与被控端断线的处理

| 场景 | 当前保护 | 边界 |
| --- | --- | --- |
| AI ↔ 桥接断开 | 常驻 daemon 继续读输出，后续用游标读 | 必须保持 daemon/存储运行；不是临时脚本 |
| 面板 ↔ 探针断开 | 保活、有限退避＋抖动、尽力重新附着；记录缺口 | 不能恢复没收到的日志，保留窗口不是永久保证 |
| 派发时断线 | 先保存 command_id/命令哈希和未确认状态；无法确认就未知 | 字节写入成功不证明远端执行，记录 ID 不提供端到端恰好一次 |
| 命令已完成，结束帧丢失 | 保留不确定状态，不自动重发，阻止下一条普通命令 | 看不到退出标记不能报 0；原 shell 可能已空闲，但不能随便注入探测污染前台程序 |
| 相同 command_id 再调用 | 返回原记录/继续读，相同 ID 改命令会拒绝 | 删除状态目录、换 ID 或手工新开会话重跑不受这一机制保护 |
| 恢复了相同 request_id | 空闲时验证 nonce＋shell PID，验证失败标记上下文丢失 | ID 相同不保证原 shell；忙时保持 attached_context_unverified |
| 工具等待结束 | 返回当前状态，任务继续运行 | 不等于取消，也不证明失败 |
| 认证失败/过期/恢复超窗口 | 明确错误/失效，不无限快速重试 | 不自动换身份、降级到任务 API/SSH 或重跑命令 |

对不确定会话，可读状态/已有日志、请求尽力 Ctrl+C，或明确关闭；不能把中断请求视为已经停止。不要为了网络恢复杀整个探针或节点其他进程。网络保护减少重复执行造成的风险，无法保证任意 root shell 命令不会损坏设备；安装、迁移和修改文件应有命令自身的幂等检查、备份与可审查的回滚步骤，面板不能推导任意命令的通用补偿。

## 可选增强（当前未实现）

- 命令级远端完成记录：执行结束后保存结果，经文件接口核查，能减少“END 丢失但已经结束”的未知状态；`exit/exec/set -e`、磁盘/权限问题仍可能没有记录。查询必须避开向未知前台程序注入 shell 输入。
- 探针端持久任务 ID/输出序号/回放：可覆盖更多断线/重启场景，需单独验证协议、存储和清理；业务副作用仍不因消息去重而自动成为恰好一次。
- 对长期任务采用受控后台任务、结果文件和应用级检查点；不默认安装 tmux 或另一个探针。

2026-10-07 阅读的官方设计资料：微软 [Retry pattern](https://learn.microsoft.com/en-us/azure/architecture/patterns/retry)、[Idempotent Consumer](https://learn.microsoft.com/en-us/azure/architecture/patterns/idempotent-consumer)、[Circuit Breaker](https://learn.microsoft.com/en-us/azure/architecture/patterns/circuit-breaker)、[Compensating Transaction](https://learn.microsoft.com/en-us/azure/architecture/patterns/compensating-transaction)。实际阅读来源是 MicrosoftDocs/architecture-center 的 retry-content.md、idempotent-consumer.md、circuit-breaker.md、compensating-transaction.md 和 transient-faults.md；retry.md 不存在，已依官方 retry.yml 跳转读取正文。共同要求：区分暂时故障/永久失败，有限重试、记录关联 ID，副作用必须考虑幂等，补偿是业务特定操作，不能盲目自动回滚。
