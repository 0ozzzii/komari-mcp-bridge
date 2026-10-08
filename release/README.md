# 与官方部署方式对齐

本项目保留 Komari 的面板、探针主动回连、终端中继与服务管理方式；新增常驻 MCP 桥接、原后台 MCP 菜单、权限管理及探针资源/连接优化。编译发生在 GitHub Actions，正式部署设备下载成品或拉镜像，不必安装 Go、Node、Zig，也不必拉源码现场构建。

默认从 [最新正式版（latest）](https://github.com/0ozzzii/komari-mcp-bridge/releases/latest) 下载成品，或使用本项目三个组件的 `:latest` 镜像。GitHub 的 latest 是入口，实际发行版仍有固定版本号；部署时先解析并记录实际 tag，再从同一 tag 下载程序与校验文件，避免发布切换时混用版本。旧版本与快照保留在 [Releases](https://github.com/0ozzzii/komari-mcp-bridge/releases)，回退时指定原 tag。

**只有发布工作流成功并上传完整附件后，成品才可安装。** 不把本地 `Snapshot-local-validation` 或构建缓存当作正式发行版；最新代码合并也不等于已发布新的 latest。

私有开发库为 `0ozzzii/komari-mcp-bridge-dev`，公开分发库为 `0ozzzii/komari-mcp-bridge`。安装来源可配置，审核密钥只在 dev 配置。源码、Git 历史、镜像和 Release 附件分别检查，见[公开分发说明](PUBLIC_DISTRIBUTION.md)与[同步教程](https://github.com/0ozzzii/komari-mcp-bridge/blob/main/release/PUBLIC_SYNC.zh-CN.md)。本轮不新增自动推送工作流。

## 入口与官方保持一致

| 项目 | 官方习惯 | 本项目 |
| --- | --- | --- |
| 面板二进制 | GitHub Release，`komari-<os>-<arch>[.exe]` | 相同命名，公开分发源 `0ozzzii/komari-mcp-bridge` |
| 探针二进制 | Release，`komari-agent-<os>-<arch>[.exe]` | 相同命名，包含资源统计、连接、执行保护及 Windows Shell 适配 |
| Linux 探针安装 | 面板生成命令，下载 `install.sh`，安装并注册服务 | 保留参数/安装目录/服务模型，脚本和下载源都指向本仓库 |
| Windows 探针安装 | `install.ps1` 下载 exe，NSSM 托管 | 保留官方模型，先核对 SHA256，再处理原服务 |
| Linux 面板安装 | `install-komari.sh` 菜单、`/opt/komari`、systemd | 保留模型；只提供本发行版，避免误切 Lite/官方无 MCP 版本 |
| 面板容器 | 预构建二进制装入官方 Alpine 镜像 | `ghcr.io/0ozzzii/komari-mcp-bridge:<TAG>` |
| 探针容器 | 官方探针镜像、容器标记、已有参数 | `ghcr.io/0ozzzii/komari-mcp-bridge-agent:<TAG>` |
| MCP 附加进程 | 官方没有该组件 | `komari-mcp-<os>-<arch>[.exe]` 或 `...-mcp:<TAG>`，无需现场编译 |
| 探针自更新 | 官方 `update` 包查询 Repo 和版本 | 复用原更新机制，构建时将 `update.Repo` 固定为本仓库 |

Windows 面板/桥接下载同架构 exe，用测试环境已有的 Windows 服务托管工具运行；没有把 Windows 安装伪装成 systemd。不要替换 Windows 测试节点 原有探针或共享 cloudflared 服务。

默认发布矩阵：面板沿用官方 8 个 Linux/Windows 目标；探针沿用官方 14 个 Linux/Windows/macOS/FreeBSD 目标，桥接同样构建这 14 个目标。交叉编译通过不等于每个平台运行已验收。Termux/Android 原生运行未因 Linux/arm64 编译成功而得到保证。

## 构建、发布、安装是独立步骤

1. 推送代码或创建 PR：`build.yml` 检查前端、面板、桥接、优化探针和真实 loopback 终端链路；构建成品、检查完整矩阵并生成 `sha256sums.txt`、`release.json`。
2. 开发/隔离验收通过后，维护者创建并发布 GitHub Release。正式 tag 使用 `vX.Y.Z`；快照使用 `Snapshot-*` 并设为 prerelease。遵循原探针语义版本更新规则，禁止使用任意测试字符串冒充稳定版本。
3. `release.yml` 复用完整构建，打包三个预构建容器镜像，再附加经校验的全部 Release 文件。只有 GitHub 当前 latest 正式版才提升镜像 `latest`；快照不覆盖稳定入口。已有资产不默认覆盖。
4. 已部署服务不会因为代码合并或新 Release 而由这个工作流自动更新。探针若用户原先启用了官方自动更新选项，它仍按原选项检查**本项目已发布版本**，容器仍按官方方式更新镜像。

GitHub `release.published` 事件沿用官方流程。若某个构建/上传失败，Release 可能暂时没有完整附件；检查 Actions 和完整文件清单，修复后再部署。不同仓库/镜像权限由 GitHub 管理；公开 Git 仓库不会自动把私有 GHCR package 改为公开。没有为这项工作自动改变仓库或 package 可见性。

### 默认 latest 与固定版本下载

普通安装无需手填版本：Linux 安装器未指定版本时、Windows 安装器未指定 `--install-version` 时，均解析本仓库 GitHub `releases/latest`。Linux 面板安装菜单选择正式版；探针继续使用面板生成的安装命令。面板、探针、桥接的手动下载入口同样是上面的最新正式版页面。

单个最新成品也可使用 `https://github.com/0ozzzii/komari-mcp-bridge/releases/latest/download/<官方兼容文件名>`。实际安装请先解析固定 tag，再按以下地址下载整组程序和校验文件。容器初次部署可用 `:latest`，升级时记录实际镜像 digest，固定 tag/digest 用于回退；已有容器不会因为标签变化自动重新启动。

面板、探针、桥接都使用：

```text
https://github.com/0ozzzii/komari-mcp-bridge/releases/download/<TAG>/<官方兼容文件名>
https://github.com/0ozzzii/komari-mcp-bridge/releases/download/<TAG>/sha256sums.txt
```

安装脚本可从**已确认 tag**的 raw 路径下载：

```text
https://raw.githubusercontent.com/0ozzzii/komari-mcp-bridge/<TAG>/install-komari.sh
https://raw.githubusercontent.com/0ozzzii/komari-mcp-bridge/<TAG>/install.sh
https://raw.githubusercontent.com/0ozzzii/komari-mcp-bridge/<TAG>/install.ps1
```

面板 Release 内的前端自动使用该构建 commit 的脚本和该 tag 的探针/镜像。三处安装对话框均来自 `agentRelease.ts`，不再混用官方仓库链接。Linux 探针保留 `--install-dir`、`--install-service-name`、`--install-version`、`--install-ghproxy`、`--install-no-mirror` 等参数；节点 Client Token 仍由本面板生成，不能用 MCP Key 替代。

安装器先下载临时文件并比对 SHA256；校验失败不替换原二进制、不先停止旧服务。SHA256 与文件同由 Release 提供，用于完整性校验，不能代替可信发布身份。脚本不打印节点 Token 的完整启动参数。服务配置本身仍含节点凭据，要保护目录和文件权限。旧程序备份不等于数据库迁移回滚；升级面板前应备份数据库，数据库降级须单独评估。

## 国内节点与域名接入注意事项

- 探针使用面板提供的、已经验证可用的公网 **HTTPS 地址**，通常为 `https://panel.example.com`（标准 443）。不要擅自改成 HTTP、添加面板内部监听端口，或临时套 SSH 端口转发；内网监听地址与对外接入地址是两回事。已验证探针可以直连时，沿用直连路线。
- 国内云服务商可能对未备案域名实施接入限制。遇到 `403` / `Non-compliance ICP Filing`，先核对 endpoint、反向代理和响应来源，再按服务商备案或接入要求处理。**HTTPS/443 并不保证免于备案限制**；不要仅凭一次成功就承诺其他节点、网络或服务商均可用。
- 保留 TLS 证书校验。Android/Termux 出现证书错误时，先检查时间、证书链和运行环境的 CA 信任库；`-u` / `--ignore-unsafe-cert` 会跳过证书校验，不是 Android 的默认必选项。只有确认 DNS 配置有问题时才配置可达的 `--custom-dns`，不要无条件覆盖所有设备的 DNS。
- 验证实际探针的注册/上报和终端 WebSocket，并单独验证 MCP。浏览器能打开面板、一次 HTTP 请求成功，都不足以证明终端链路可用；Cloudflare Access 等额外认证也可能影响探针。

这些是接入排查原则，不自动修改用户的域名、代理、端口、证书或现有服务；网络延迟和 TLS 协议版本以各环境实测为准。

## 本版来源与声明

本版是独立维护的 Komari MCP 适配版本，保留上游署名与许可；不是官方发行版。来源、修改范围与前端许可现状见 [ATTRIBUTION.md](../ATTRIBUTION.md)。Release 附件及发布镜像同时保留 LICENSE、LICENSE.komari-agent、NOTICE 和 ATTRIBUTION.md；升级、重打包或二次分发不要遗漏这些声明。

## MCP 是额外常驻组件，不改变被控端网络路线

`AI → 面板 /mcp/<调用方 Key> → 常驻桥接 → 面板管理员终端 WS → 探针终端 WS → shell/PTY`，返回反向经过同一面板中继。管理员密钥/私有控制口不发给 AI。桥接保持读取；一次工具返回不销毁终端。断线恢复仍是尽力恢复，不保证补回探针未缓存的输出。

- 原生二进制：桥接用 `komari-mcp serve`，环境配置见 Release 中 `komari-mcp.env.example` 和 `komari-mcp.service`。Linux systemd 示例使用独立普通服务用户；Windows 用既有服务托管方式。
- Docker：`release/compose.yml` 是面板＋桥接成品镜像示例，版本默认 `latest`，需要固定或回退时设置 `KOMARI_RELEASE_VERSION=<历史tag>`。仅面板 `127.0.0.1:18080` 映射到宿主机，桥接和控制口留在独立容器网络。先配置 control token，`docker compose up -d panel` 初始化面板；管理员在面板创建 API Key，安全写入私有环境配置，再启动 bridge。没有创建匿名控制入口，也不自动关闭 2FA。
- Windows 测试节点/Cloudflare：仍只需 `komari.example.com` → `http://127.0.0.1:18080`，面板与 `/mcp/...` 共用域名。不要对外发布 `8968/control`，不要改旧 SSH 路由。现有 Access/代理认证与 WebSocket 策略必须实际验证。

正式配置不能用占位符启动桥接；管理员 API Key 和控制 token 只在服务端私有配置中注入。网页创建的是独立调用方 MCP Key，不能拿仓库下载凭据代替。

## 自动同步官方更新

默认每日东八区 **12:00**（UTC 04:00）检查官方三个 `main` 的完整 commit SHA，也可手动运行 `sync-upstream.yml`。GitHub 定时任务可能排队延迟，不保证秒级准点。

- 面板从记录的官方基线做 Git 三方应用。原仓库导入的是源码快照，没有上游 Git 父历史；脚本根据真实 blob 合并，不伪造 ancestry。
- 探针/前端把我们的补丁在新官方提交上 rebase，刷新锁定 SHA 和可复用补丁。探针安装脚本自身也参与 rebase，保留本发布源和校验逻辑。
- 任一组件冲突/历史倒退：丢弃整个临时候选，只生成冲突报告和 draft PR；不能提交一半的新基线。定时任务没有 AI 自动修冲突功能。
- 有待处理的同步 PR 就不覆盖它，避免冲掉人工或开发 Agent 的修复。关闭或合并后，下一轮再检查。
- 自动生成 PR 不等于自动合并。使用 `GITHUB_TOKEN` 创建 PR 后不会自然触发常规 PR CI，任务显式 dispatch 构建工作流。
- 官方 `.github/workflows` 不自动覆盖本项目 CI，避免导入官方的自动合并/发布规则。相关变化由开发者另行审核。

工作流必须进入仓库默认分支，定时任务才会生效。GitHub Settings → Actions → General 需允许 Actions 创建 PR；CI/分支保护失败不绕过。不需要给每台探针配置 GitHub API Key，也不要求将私有仓库改为公开才能在 CI 构建。

## 内置主、备 AI 审查（推荐）

逐步点击配置见 [GitHub 主备 AI 审核配置教程](AI_REVIEW_SETUP.zh-CN.md)，包括 Secrets、Variables、权限、邮件和首次真实验证。

审查 Agent 是按需启动的 GitHub Actions 任务，不是探针上的常驻程序，也不运行在面板进程里。无 API 配置时保持 PR 等待。

在仓库 **Settings → Secrets and variables → Actions** 填以下项目。密钥只能放 Secrets，不写入聊天、仓库文件、普通 Variables、日志或 PR。

| 位置 | 名称 | 填写内容 |
| --- | --- | --- |
| Variables | `REVIEW_PRIMARY_BASE_URL` | 主接口 OpenAI Chat Completions 兼容 base URL，例如 `https://api.example.com/v1`，不含密钥或 `/chat/completions` |
| Variables | `REVIEW_PRIMARY_MODEL` | 主模型 ID |
| Secrets | `REVIEW_PRIMARY_API_KEY` | 主接口密钥 |
| Variables | `REVIEW_BACKUP_BASE_URL` | 备用接口 base URL |
| Variables | `REVIEW_BACKUP_MODEL` | 备用模型 ID |
| Secrets | `REVIEW_BACKUP_API_KEY` | 备用接口密钥 |
| Variables | `UPSTREAM_AI_AUTOMERGE` | 明确填 `true` 时才允许 AI 批准后合并；不填则只审查 |

只需要配置兼容接口，不需要额外 GitHub PAT。当前适配的是 `/chat/completions`，不是宣称支持所有厂商原生协议；不支持该协议的供应商需要单独适配。连接必须 HTTPS，拒绝跳转转发凭据。

流程为 `定时检查 → 同步 PR → 完整 CI → 主模型审核 → 必要时备用 → 确定性合并检查`：

1. CI 通过后，受信任默认分支的审查脚本把 PR diff 与三个官方源码增量作为**数据**交给模型；不执行 PR 代码、不把 API Key 放进提示词，也不把文件注释当作指令。
2. 仅主接口故障、限流、超时或无法返回有效结构化审查时切备用；主模型拒绝/不确定不会找备用模型推翻结果。
3. 模型输出必须绑定当前 SHA。发现中/高/严重问题、上下文超过 180000 字节、材料不完整、两接口失败或结果不确定：不合并。不截断差异后假装完整审查。
4. 审查 JSON 保存为本轮 Actions 的 `upstream-ai-review` artifact，保留 30 天，包括主/备用选择和失败类型，不含密钥。
5. 独立合并 job 不接触模型密钥，再查 PR 是否仍为相同 SHA、基线是否变化、最新完整构建是否成功、是否 draft/冲突，以及当前主分支是否已包含在候选中；最后向 GitHub 提交带 SHA 的 squash merge。不满足条件就保持等待。
6. GitHub 分支保护仍有效，拒绝合并时不绕过。建议配置必须通过构建、分支必须保持最新；若规则要求独立 GitHub 账户批准，内部创建 PR 的 bot 不能自己批准，需要下面的独立审核身份方式或由仓库维护者批准。

模型审核有漏报可能，不是形式化证明；复杂改动、不确定结果交给开发 Agent。默认不会让模型修改代码、运行 shell、修复测试或批准发布。要停自动合并，删除/改为非 `true` 的 `UPSTREAM_AI_AUTOMERGE` 即可；要停模型调用，清空模型 Variables 或禁用审查 workflow。

首次接入 API 后，可对一个隔离测试同步 PR 手动执行 `Review upstream candidate...`，输入完整 head SHA，先保持自动合并关闭，看审查 artifact 是否正常；通过后再开合并。这一真实 API 测试尚未在本环境进行。

### 已有外部审核 Agent 的可选接口

仍可采用 GitHub 原生 API，不必新增匿名合并入口：

- Variables `UPSTREAM_REVIEWER_LOGINS`：允许审核的 GitHub 用户/专用 GitHub App bot 登录名，逗号分隔。
- 可选 Variables `UPSTREAM_REVIEW_WEBHOOK_URL` + Secrets `UPSTREAM_REVIEW_WEBHOOK_TOKEN`：HTTPS 通知入口。消息只有仓库、PR URL、head SHA 和状态，不含合并凭据。
- 外部 Agent 用自己的 GitHub 身份审查，并针对该提交提交 `APPROVED` 或 `CHANGES_REQUESTED`。内置 `agent-review-merge.yml` 再检查身份和 CI 后合并。
- Webhook 通知不等于授权；没配置 reviewer 身份不会合并。通知失败不会造成放行，可直接在 GitHub 查看 PR。

## 发布和部署前验证

本地回归覆盖安装失败保留、三方冲突保护、幂等补丁准备、版本/附件完整性、主备切换规则、固定 SHA 审核和 CI 合并限制。终端回归覆盖原面板＋优化探针＋MCP 的真实 loopback PTY 链路。不能将这些结果写成 Windows 测试节点/Cloudflare 或第三方模型实测。

发布前还要看到 GitHub 完整矩阵、镜像推送与附件校验成功；部署前在隔离环境验证真实 Windows 服务、前端菜单、探针安装、节点授权、持续 cd/export、流式输出、非零退出、中断、断线不重发、单域名 WebSocket/MCP、日志缺口和资源上限。不要对生产做断网/重启验收。

升级前记录 tag/commit/`release.json`、备份数据库和桥接 state/config；仅替换本项目测试服务与路由。已有终端和在途命令的处理需提前确认，不能声称保存元数据即可恢复 shell 或网络对象。

## 上游依据

核对日期：2026-10-07。面板基线 `39cb3f59b2837e8d052203bfbbf3187888e5fa4c` 的官方 Release 工作流和 Dockerfile；探针基线 `828afafe7fb5b1b58c1232094e1e46320719016e` 的 Release 工作流、安装器及 update 包。前端基线 `0321789bc1989e53df729dfc98bed2a2800c39c6`。未来基线以 `release/config.json` 和每次 PR/发行版的 provenance 为准。

## 请求节奏、429 和邮件通知

| 环节 | 触发/频率 | 失败时行为 |
| --- | --- | --- |
| 官方检查 | 默认每天东八区 12:00（UTC 04:00）一次 | 网络/同步失败生成故障记录，不更新运行设备 |
| 待处理同步 PR | 同一时间保留一个，已有 PR 不被定时覆盖 | 冲突保留 draft，需要开发 Agent 解决 |
| 构建 | 创建候选后运行全部矩阵和回归 | 失败不调用模型合并 |
| 模型审查 | CI 通过或手动针对固定 SHA 重试；整个仓库同时只跑一个审查工作流 | 过期 SHA、非同步 PR 不发送模型请求 |
| 主接口 | 默认 6 RPM，严格同步串行，单次网络等待最多 90 秒，最多 3 次尝试 | 429/408/网络故障/部分 5xx 做退避，401/403/配置错误不重复轰炸 |
| 备用接口 | 主接口完成有限重试仍失败后，串行尝试，最多 3 次 | 共享每分钟请求间隔，两组失败保持不确定、不合并 |
| 总审查等待 | 模型请求阶段最多 8 分钟，工作流有 10 分钟保护 | 不会无限重试；GitHub 排队及读取审查材料不算模型请求并发 |
| 合并 | 当前 SHA 审核通过＋构建成功＋无冲突＋明确开启自动合并 | 只合并代码，不发布版本、不操作设备 |

`REVIEW_RPM` 可在 Actions Variables 设置为 1～10，默认 6。GitHub concurrency 对本仓库的该审查流程实行全局并发 1，包括手动运行；主备不会并行。GitHub 排队可能保留最新待运行事件而替换更旧的待运行事件，旧的已运行审查不会被主动取消，脚本仍检查 PR 的最新 SHA。这个限制不控制你在别的项目里使用同一个第三方账号的请求。

429 优先遵守 `Retry-After`（支持秒数和 HTTP 日期）；没有该头时采用带抖动的退避和 RPM 间隔。若服务端要求等超过 120 秒，改用备用接口，而不是提前再次访问被限流的主接口。主模型有效拒绝或不确定不触发备用翻案。429 重试用尽、接口欠费/凭据错误、上下文过大或两接口失效均保持 PR 等待；在报告中区分接口故障和代码审查意见。

用户已选择 **故障 Issue＋GitHub 自带邮件**，无需 SMTP 密钥。同步、构建、模型接口/不确定结果、审核拒绝、合并失败/暂缓分别复用一个未关闭 Issue。模型审核拒绝会创建独立提醒，不能当作合并恢复；审核通过但因主线变化等未真正合并，也会提醒。

每个新失败运行在既有 Issue 留一条带 Actions 链接的评论，触发 GitHub 通知；同一运行重复处理不会重复评论，也不会每次 429 重试都发提醒。接口恢复只关闭接口故障，审核通过才关闭拒绝提醒，实际合并完成或 PR 已关闭后才关闭合并提醒。个人仓库通知其 owner；可设置 `UPSTREAM_NOTIFICATION_MODE=off` 暂停自动故障通知。

邮件必须在 GitHub 开启：仓库右上角 **Watch → Custom → Issues**（或 All Activity），账号 **Settings → Notifications** 中开启相应 Email 通知，将通知邮箱设为期望的已验证邮箱。任务不会读取你私有邮箱、注册 SMTP 或保证任意账号偏好下都有邮件。Webhook、Issue、GitHub 邮件以及真实第三方 API 都还需要接入后验证；本轮单元测试验证的是故障策略和 Issue/运行提醒去重逻辑。

### 故障处理

| 现象 | 怎么处理 | 如何恢复 |
| --- | --- | --- |
| 429 | 查看 artifact 中主/备用及次数；降低 `REVIEW_RPM`、检查该供应商额度和同账号其他调用 | 不改 PR 内容时，手动运行 AI review workflow，填当前完整 head SHA |
| 401/403、欠费 | 在 Actions Secrets 更新对应 API Key，核对供应商账户权限/余额 | 不要把 Key 发到 Issue 或聊天；配置修好后手动重审 |
| 404/模型不存在 | 核对 base URL 是兼容 `/chat/completions` 的 `/v1` 地址、模型 ID 和访问权限 | 修正 Variables 后重审 |
| 两接口都失败/超时 | 查 provider failure 类型；确认接口连通、配额；等待供应商恢复 | PR 未合并，旧部署不受影响；恢复后手动重审 |
| 模型拒绝 | 查看 findings，交给开发 Agent 修复，不用备用模型绕过拒绝 | 推送新提交→重新构建→针对新 SHA 重新审查 |
| 模型不确定/差异过大 | 不默默截断；让开发 Agent 拆分或人工/外部 Agent 审查 | 每份提交完整通过构建与审查；必要时走独立审核身份方式 |
| 同步冲突/draft PR | 按 `release/upstream-report.md` 处理三方冲突；保护 MCP/容器/连接优化与安装来源 | 修复候选，刷新/验证补丁，标为 ready 后重新构建；不能只改 report 假装已修好 |
| 构建失败 | 看实际失败的架构/测试，检查新的 Go/前端依赖要求 | 修复后重新运行构建；禁止删除测试或用官方原版替换优化版来放行 |
| 分支或 head 改变 | 旧审批不算新代码的审批 | 更新候选使其包含当前主分支，再构建、审核新的 SHA |
| 分支保护拒绝合并 | 查看 GitHub 规则；可能要求独立身份审查 | 使用专用 GitHub App/外部 reviewer 或维护者操作，不绕过保护 |
| PR/Issue 无法创建 | 仓库可能关闭 Issues，或 Actions 不允许创建 PR | 在仓库设置中启用对应能力；查看保留的 Actions artifact |
| 没收到邮件 | 检查仓库 Watch、通知过滤、邮箱验证、邮件垃圾箱 | 先验证 Issue 已创建；通知设置问题不通过暴露账号邮箱解决 |
| 合并但没有新 Release | 这是设计行为：代码更新不等于发行/部署 | 验收后按发布步骤创建 Release；再在隔离环境部署成品 |

Windows 部署方可直接参考 [成品部署提示词](windows-deployment-guide.md)。桥接 Windows 状态保存保留文件 flush＋rename，不调用不支持的 POSIX 目录 fsync；断电持久性与 Unix 不作相同承诺，仍需 NTFS/ACL/服务实测。

## 配套执行保护版本

新的工作策略协商、共享槽位与探针侧执行期限需要本轮面板、桥接、探针配套。下载发行版沿用以上官方式流程，探针仍下载编译好的 `komari-agent-<os>-<arch>`，不在被控端现场构建。普通旧探针缺少保护 ACK 时不能派发受管理命令；请先隔离验收，不把仅握手成功当作终端可用。详见 [执行策略](../bridge/EXECUTION_POLICY.md)。
