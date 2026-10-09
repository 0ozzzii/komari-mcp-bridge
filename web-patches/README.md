# 原后台一级 MCP 菜单

用户明确要求在原 Komari 管理后台“账户”下面增加一级 **MCP** 菜单。原导航插件会进入“插件”子菜单，因此本版采用小范围前端补丁，不需要安装导航插件。

基线：官方 `komari-web` 完整提交 `0321789bc1989e53df729dfc98bed2a2800c39c6`。

只改三处：

| 文件 | 增量 |
| --- | --- |
| `src/config/menuConfig.json` | 在账户与底部文档之间增加一级 MCP，终端图标 |
| `src/routes.ts` | 在现有管理员布局下增加 `/admin/mcp` 子路由 |
| `src/pages/admin/mcp.tsx` | 保留原后台侧栏，在右侧同源嵌入现有 `/api/admin/mcp` 管理页 |

网页仍沿用原管理员登录、Cookie 和敏感操作 2FA；前端没有嵌入任何 Key。工具调用仍走 `/mcp`，与网页 `/admin/mcp`、管理 API `/api/admin/mcp` 分开。面板总开关、多个 Key、节点及工具授权由已有管理页/桥接处理。

Linux 开发环境执行 `bash web-patches/prepare.sh /path/to/komari-web`，再进入前端目录 `npm ci && npm run build`。准备脚本锁定基线、验证重复应用，不覆盖已有冲突。`dev/setup.sh` 已包含这一步。

Windows 部署 Agent：在固定官方前端 checkout 使用 `git apply --check` 再 `git apply` 本补丁；先检查 HEAD 与已有修改，不使用 reset/clean 覆盖 Windows 修复。已应用时通过 `git apply --reverse --check` 确认，不再次应用。执行 `npm run build`，按原 `dev/setup.sh` 形式把实际 `dist` 打成 `dist.tar.zst` 后编译新的 Windows 面板。也可使用本次交接的已验证前端资源包，核对 manifest 哈希。

更新已安装的 Windows 测试节点 测试面板时，保留其数据库、服务工作目录、凭据、主题和 Windows 兼容修复；先构建验证新文件，再仅替换并重启新测试面板服务。不要重新初始化安装，不更换探针或调整 CF 路由。旧 `mcp-tools` 插件如已安装可停用，避免重复菜单。自定义首页主题与本后台菜单无关；不要切换用户的首页主题。

验收：管理员登录后 MCP 是账户下面的一级菜单；点击后 URL 为 `/admin/mcp`，原侧栏保留，右侧能显示服务开关、Key 和节点权限。刷新直接进入、返回服务器列表、未登录跳转登录、MCP 管理 API 未授权拒绝均应验证。

新增 `0002-agent-release-source.patch` 将三处探针安装命令与 Docker 镜像来源统一到本仓库，并在 Release 构建时注入脚本 commit 与探针版本。当前生效的基线和补丁清单以 [release/config.json](../release/config.json) 为准；自动同步会刷新它们。

新增 `0003-mcp-management-layout.patch`：内嵌管理页用 ResizeObserver 自动调整高度，由原后台容器承担页面滚动，避免双重滚动条。更新时必须重新构建前端资源和面板（Go 内嵌管理页也有变化），不能复用旧前端包。

新增 `0004-custom-release-identity.patch`：后台显示 **Komari MCP**，版本仍来自本项目构建时注入的 tag/commit；更新提示只查询本项目公开仓库，不与官方版本线比较。默认发行库 `0ozzzii/komari-mcp-bridge`，可在构建时用 `VITE_KOMARI_PANEL_REPOSITORY` 指定；未设置则沿用 `VITE_KOMARI_AGENT_REPOSITORY`。只提示有面板二进制、`release.json` 和 `sha256sums.txt` 的正式发布，忽略草稿、预发布和无安装资产的发布。更新按钮打开该发行库的 Release；上游源码同步与面板安装更新是两个独立流程。须重新构建前端和面板后生效。
