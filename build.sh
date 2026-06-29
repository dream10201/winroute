#!/usr/bin/env bash
# 一键把 winroute 编译成 Windows exe。
# 在 Linux / macOS / Windows(Git-Bash) 上都能用,自动交叉编译。
#
#   ./build.sh            # 编译 amd64 -> winroute.exe
#   ./build.sh arm64      # 编译 arm64 -> winroute.exe
#   GOARCH=arm64 ./build.sh
set -euo pipefail

cd "$(dirname "$0")"

ARCH="${1:-${GOARCH:-amd64}}"
OUT="winroute.exe"

if ! command -v go >/dev/null 2>&1; then
  echo "error: 找不到 go,请先安装 Go (https://go.dev/dl/)" >&2
  exit 1
fi

echo ">> go version: $(go version)"
echo ">> 交叉编译 windows/${ARCH} -> ${OUT}"

# -s -w 去掉调试符号,体积更小;CGO 关掉保证纯静态、可跨平台编译。
CGO_ENABLED=0 GOOS=windows GOARCH="${ARCH}" GOFLAGS=-mod=mod \
  go build -trimpath -ldflags "-s -w" -o "${OUT}" .

echo ">> 完成: $(ls -lh "${OUT}" | awk '{print $9, $5}')"
