# Android / Termux 安装探针

当前构建提供 `komari-agent-linux-arm64`。在 Android 13 ARM64 的 Termux 中使用，设备无需 root。先运行 `uname -m`，预期输出 `aarch64`。请将文件放在 Termux 的私有目录（例如 `$HOME/komari`），共享存储可能禁止执行。

## 使用已下载的 Actions 成品

将 `komari-agent-linux-arm64` 与同一构建的 `sha256sums.txt` 拷到工作目录，进入该目录执行：

```bash
pkg install curl wget coreutils
grep -E '  komari-agent-linux-arm64$' sha256sums.txt | sha256sum -c -
chmod 700 ./komari-agent-linux-arm64
./komari-agent-linux-arm64 --version
```

校验应输出 `komari-agent-linux-arm64: OK`，`--version` 应包含 `komari-agent:custom-runtime`。

```bash
read -rp 'Panel HTTPS URL: ' KOMARI_PANEL
read -rsp 'Node token: ' KOMARI_TOKEN
printf '\n'
umask 077
nohup ./komari-agent-linux-arm64 -e "$KOMARI_PANEL" -t "$KOMARI_TOKEN" \
  --custom-dns 223.5.5.5 --resource-scope host --include-mountpoint /data \
  --exclude-nics lo --month-rotate 1 --disable-auto-update \
  >> komari-agent.log 2>&1 < /dev/null &
printf 'Komari PID %s\n' "$!"
unset KOMARI_TOKEN
```

查看日志时，`Basic info uploaded successfully` 表示基础信息已被面板接收，`WebSocket connected using v2 protocol` 表示已建立上报长连接。检查存活可运行 `kill -0 <PID>`。停机请确认 PID 仍属于本探针，再运行 `kill <PID>`。

## 已发布版本的安装器

正式分发成品和同一版本安装脚本已就绪后，可用：

```bash
bash ./install.sh --install-no-systemd --install-dir "$HOME/komari" \
  --install-version "$KOMARI_RELEASE" \
  -e "$KOMARI_PANEL" -t "$KOMARI_TOKEN" --custom-dns 223.5.5.5 \
  --resource-scope host --include-mountpoint /data --exclude-nics lo \
  --month-rotate 1 --disable-auto-update
```

安装器在目标目录生成 `start-komari-agent.sh`、`stop-komari-agent.sh`、PID 文件和日志。启动脚本含节点 Token，文件权限为 700，请只留在私有目录。无 systemd 模式依赖后台进程存活，设备或 Android 结束 Termux 后需重新运行启动脚本。安装器将保留检测到的定制版和已传入禁用自动更新参数的现有探针。使用 `--keep-custom` 可直接保留。只有明确要换版时才使用 `--replace-custom`。

Android auto 资源范围使用设备可读的 host 指标。磁盘默认用 `/data`，显式 `/` 也会映射到 `/data`。运行时间按探针进程计，load 平均值省略。非 root ICMP 使用系统允许的 datagram ping socket，平台若限制该权限，可用 TCP/HTTP 探测确认连通性。实际可读取的指标取决于 Android 权限和内核配置。
