# GitHub 主备 AI 审核配置教程

适用仓库：[0ozzzii/komari-mcp-bridge](https://github.com/0ozzzii/komari-mcp-bridge)。本教程按仓库实际的 `ai-review.yml`、`sync-upstream.yml` 和审核脚本编写。

审核 Agent 运行在 GitHub Actions：每天北京时间中午12:00检查官方更新，有更新才创建候选 PR、运行完整 CI，再调用模型审核。GitHub 可能延迟调度。没有更新就不需要调用模型。模型审核通过且开启自动合并后，代码可合入 main；**合并不会自动发布 Release，也不会替你更新服务器。**

## 1. 允许仓库运行工作流和创建 PR

1. 打开仓库，点击顶部 **Settings**。
2. 左侧点击 **Actions → General**。
3. 在 **Actions permissions** 允许此仓库使用现有工作流需要的 Actions；如果仓库已有正常配置，不必扩大组织策略。
4. 在 **Workflow permissions** 勾选 **Allow GitHub Actions to create and approve pull requests**，点击 **Save**。
5. 工作流已经分别声明需要的 `contents`、`pull-requests`、`actions`、`issues` 权限，不需要创建“全部权限”的 PAT。组织策略或分支保护不允许时，按报错处理，不绕过限制。
6. 仓库 **Settings → General → Features** 保持 **Issues** 开启，用于故障通知。

模型 API Key 只用于调用第三方模型，不能代替 GitHub 权限。面板管理员 Key、探针 Client Token、MCP 接入 Key 都不填在这里。

## 2. 填主、备用 API 密钥：Secrets

1. 在仓库 **Settings** 左侧打开 **Secrets and variables → Actions**。
2. 选择 **Secrets** 页签。
3. 点击 **New repository secret**。
4. 在 **Name** 填下表名称，在 **Secret** 粘贴对应供应商的密钥，点击 **Add secret**。
5. 对备用密钥重复一次。主、备用可以是不同供应商或不同账户。

| Name | Secret 内容 |
| --- | --- |
| `REVIEW_PRIMARY_API_KEY` | 主模型供应商 API Key |
| `REVIEW_BACKUP_API_KEY` | 备用模型供应商 API Key |

不要把密钥填在普通 Variables、仓库文件、Issue、聊天或 URL 中。后续换密钥，在该条右侧编辑更新即可。

## 3. 填接口地址和模型：Variables

仍在 **Secrets and variables → Actions**，切换到 **Variables** 页签。每项点击 **New repository variable**，填写 **Name** 和 **Value**，点击 **Add variable**。

| Name | Value 示例或要求 |
| --- | --- |
| `REVIEW_PRIMARY_BASE_URL` | `https://api.example.com/v1`，换成主供应商的实际兼容接口基地址 |
| `REVIEW_PRIMARY_MODEL` | 主供应商支持的准确模型 ID，例如 `<主模型ID>`，不要原样填占位符 |
| `REVIEW_BACKUP_BASE_URL` | `https://backup.example.com/v1`，换成备用供应商地址 |
| `REVIEW_BACKUP_MODEL` | 备用供应商支持的准确模型 ID |
| `REVIEW_RPM` | `6`，允许1～10；请求始终串行，并发为1 |
| `UPSTREAM_AI_AUTOMERGE` | 首次验证填 `false`；验证成功后改为 `true` 才自动合并 |

当前接口要求 **OpenAI Chat Completions 兼容协议**，脚本会在 base URL 后追加 `/chat/completions`。例如实际请求地址为 `https://api.example.com/v1/chat/completions`，BASE_URL 应填 `https://api.example.com/v1`。供应商若用其他兼容基路径，填它文档要求的基路径；不要重复追加 `/chat/completions`。必须是 HTTPS，不把 Key 放进 URL，也不跟随重定向发送凭据。仅支持原生 Anthropic/Gemini/Responses 等其他协议的地址不能直接假装兼容。

主模型有效拒绝或表示不确定时，不会用备用模型推翻结论。只有主接口技术故障、429限流、超时等情况，才有限重试并切备用；主备不并发请求。

`UPSTREAM_REVIEWER_LOGINS`、`UPSTREAM_REVIEW_WEBHOOK_URL` 和 `UPSTREAM_REVIEW_WEBHOOK_TOKEN` 是另一条外部 GitHub 审核身份路线的可选项。只使用本教程的内置主备 API 审核时，不用配置它们。

## 4. 开启 GitHub 邮件通知

不需要 SMTP 密钥，也不需要在仓库填写邮箱。

1. 回仓库主页，右上角点击 **Watch → Custom**。
2. 勾选 **Issues** 并保存；也可以选 **All Activity**，但通知会更多。
3. 点击 GitHub 右上角头像 → **Settings → Notifications**。
4. 在订阅和参与通知相关设置中启用 **Email**，核对通知邮箱是你希望收信的已验证地址。GitHub 页面名称可能随版本略变。
5. 确认垃圾邮件过滤没有屏蔽 GitHub。

同步失败、CI失败、模型调用失败、审核拒绝、合并失败或暂缓，会分别创建／更新故障 Issue；新的失败运行会评论并提及仓库所有者。同一次运行会去重，不会每次429都发邮件。邮件是否投递取决于 GitHub 账号通知设置，配置后仍须验证。

不要设置 `UPSTREAM_NOTIFICATION_MODE=off`，否则仓库故障通知会关闭。该变量默认不填即可。

## 5. 首次验证主备配置

1. 保持 `UPSTREAM_AI_AUTOMERGE=false`。
2. 打开仓库顶部 **Actions**。
3. 左侧选择 **Prepare official upstream update PR**。
4. 点击 **Run workflow**，选择 **main**，再次点击 **Run workflow**。
5. 查看运行结果：没有官方更新就不会产生新 PR；有冲突会保留 draft，先交给开发 Agent 修复；已有同步 PR 时不会覆盖它。
6. 有正常同步候选后，等它的 **Build and validate** 全部通过；内置模型审核会按工作流自动触发。
7. 打开 **Review upstream candidate with primary and backup models** 的对应运行，在 **Artifacts** 下载 `upstream-ai-review`，查看审核 JSON 中的 decision、供应商选择、调用失败类型和 findings。材料必须对应当前完整 head SHA。
8. 需要手动重审时，打开该审核工作流 → **Run workflow** →选择 **main**，填写同步 PR 当前的完整40位 **head_sha**。可让本地 Agent 执行 `gh pr view <同步PR编号> --repo 0ozzzii/komari-mcp-bridge --json headRefOid --jq .headRefOid` 获取；不要填 main SHA、短 SHA 或过期提交。
9. 确认真实 API 能返回有效结果，故障通知也符合预期后，把 `UPSTREAM_AI_AUTOMERGE` 改为 `true`。若当前候选已有 approve，可针对相同且未变化的完整 SHA 手动重审以触发合并检查。

这是专门审核机器人创建的 `upstream/official-sync-*` 同步 PR 的流程，不是任意开发 PR 的通用模型调用入口。不要拿普通开发 PR #5 当作该模型配置的有效验收；脚本会拒绝不符合身份／分支规则的候选。

GitHub 分支规则仍然生效。如果要求独立账号审批，内部 bot 不能替自己满足这一要求，需要已授权外部审核身份或维护者审批。不要为了自动化删除分支保护。

## 6. 日常运行和故障处理

| 看到什么 | 怎么处理 |
| --- | --- |
| 429、限流 | 自动有限退避／备用；检查账号配额与其他项目调用，必要时降低 REVIEW_RPM，然后手动重审当前 SHA |
| 401／403、欠费 | 更新对应 Actions Secret，检查供应商权限和余额；不把密钥贴到日志里 |
| 404、模型不存在 | 核对 BASE_URL、兼容协议、MODEL ID 与模型访问权限 |
| 审核 request_changes | 阅读 findings，让开发 Agent 修复，再对新提交构建、审核；不换模型绕过拒绝 |
| uncertain、差异过大 | 保持未合并；交给开发 Agent 完整处理或拆分，不截断材料后当作通过 |
| 同步冲突、draft | 查看候选 PR 的 release/upstream-report.md；修复后重新构建、标记 ready |
| 合并 held／失败 | 核对 main 或 PR SHA 是否变化、CI与分支规则；旧审批不能用于新代码 |
| 没收到邮件 | 先看故障 Issue 是否创建，再检查 Watch、Email、已验证邮箱和垃圾箱 |
| 合并后没有新版下载 | 合并只更新源码；Release仍须单独发布，已有设备不会因此自动重启 |

停止自动合并：把 `UPSTREAM_AI_AUTOMERGE` 改为 `false`。停止模型调用：在 Actions 禁用 **Review upstream candidate with primary and backup models**。停止每日源码同步：禁用 **Prepare official upstream update PR**。无需删除现有发布版本或停止面板。

首次接入前，代码／本地模拟测试通过不代表你的第三方 API 和邮箱已经实测成功。按第5节完成一次真实同步候选的审核，再开启闭环。
