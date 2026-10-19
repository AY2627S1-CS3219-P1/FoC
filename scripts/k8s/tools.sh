#!/usr/bin/env bash
# Fetch pinned upstream releases and check their published SHA256 checksums.
set -euo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/common.sh"
require curl tar
case $(uname -s) in Linux) os=linux ;; Darwin) os=darwin ;; *) die 'Supported tool hosts: Linux and macOS.' ;; esac
case $(uname -m) in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) die 'Supported tool architectures: amd64 and arm64.' ;; esac
mkdir -p "$K8S_TOOLS_DIR"
tool_tmp=$(mktemp -d)
trap 'rm -rf -- "$tool_tmp"' EXIT

verify_download() {
    local url=$1 checksum_url=$2 destination=$3 expected actual
    expected=$(curl --fail --location --retry 3 "$checksum_url")
    expected=${expected%%[[:space:]]*}
    [[ $expected =~ ^[[:xdigit:]]{64}$ ]] || die "Invalid published checksum from $checksum_url"
    if [[ -f $destination ]]; then
        if command -v sha256sum >/dev/null; then actual=$(sha256sum "$destination"); else actual=$(shasum -a 256 "$destination"); fi
        [[ ${actual%% *} != "$expected" ]] || return 0
    fi
    curl --fail --location --retry 3 --output "$destination" "$url"
    if command -v sha256sum >/dev/null; then
        actual=$(sha256sum "$destination")
    else
        actual=$(shasum -a 256 "$destination")
    fi
    [[ ${actual%% *} = "$expected" ]] || die "Checksum mismatch: $url"
}

# A receipt lets retries avoid network calls, while detecting changed binaries.
receipt_valid() {
    local name=$1 version=$2 digest
    [[ -x $K8S_TOOLS_DIR/$name && -f $K8S_TOOLS_DIR/$name.verified ]] || return 1
    if command -v sha256sum >/dev/null; then digest=$(sha256sum "$K8S_TOOLS_DIR/$name"); else digest=$(shasum -a 256 "$K8S_TOOLS_DIR/$name"); fi
    [[ $(< "$K8S_TOOLS_DIR/$name.verified") = "$version $os $arch ${digest%% *}" ]]
}

install_binary() {
    local name=$1 version=$2 source=$3 digest
    install -m 755 "$source" "$K8S_TOOLS_DIR/$name"
    if command -v sha256sum >/dev/null; then digest=$(sha256sum "$K8S_TOOLS_DIR/$name"); else digest=$(shasum -a 256 "$K8S_TOOLS_DIR/$name"); fi
    printf '%s %s %s %s\n' "$version" "$os" "$arch" "${digest%% *}" > "$K8S_TOOLS_DIR/$name.verified"
}

if ! receipt_valid kind 0.33.0; then
    url="https://github.com/kubernetes-sigs/kind/releases/download/v0.33.0/kind-$os-$arch"
    verify_download "$url" "$url.sha256sum" "$tool_tmp/kind"
    install_binary kind 0.33.0 "$tool_tmp/kind"
fi
if ! receipt_valid kubectl 1.35.0; then
    url="https://dl.k8s.io/release/v1.35.0/bin/$os/$arch/kubectl"
    verify_download "$url" "$url.sha256" "$tool_tmp/kubectl"
    install_binary kubectl 1.35.0 "$tool_tmp/kubectl"
fi
if ! receipt_valid helm 3.20.1; then
    url="https://get.helm.sh/helm-v3.20.1-$os-$arch.tar.gz"
    verify_download "$url" "$url.sha256sum" "$tool_tmp/helm.tar.gz"
    tar -xzf "$tool_tmp/helm.tar.gz" -C "$tool_tmp" "$os-$arch/helm"
    install_binary helm 3.20.1 "$tool_tmp/$os-$arch/helm"
fi
if ! receipt_valid istioctl "$K8S_ISTIO_VERSION" || [[ ! -d $K8S_TOOLS_DIR/istio-$K8S_ISTIO_VERSION/manifests/charts/base ]]; then
    istio_os=$os
    [[ $os != darwin ]] || istio_os=osx
    url="https://github.com/istio/istio/releases/download/$K8S_ISTIO_VERSION/istio-$K8S_ISTIO_VERSION-$istio_os-$arch.tar.gz"
    verify_download "$url" "$url.sha256" "$K8S_TOOLS_DIR/istio.tar.gz"
    tar -xzf "$K8S_TOOLS_DIR/istio.tar.gz" -C "$K8S_TOOLS_DIR"
    install_binary istioctl "$K8S_ISTIO_VERSION" "$K8S_TOOLS_DIR/istio-$K8S_ISTIO_VERSION/bin/istioctl"
fi
note "Verified tools installed in $K8S_TOOLS_DIR"
