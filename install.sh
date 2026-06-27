#!/usr/bin/env bash
#
# webextract 一键安装脚本（macOS / Linux）
#
# 用法：
#   curl -fsSL https://raw.githubusercontent.com/dongmy54/webextract/main/install.sh | bash
#
# 自定义安装目录（默认 $HOME/.local/bin）：
#   INSTALL_DIR=/usr/local/bin curl -fsSL .../install.sh | bash
#
# 需要本机已安装 curl 或 wget；安装后运行时还需要 Chrome / Chromium / Edge。
#
set -euo pipefail

REPO="dongmy54/webextract"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

color() { printf '\033[%sm%s\033[0m' "$1" "$2"; }
info()  { printf '%s %s\n' "$(color '1;34' '==>')" "$*"; }
warn_() { printf '%s %s\n' "$(color '1;33' '!!')"  "$*" >&2; }
err()   { printf '%s %s\n' "$(color '1;31' 'xx')"  "$*" >&2; }

# ---- 1. 识别平台 ----
os_raw="$(uname -s)"
arch_raw="$(uname -m)"
case "$os_raw" in
  Darwin) os="darwin" ;;
  Linux)  os="linux"  ;;
  MINGW*|MSYS*|CYGWIN*)
    err "检测到 Windows 类环境。请使用 PowerShell，或手动下载 zip："
    err "  https://github.com/${REPO}/releases/latest"
    exit 1
    ;;
  *) err "不支持的操作系统: $os_raw"; exit 1 ;;
esac
case "$arch_raw" in
  x86_64|amd64)  arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) err "不支持的架构: $arch_raw"; exit 1 ;;
esac
info "检测到平台: ${os}/${arch}"

# ---- 2. 选择下载工具 ----
if command -v curl >/dev/null 2>&1; then
  http_get() { curl -fsSL "$1"; }
  http_dl()  { curl -fsSL "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
  http_get() { wget -qO- "$1"; }
  http_dl()  { wget -qO "$2" "$1"; }
else
  err "需要 curl 或 wget 来下载，请先安装其一。"; exit 1
fi

# ---- 3. 查询最新 Release 并匹配资产 ----
info "查询最新版本..."
api_json="$(http_get "https://api.github.com/repos/${REPO}/releases/latest")"

asset_url="$(printf '%s\n' "$api_json" \
  | grep -oE 'https://[^"]+_'"${os}_${arch}"'\.tar\.gz' | head -n1 || true)"
checksum_url="$(printf '%s\n' "$api_json" \
  | grep -oE 'https://[^"]+/checksums\.txt' | head -n1 || true)"

if [ -z "$asset_url" ]; then
  err "未找到适合 ${os}/${arch} 的发布资产。"
  err "请前往 https://github.com/${REPO}/releases/latest 手动下载。"
  exit 1
fi
asset_name="$(basename "$asset_url")"
info "下载: $asset_name"

# ---- 4. 下载到临时目录 ----
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

archive="$tmpdir/$asset_name"
http_dl "$asset_url" "$archive"

# ---- 5. 校验 sha256（如 checksums.txt 可用）----
have_hasher=""
if command -v sha256sum >/dev/null 2>&1; then have_hasher=sha256sum
elif command -v shasum >/dev/null 2>&1; then have_hasher="shasum -a 256"; fi

if [ -n "$checksum_url" ] && [ -n "$have_hasher" ]; then
  checksums_file="$tmpdir/checksums.txt"
  if http_dl "$checksum_url" "$checksums_file" 2>/dev/null; then
    expected="$(grep -E "[[:space:]]+${asset_name}\$" "$checksums_file" \
      | awk '{print $1}' | head -n1 || true)"
    if [ -n "$expected" ]; then
      actual="$($have_hasher "$archive" | awk '{print $1}')"
      if [ "$expected" != "$actual" ]; then
        err "校验失败: 期望 ${expected:0:12}… 实际 ${actual:0:12}…"; exit 1
      fi
      info "校验通过 (sha256)"
    fi
  fi
fi

# ---- 6. 解压 ----
info "解压..."
tar -xzf "$archive" -C "$tmpdir"
if [ ! -f "$tmpdir/webextract" ]; then
  err "归档内未找到 webextract 可执行文件。"; exit 1
fi

# ---- 7. 安装到目标目录 ----
mkdir -p "$INSTALL_DIR"
install_path="$INSTALL_DIR/webextract"
mv "$tmpdir/webextract" "$install_path"
chmod +x "$install_path"
info "已安装到: $install_path"

# ---- 8. PATH 提示 ----
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    printf '\n'
    warn_ "$INSTALL_DIR 不在 PATH 中，请将其加入（任选其一）："
    rc_file="$HOME/.bashrc"
    case "$(basename "${SHELL:-bash}")" in
      zsh)  rc_file="$HOME/.zshrc" ;;
      fish) rc_file="$HOME/.config/fish/config.fish"
            printf '    fish_add_path %s\n' "$INSTALL_DIR" ;;
    esac
    if [ "${rc_file##*/}" = "config.fish" ]; then :; else
      printf '    echo '\''export PATH="%s:$PATH"'\'' >> %s\n' "$INSTALL_DIR" "$rc_file"
    fi
    ;;
esac

# ---- 9. 完成 ----
printf '\n'
info "$(color '1;32' '✓ webextract 安装完成')"
printf '  位置：%s\n' "$install_path"
printf '  用法：%s <URL>     （单页提取）\n' "$(color '1;36' 'webextract')"
printf '        %s crawl <URL>   （整站爬取）\n' "$(color '1;36' 'webextract')"
printf '  注意：运行时需要本机已安装 Chrome / Chromium / Edge（用于无头渲染）。\n'
