#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

echo "==> Building gnata.wasm..."
GOOS=js GOARCH=wasm go build -ldflags="-s -w" -trimpath -o npm/wasm/gnata.wasm ./wasm/

echo "==> Copying wasm_exec.js from Go toolchain..."
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" npm/wasm/wasm_exec.js

echo "==> Installing npm dependencies..."
cd npm && npm install

echo "==> Building TypeScript..."
npm run build

echo "==> Done."
echo "    npm/wasm/  contains gnata.wasm + wasm_exec.js"
echo "    npm/dist/  contains JS + .d.ts"
