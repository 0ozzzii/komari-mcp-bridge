#!/usr/bin/env bash
set -euo pipefail
umask 022
task_repo=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
task_tools=/workspace/.tools/komari-go1.25.0
task_cache=/workspace/.cache
mkdir -p "$task_tools" "$task_cache/go" "$task_cache/go-build"
task_archive="$task_tools/go1.25.0.linux-amd64.tar.gz"
if [[ ! -f "$task_archive" ]]; then
 curl --fail --location --retry 2 --output "$task_archive" https://dl.google.com/go/go1.25.0.linux-amd64.tar.gz
fi
printf '%s  %s\n' 2852af0cb20a13139b3448992e69b868e50ed0f8a1e5940ee1de9e19a123b613 "$task_archive" | sha256sum --check --status
if [[ ! -x "$task_tools/go/bin/go" ]]; then tar -xzf "$task_archive" -C "$task_tools"; fi
export GOPATH="$task_cache/go" GOCACHE="$task_cache/go-build" GOTOOLCHAIN=local
export PATH="$task_tools/go/bin:$PATH"
# Preserve TLS, the inherited HTTP proxy and checksum database. Official HTTPS
# Git is a supported source fallback for module archives whose CDN is unavailable.
export GOPROXY='https://proxy.golang.org|direct'
go version
cd "$task_repo/bridge"
go mod download
mkdir -p bin
go build -trimpath -ldflags='-s -w' -o bin/komari-mcp ./cmd/komari-mcp
KOMARI_MCP_TEST_BINARY="$task_repo/bridge/bin/komari-mcp" go test ./... -count=1
cd "$task_repo"
go mod download
task_web=/workspace/komari-web-build
task_web_commit=$(python3 -c 'import json; print(json.load(open("release/config.json"))["upstream"]["web"]["commit"])')
if [[ ! -e "$task_web" ]]; then
 git clone https://github.com/komari-monitor/komari-web.git "$task_web"
 git -C "$task_web" checkout --detach "$task_web_commit"
fi
if [[ "$(git -C "$task_web" rev-parse HEAD)" != "$task_web_commit" ]]; then
 echo 'Frontend checkout is a different version; refusing to overwrite.' >&2;exit 1
fi
bash "$task_repo/web-patches/prepare.sh" "$task_web"
cd "$task_web"
npm ci --cache "$task_cache/npm" --no-audit --no-fund
npm run build
test -s dist/index.html
tar -cf "$task_cache/komari-default-dist.tar" -C dist .
mkdir -p "$task_repo/web/public/defaultTheme"
zstd -19 -T2 -q -f "$task_cache/komari-default-dist.tar" -o "$task_repo/web/public/defaultTheme/dist.tar.zst"
if [[ ! -e "$task_repo/web/public/defaultTheme/komari-theme.json" ]]; then
 cp komari-theme.json "$task_repo/web/public/defaultTheme/komari-theme.json"
fi
cd "$task_repo"
python3 -m unittest discover -s release/tests -v
go test ./internal/mcpbridge ./web/router ./web/api/terminal ./web/connection ./web/api/client -count=1
bash agent-patches/prepare.sh /workspace/komari-agent-dev
cd /workspace/komari-agent-dev
umask 022
go test ./executionguard ./monitoring/unit ./cmd/flags ./cmd ./monitoring ./terminal ./ws -count=1
go test ./server -run 'Test(MonitorControlFramesRefreshReadDeadline|OutputBudget|TaskDeadline)' -count=1
CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags='-s -w' -o /tmp/komari-agent-container-fixed .
cd "$task_repo/bridge/validation/nativechain"
# Source integration may need a different transitive module graph after overlays.
# Keep this test-only resolution in the cache, not in the tracked lockfiles.
cp go.mod "$task_cache/nativechain-setup.mod"
cp go.sum "$task_cache/nativechain-setup.sum"
go mod download -modfile="$task_cache/nativechain-setup.mod"
go test -mod=mod -modfile="$task_cache/nativechain-setup.mod" -race ./... -count=1 -timeout=90s
