# 本地实际源码链路验证

运行前执行仓库 `dev/setup.sh` 或将可审查的两组探针补丁准备到相邻 `komari-agent-dev` checkout。

```bash
cd bridge/validation/nativechain
go test -race ./... -count=1 -timeout=70s
```

这个独立 Go module 用相对 replace 引用仓库面板、桥接和相邻探针源码，避免改变生产模块依赖。Linux 上运行真实 PTY，使用本地 loopback WebSocket 和官方 MCP SDK；面板真实 `ForwardTerminal` 和探针 `StartTerminal` 参与测试。SQLite 审计文件仅写入临时目录。

节点发现、管理员 Bearer 校验和终端请求调度是本地夹具，不能代替真实 AdminAuth/2FA、反向代理、监控事件通道与生产安装验收。22 秒无输出命令跨过默认 20 秒心跳；断开管理员连接后重新连接同一 request_id，校验 nonce/PID 连续性与 cd/export 保留，确认退出码 7。测试期间可能出现探针原有关闭日志，不代表生产节点操作。
