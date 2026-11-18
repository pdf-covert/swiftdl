#!/bin/bash
# 打包 fnOS 应用：分别构建 arm 与 x86 两个架构的 .fpk 安装包
# 用法: ./build.sh   （可用 FNPACK=/path/to/fnpack 覆盖 fnpack 路径）
set -euo pipefail

cd "$(dirname "$0")"
ROOT="$(cd .. && pwd)"

# 清理 macOS Finder 产生的杂文件，避免被打进 fpk
find . -name '.DS_Store' -delete

FNPACK="${FNPACK:-$HOME/.local/bin/fnpack}"
DIST="${ROOT}/dist"   # 产物放 package 外，避免被打进 fpk
mkdir -p "$DIST"

VERSION=$(awk -F= '/^version/{gsub(/[[:space:]]/,"",$2); print $2}' manifest)
echo "== swiftdl v${VERSION} 双架构打包 =="

# 1) 前端：类型检查 + 构建，产物复制进 app/www
echo "-- 前端构建 (vite) ..."
( cd "${ROOT}/ui" && npm run build )
rm -rf app/www
cp -R "${ROOT}/ui/www" app/www

# 2) 保留原始 manifest，脚本退出时恢复
cp manifest manifest.bak
trap 'mv -f manifest.bak manifest' EXIT

build_one() {
    local goarch="$1" platform="$2"
    echo "-- ${goarch}: 交叉编译 (manifest platform=${platform}) ..."
    CGO_ENABLED=0 GOOS=linux GOARCH="${goarch}" go build \
        -C "${ROOT}/src/server" \
        -trimpath -ldflags "-s -w" \
        -o "${ROOT}/package/app/server/swiftdl-server" .

    sed -i.bak2 "s/^platform[[:space:]]*=.*/platform = ${platform}/" manifest
    rm -f manifest.bak2

    rm -f swiftdl.fpk
    "$FNPACK" build

    local out="${DIST}/swiftdl_v${VERSION}_${platform}.fpk"
    mv swiftdl.fpk "$out"
    echo "   完成: $out ($(du -h "$out" | cut -f1 | tr -d ' '))"
}

# arm 在前、x86 在后：脚本结束后留在 app/server/ 的二进制与默认 manifest(x86) 一致
build_one arm64 arm
build_one amd64 x86

echo "== 打包完成 =="
ls -lh "$DIST"
