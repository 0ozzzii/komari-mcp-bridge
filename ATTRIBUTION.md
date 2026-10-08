# 来源、修改范围与许可说明

本项目由 `0ozzzii/komari-mcp-bridge` 独立维护，基于官方 Komari 源码增加 MCP 接入及相关稳定性适配，不代表 Komari 官方发行、背书或维护承诺。

| 来源 | 许可／记录 | 本仓库修改范围 |
| --- | --- | --- |
| [komari-monitor/komari](https://github.com/komari-monitor/komari) | 原 MIT 许可见 [LICENSE](LICENSE)；原第三方声明见 [NOTICE](NOTICE) | 管理员 MCP 页面、认证代理与同域名接入；任务保护等增量 |
| [komari-monitor/komari-agent](https://github.com/komari-monitor/komari-agent) | 原 MIT 许可完整保留于 [LICENSE.komari-agent](LICENSE.komari-agent) | `agent-patches/` 的容器统计、连接和执行保护；安装／更新源适配 |
| [komari-monitor/komari-web](https://github.com/komari-monitor/komari-web) | 固定上游提交未提供独立 LICENSE 文件；不据此声称该组件受 MIT 授权。保留源码、依赖声明与来源，分发权限应以其作者提供的许可为准 | `web-patches/` 的 MCP 菜单、布局和探针安装源适配 |
| 新增桥接与适配代码 | 本仓库 LICENSE；第三方模块仍受其各自许可约束 | `bridge/`、发布和同步脚本、管理日志查看等 |

上游的完整提交固定在 `release/config.json`，发行版的 `release.json` 记录构建 commit、上游提交与资产校验值。补丁和本仓库 Git 历史用于追踪修改。未来上游许可变化应随同步检查，不把未注明许可当成无条件授权。

保留上游版权、MIT 许可正文和已有第三方 NOTICE，未将其改写为本项目独占原创。安装文档和镜像下载源指向本仓库，原上游名称用于说明来源与兼容关系。Release 附带 LICENSE、LICENSE.komari-agent、NOTICE 和本说明；三个发布镜像在 `/usr/share/licenses/komari-mcp-bridge/` 保留这些文件。依赖完整许可仍可从源码及其模块／锁文件对应版本核对。

项目及依赖按照各自许可提供，无额外稳定性保证；本说明不替代许可原文，也不承诺免于所有法律或运营责任。与本版增量有关的问题请提交本仓库 Issues，官方原版的维护渠道仍属于原项目作者。
