# Embedded verbatim into the official-derived installers by render-installers.py.
# No secret is sent to GitHub; Client Tokens are only passed to the agent.
komari_release_repository=${KOMARI_RELEASE_REPOSITORY:-0ozzzii/komari-mcp-bridge}
printf '%s\n' "$komari_release_repository" | LC_ALL=C grep -Eq '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$' || {
    echo 'Invalid KOMARI_RELEASE_REPOSITORY; expected owner/repository' >&2
    exit 1
}

komari_resolve_release() {
    komari_requested=$1
    if [ -z "$komari_requested" ] || [ "$komari_requested" = latest ]; then
        komari_api="https://api.github.com/repos/${komari_release_repository}/releases/latest"
        komari_tag=$(curl -fsSL --connect-timeout 15 --max-time 60 "$komari_api" |
            sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1) || return 1
    elif [ "$komari_requested" = snapshot ]; then
        komari_api="https://api.github.com/repos/${komari_release_repository}/releases?per_page=100"
        komari_tag=$(curl -fsSL --connect-timeout 15 --max-time 60 "$komari_api" |
            grep -o '"tag_name":[[:space:]]*"Snapshot-[A-Za-z0-9._-]*"' |
            sed 's/.*"\(Snapshot-[^"]*\)".*/\1/' | LC_ALL=C sort -r | head -n 1) || return 1
    else
        komari_tag=$komari_requested
    fi
    # Restrict path and metadata characters even when the release API is used.
    printf '%s\n' "$komari_tag" | LC_ALL=C grep -Eq '^(v[0-9]+\.[0-9]+\.[0-9]+|Snapshot-[A-Za-z0-9._-]+)$' || return 1
    printf '%s\n' "$komari_tag"
}

komari_fetch_verified() (
    set -eu
    komari_url=$1
    komari_dest=$2
    komari_name=${komari_url##*/}
    komari_dir=$(mktemp -d "${komari_dest}.download.XXXXXX")
    trap 'rm -rf -- "$komari_dir"' EXIT HUP INT TERM
    curl -fsSL --retry 2 --connect-timeout 15 --max-time 300 "$komari_url" -o "$komari_dir/binary"
    test -s "$komari_dir/binary"
    curl -fsSL --retry 2 --connect-timeout 15 --max-time 60 "${komari_url%/*}/sha256sums.txt" -o "$komari_dir/manifest"
    komari_expected=$(awk -v n="$komari_name" '$2 == n || $2 == "*" n {print $1; count++} END {if(count!=1) exit 1}' "$komari_dir/manifest")
    printf '%s\n' "$komari_expected" | LC_ALL=C grep -Eq '^[a-fA-F0-9]{64}$'
    if command -v sha256sum >/dev/null 2>&1; then
        komari_actual=$(sha256sum "$komari_dir/binary" | awk '{print $1}')
    elif command -v shasum >/dev/null 2>&1; then
        komari_actual=$(shasum -a 256 "$komari_dir/binary" | awk '{print $1}')
    elif command -v openssl >/dev/null 2>&1; then
        komari_actual=$(openssl dgst -sha256 "$komari_dir/binary" | awk '{print $NF}')
    else
        echo 'SHA256 verification requires sha256sum, shasum or openssl' >&2; exit 1
    fi
    test "$(printf '%s' "$komari_expected" | tr A-F a-f)" = "$komari_actual" || { echo 'Release checksum mismatch; existing installation preserved' >&2; exit 1; }
    chmod 755 "$komari_dir/binary"
    mv -f "$komari_dir/binary" "$komari_dest"
)
