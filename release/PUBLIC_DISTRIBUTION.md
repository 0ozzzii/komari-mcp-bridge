# 公开分发与环境解耦

开发主库：`0ozzzii/komari-mcp-bridge-dev`（Private）。公开分发库：`0ozzzii/komari-mcp-bridge`（Public）。模型审核 Secrets 留在开发仓库，不复制到分发库、Release、镜像或节点。

`release/config.json` 分别记录源码仓库与 `distribution_repository`。安装脚本默认下载公开分发库，可用非秘密环境变量 `KOMARI_RELEASE_REPOSITORY=owner/repository` 覆盖；只接受仓库身份，不接受 URL 或凭据。探针构建通过 `RELEASE_REPOSITORY` 选择更新源；前端通过 `VITE_KOMARI_AGENT_REPOSITORY`／`VITE_KOMARI_AGENT_INSTALL_REF` 配置安装来源。覆盖时必须提供完整同版本成品，不能仅改链接假装发布成功。

面板与桥接端点使用 `KOMARI_LISTEN`、`KOMARI_BASE_URL`、`KOMARI_MCP_UPSTREAM_URL` 等配置。探针 `-e` 和 `-t` 由安装命令／服务配置提供，没有默认真实 Client Token。loopback、容器网络和默认端口是通用内部配置，不需要为脱敏删除官方默认端口。

容器与 GitHub 二进制下载分别配置：`container_repository` 保留既有 GHCR 包命名空间，前端可用 `VITE_KOMARI_IMAGE_REPOSITORY` 覆盖。源码仓库改为Private并不自动改变旧包可见性，也不能保证新包公开。发布时单独验证三个镜像匿名拉取与tag/digest，不把下载仓库名直接拼成尚不存在的新镜像。

文档与测试使用 `komari.example.com` 等保留域名。不要把示例域名当作可用服务，不用真实公网域名作 Mock Host。去掉设备别名不代表 Windows／Android 已端到端验收。

## 公开前检查范围

1. 检查最终跟踪文件、Release 附件、镜像层、打包配置和截图；不得包含个人 endpoint、真实节点 Token、模型 Key 或真实终端日志。
2. 通用教程保留在 `release/`；个人交接与导出日志放仓库外，或明确忽略的 `.private/`、`local/`。已跟踪文件不能靠补 gitignore 隐藏。
3. 主干清理**不会清理旧提交、Tag、旧附件、Actions 日志和缓存**。公开同步若带完整 Git 历史，也会带入先前资料；必须另行审核历史或采用经批准的干净导出策略。不擅自重写私有历史或删除历史 Release。
4. 私有 Release 需要访问权限；免鉴权安装须验证公开分发库同 tag 的附件、checksum 和 manifest 已齐全。源码镜像与成品发布是两件事。
5. 按[公开同步教程](https://github.com/0ozzzii/komari-mcp-bridge/blob/main/release/PUBLIC_SYNC.zh-CN.md)分别同步已审查源码和完整成品；提供可复用导出／校验入口，本轮不新增自动推送工作流。公开 Release 附件完整后才设为 latest。

可运行 `python3 release/check-public-tree.py` 检查当前跟踪文本中的高置信度密钥／凭据 URL 和私有运行文件。私有 CI 可通过 `PUBLIC_FORBIDDEN_HOSTS` 提供不应公开的域名列表；脚本仅报告文件与行号，不打印命中值。它不覆盖所有自定义 Token、历史提交、二进制内嵌字符串或图片像素，不能作为“绝无秘密”的保证。

许可证、NOTICE 和上游固定 SHA 必须保留。随机测试 nonce、测试身份和隔离 localhost 夹具不等于真实凭据；清理假数据不能代替秘密审计。

运行时仍要保护服务配置、进程参数和初始化输出。上游面板初始化／`chpasswd` 的本机输出可能包含管理员密码；这不是硬编码秘密，也不能因此把整段控制台日志上传。安装脚本隐藏探针参数和代理地址，NixOS配置提示只给私有参数占位符，不回显实际Token。
