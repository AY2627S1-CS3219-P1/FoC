#!/usr/bin/env bash
# Bootstrap the pinned Bun runner before the TypeScript Kubernetes CLI exists.
set -euo pipefail

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
version=$(< "$repo_root/.bun-version")
[[ $version = 1.4.3 ]] || { printf 'ERROR: Update the pinned Bun checksum table before changing .bun-version.\n' >&2; exit 1; }
tools_dir=${K8S_TOOLS_DIR:-$repo_root/.tools/k8s}
mkdir -p "$tools_dir"

case $(uname -s):$(uname -m) in
    Linux:x86_64|Linux:amd64)
        archive=bun-linux-x64-baseline.zip
        checksum=1fc2edac843102909e3a1be1d8d9802cc6071cf074e67e88231f4ffe0f8b397b
        platform=linux-x64-baseline
        ;;
    Linux:aarch64|Linux:arm64)
        archive=bun-linux-aarch64.zip
        checksum=efa9813da5ed72423bf847f916e8d2c47c0d776add972354026a75e10da9aa21
        platform=linux-aarch64
        ;;
    Darwin:x86_64|Darwin:amd64)
        archive=bun-darwin-x64-baseline.zip
        checksum=0fde04e8b092156b97fe6432e899baf16d9ed6ce52cbb4218d2d9c07a2f827ef
        platform=darwin-x64-baseline
        ;;
    Darwin:arm64|Darwin:aarch64)
        archive=bun-darwin-aarch64.zip
        checksum=80afd4c069b46a6fa8f3ec520bebc750a7843e4a8d5b9583d29ad436c00a3403
        platform=darwin-aarch64
        ;;
    *)
        printf 'ERROR: Unsupported Bun host: %s %s\n' "$(uname -s)" "$(uname -m)" >&2
        exit 1
        ;;
esac

if command -v sha256sum >/dev/null; then
    hash_file() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null; then
    hash_file() { shasum -a 256 "$1" | awk '{print $1}'; }
else
    printf 'ERROR: Install sha256sum or shasum to verify Bun.\n' >&2
    exit 1
fi

bun_path="$tools_dir/bun"
receipt="$tools_dir/bun.verified"
if [[ -x $bun_path && -f $receipt ]]; then
    actual=$(hash_file "$bun_path")
    if [[ $(< "$receipt") = "$version $platform $actual" ]] && [[ $($bun_path --version) = "$version" ]]; then
        exit 0
    fi
fi

command -v curl >/dev/null || { printf 'ERROR: curl is required to download Bun.\n' >&2; exit 1; }
command -v unzip >/dev/null || { printf 'ERROR: unzip is required to extract the pinned Bun runner.\n' >&2; exit 1; }
tmp_dir=$(mktemp -d)
trap 'rm -rf -- "$tmp_dir"' EXIT
url="https://github.com/oven-sh/bun/releases/download/bun-v$version/$archive"
curl --fail --location --retry 3 --output "$tmp_dir/bun.zip" "$url"
actual=$(hash_file "$tmp_dir/bun.zip")
[[ $actual = "$checksum" ]] || { printf 'ERROR: Bun archive checksum mismatch.\n' >&2; exit 1; }
unzip -p "$tmp_dir/bun.zip" "bun-$platform/bun" > "$tmp_dir/bun"
chmod 755 "$tmp_dir/bun"
[[ $($tmp_dir/bun --version) = "$version" ]] || { printf 'ERROR: Bun version verification failed.\n' >&2; exit 1; }
install -m 755 "$tmp_dir/bun" "$bun_path"
actual=$(hash_file "$bun_path")
printf '%s %s %s\n' "$version" "$platform" "$actual" > "$receipt"
