# 探针增量补丁：容器统计与连接稳定性

基线为 Komari Agent `main` 提交 `828afafe7fb5b1b58c1232094e1e46320719016e`，容器验证日期 2026-10-05，连接稳定性验证日期 2026-10-06。本目录只提供源码补丁，不修改已安装探针或服务。

```bash
bash agent-patches/prepare.sh /workspace/komari-agent-dev
cd /workspace/komari-agent-dev
umask 022
go test -race ./monitoring/unit ./cmd/flags ./cmd ./monitoring ./terminal ./ws -count=1
go test -race ./server -run '^TestMonitorControlFramesRefreshReadDeadline$' -count=1
CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags='-s -w' -o /tmp/komari-agent-container-fixed .
```

准备脚本拒绝覆盖其他提交和已有修改；精确已应用的补丁可以重复检查。环境必须使用真正的 Go 1.25.0 编译器。本云环境为 `/workspace/.tools/komari-go1.25.0/go/bin/go`，缓存位置需要设置 `GOPATH=/workspace/.cache/go GOCACHE=/workspace/.cache/go-build`。`/usr/bin/go` 不是所需编译器。

新增 `--resource-scope auto|container|host`，对应环境变量 `AGENT_RESOURCE_SCOPE`，配置字段 `resource_scope`。默认 auto：沿用现有容器检测，在容器中选择容器统计；已有显式 `HOST_PROC` 视为宿主机监控意图，可用 container 再次覆盖。

| 指标 | 容器模式行为 |
| --- | --- |
| 内存 | cgroup v1/v2 当前使用量；默认减去 inactive file cache，兼容已有包含缓存选项；可见祖先的有限限额取最小值 |
| CPU | cgroup 累计 CPU 时间增量 / 墙钟增量 / 有效 CPU 配额；配合 cpuset 和父级 quota；旧整数核心数字段向上取整 |
| Swap | v2 swap current/max；v1 memsw 减 memory；不可读取时为未知 |
| Load | 不把宿主机 loadavg 当容器负载；旧数值字段返回 0，scope/message 明确 unknown |
| 磁盘 | 不把共享 statfs 容量当容器配额；未显式 include-mountpoints 时返回未知 0/0；显式挂载点保留原统计，但它仍是文件系统容量 |
| Uptime | 内核启动时间，不宣称是容器运行时间 |
| 网络 | 当前网络命名空间；host-network 部署本身共享宿主机网络 |

未知限额不回退成宿主机容量。新 `resource_scope` 元数据旧面板可能忽略，现有 `message` 也附带统计边界说明；**旧面板仍可能把未知数值显示成 0，未新增专用未知状态界面**。GPU、硬件型号等设备信息仍遵循设备可见性，未实现完整容器设备归属追踪。

权限与监控范围分开：运行用户决定权限，cwd 是否为 `/root` 不决定权限。官方安装脚本使用 `${SUDO_USER:-$(id -un)}`；直接 root 安装通常 root，普通用户 sudo 安装可能仍指定普通用户。容器 root/sudo 只具有该容器授予的权限和命名空间，不会自动获得宿主机权限。

同样 Go 1.25.0、linux/amd64、CGO=0、`-buildvcs=false -trimpath -ldflags='-s -w'` 实测：原始源码 8,126,648 字节，修复版 8,151,224 字节，增加 24,576 字节。官方发布已裁剪符号，不能把重复裁剪说成进一步减小官方发行版。未对用户所说约 12 MB 的实际二进制做检查。

验证包含 cgroup v1/v2、namespace root、祖先限额、0.5 CPU 配额、cpuset、无限/损坏/缺失控制器、working-set 下溢和显式模式覆盖。都是本地文件夹具，不是用户容器实测。已有 MOTD 测试在继承 umask 077 时假定写入 0640 而失败，改用该测试预期的 umask 022 后通过；没有删除或弱化断言。

回滚：撤销本地源码补丁并重新构建原版本；线上切换和替换探针需要另行确认，当前未执行。

## 连接稳定性补丁

`0001-container-resource-scope.patch` 保持独立；`0002-connection-stability.patch` 追加监控读/Pong 期限、限时写与无锁关闭，以及终端独立保活、计时器代次、旧连接所有权、输入错误和 EOF 尾字节处理。overlay 增加心跳实现与回归测试，不增加第三方探针或远程执行权限。

`prepare.sh` 校验完整基线、三组补丁与 overlay；已有不匹配或只应用一半的改动会拒绝覆盖。对已有只应用容器补丁的工作区，请另建干净的基线 checkout 后应用两组，避免覆盖现有工作。

断线输出仍不缓存，不保证补回日志；Ctrl+C/销毁没有结构化 ACK，命令重发仍不允许。详细源码风险及本地实际 PTY 集成范围见 [桥接稳定性说明](../bridge/STABILITY.md)。上文 8,151,224 字节是此前仅容器补丁构建的历史测量，新两组补丁构建大小见最新验证记录。

本轮完整两组补丁同编译条件构建为 8,159,416 字节，比原始基线增加 32,768 字节，比此前仅容器补丁版增加 8,192 字节；未测量用户实际 12 MB 文件。监控 Ping/Pong 读期限刷新已用实际拨号器和 loopback 验证。

发布与自更新来源由 [release/config.json](../release/config.json) 和 [release/build.sh](../release/build.sh) 统一管理，复用官方 update 包并将 Repo 注入本仓库。自动同步 PR 会刷新基线及补丁清单；此文中的基线和测试体积是历史验证记录，不代表未来版本。

## 执行保护增量（2026-10-08）

`0003-execution-protection.patch` 和 `executionguard/` overlay 增加共享的 PTY／官方任务准入、低内存暂停新增、受管理命令的探针侧期限、独立限时维护槽位、任务输出截断；保留官方监控、文件、自更新及远控禁用能力。MCP 仍由服务端实现。配置与真实能力边界见 [执行策略说明](../bridge/EXECUTION_POLICY.md)。上文体积均是此前版本的历史测量，不能直接当作本轮大小。
