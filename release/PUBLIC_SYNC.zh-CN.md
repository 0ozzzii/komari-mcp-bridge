# 私有开发与公开发行同步

固定身份：私有开发库 `0ozzzii/komari-mcp-bridge-dev`，公开分发库 `0ozzzii/komari-mcp-bridge`。主备模型审核配置填在 **dev 仓库**，公开库只提供脱敏源码与同版成品。

## 源码同步

1. 私有 main 的 CI 成功后，记录完整40位来源 SHA。
2. 在私有 checkout 执行 `python3 release/public-distribution.py export --commit <完整SHA> --directory <尚不存在的新目录>`。它从指定提交导出，不读取本机未提交配置，不复制 `.git`、私有历史或其他分支；排除 `.github/workflows/`，执行凭据文本扫描，写入来源与逐文件 SHA256。
3. 克隆公开库到独立临时目录，先确认工作区干净。只将上述导出树同步进去，以公开 main 为父提交，再正常提交和推送；不 force-push，不把 dev 分支整体镜像过去。公开库有新改动时先核对，不能盲目覆盖。个人交接分支不合入 dev/main，也不参与导出。
4. 推送后核对 `release/distribution.json` 的 `source_commit`、文件集合与 SHA256。公开提交 SHA 与私有 SHA 不同是正常的；二进制仍记录真实私有构建 SHA。

脚本不会推送或创建自动化工作流。本轮保留手动同步入口，不引入之前暂不需要的自动推送流水线。

## 发行成品同步

1. dev 中对应正式 Release 的 **Official-aligned release binaries and images** 必须完整成功。下载同 tag 的全部附件，不在目标设备重编译。
2. 在匹配来源提交的源码树执行：`python3 release/public-distribution.py verify --directory <附件目录> --version <固定tag> --commit <完整SHA>`。它校验完整矩阵、允许的附件名、SHA256、manifest、干净来源及仓库标识；拒绝日志、真实配置、未知文件、重复或穿越路径的 checksum。旧版本使用该版本的 `release/config.json`，可传 `--config <该版配置>`，不修改旧成品或校验值。
3. 在公开库创建同 tag 的 **草稿**，目标为对应脱敏快照。逐个 `gh release upload <tag> <文件> --repo 0ozzzii/komari-mcp-bridge`。已存在同名附件先下载核对，一致则跳过；不一致停止，不无条件覆盖。
4. 核对公开附件集合、大小、SHA256 完全一致后，才执行 `gh release edit <tag> --repo 0ozzzii/komari-mcp-bridge --draft=false --latest`。未完成不发布空壳 latest。
5. 不登录 GitHub，重新下载目标系统的成品验证；三个 GHCR 包的固定 tag 与 latest 另查匿名拉取和 digest。发布不等于设备已更新。

历史 v1.0.2 的 manifest 保留发行时仓库名：源码原名 `komari-mcp-bridge`，分发原名 `komari-mcp-bridge-release`。仓库改名不改变二进制来源 SHA；结合 dev 仓库的提交和公开 `distribution.json` 核对，不改历史附件伪造新构建。

## 上传凭据

当前云环境 Git／仓库 API 可用，不表示 `uploads.github.com` 已获得认证。实际上传若返回401，可在云环境设置的 `KOMARI_GITHUB_RELEASE_TOKEN` 字段安全填写专用 GitHub fine-grained PAT：只选 **公开 komari-mcp-bridge 仓库**，Contents 读写，Metadata 读取。无须私有仓库、Secrets、Administration 或全部账号权限。

该凭据只用于附件发布，不能当作面板、探针或审核模型 Key；不要贴进聊天、仓库或日志。保存并 Publish 后，确认绑定可用；上传命令可单次使用 `GH_TOKEN="$KOMARI_GITHUB_RELEASE_TOKEN" gh release upload ...`，不改其他 Git 操作的现有平台认证。如果本地 Agent 已有 GitHub 登录，可直接使用其正常 gh 身份完成，不需要新建 Token。

密钥限于实际需要的 `api.github.com` 和 `uploads.github.com`。403策略拒绝、401认证失败、权限不足分别处理，不关闭TLS、不绕过代理、不提取平台持有的凭据。凭据缺失时保留草稿并说明具体阻断。

## 脱敏边界

通用教程留在 main；个人设备名称、真实域名、配置备份、回显与诊断包留在私有交接分支或本机安全目录。文本扫描不代替图片、二进制、许可及历史资产检查。仅导出当前已审查树，不公开私有历史；模型 Secrets、私有工作流和真实日志均不随发行同步。
