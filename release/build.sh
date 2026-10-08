#!/usr/bin/env bash
set -euo pipefail
task_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
task_component=${1:?component: panel, agent, bridge}
task_os=${2:?GOOS}
task_arch=${3:?GOARCH}
task_version=${4:?vX.Y.Z or Snapshot-*}
task_output=${5:?output directory}
task_agent=${6:-"$task_root/.release-agent"}
task_repository=${RELEASE_REPOSITORY:-$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["distribution_repository"])' "$task_root/release/config.json")}
[[ "$task_version" =~ ^(v[0-9]+\.[0-9]+\.[0-9]+|Snapshot-[A-Za-z0-9._-]+)$ ]] || { echo 'Invalid release version' >&2; exit 1; }
[[ "$task_repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || exit 1
python3 - "$task_root/release/config.json" "$task_component" "$task_os/$task_arch" <<'PY'
import json,sys
c=json.load(open(sys.argv[1])); component=sys.argv[2]
if component not in ('panel','agent','bridge') or sys.argv[3] not in c[component+'_targets']:
    raise SystemExit('Unsupported release target')
PY
mkdir -p "$task_output"
task_output=$(CDPATH= cd -- "$task_output" && pwd)
task_hash=$(git -C "$task_root" rev-parse HEAD)
task_ext=''; [[ "$task_os" != windows ]] || task_ext=.exe
export GOOS="$task_os" GOARCH="$task_arch" GOTOOLCHAIN=local
case "$task_component" in
  panel)
    cd "$task_root"
    test -s web/public/defaultTheme/dist.tar.zst
    # The SQLite backend requires CGO. Linux release images require musl,
    # matching the official Alpine runtime rather than a glibc-only binary.
    export CGO_ENABLED=1
    : "${CC:?Set a matching CGO cross compiler (official workflow uses Zig)}"
    task_flags="-s -w -X github.com/komari-monitor/komari/utils.CurrentVersion=$task_version -X github.com/komari-monitor/komari/utils.VersionHash=$task_hash"
    if [[ "$task_os" == linux ]]; then task_flags+=" -linkmode=external -extldflags '-static -Wl,--strip-all'"; fi
    go build -mod=readonly -buildvcs=false -trimpath -ldflags="$task_flags" -o "$task_output/komari-$task_os-$task_arch$task_ext" .
    ;;
  agent)
    python3 "$task_root/release/prepare.py" agent "$task_agent"
    cd "$task_agent"
    CGO_ENABLED=0 go build -mod=readonly -buildvcs=false -trimpath \
      -ldflags="-s -w -X github.com/komari-monitor/komari-agent/update.CurrentVersion=$task_version -X github.com/komari-monitor/komari-agent/update.Repo=$task_repository" \
      -o "$task_output/komari-agent-$task_os-$task_arch$task_ext" .
    ;;
  bridge)
    cd "$task_root/bridge"
    CGO_ENABLED=0 go build -mod=readonly -buildvcs=false -trimpath \
      -ldflags="-s -w -X main.version=$task_version -X main.commit=$task_hash" \
      -o "$task_output/komari-mcp-$task_os-$task_arch$task_ext" ./cmd/komari-mcp
    ;;
esac
