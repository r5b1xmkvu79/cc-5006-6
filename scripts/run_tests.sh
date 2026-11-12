#!/usr/bin/env bash
# One-command verification: unit tests, geometry parity, asset generation,
# end-to-end render checks, and the delivery bundle.
set -euo pipefail
cd "$(dirname "$0")/.."

export PATH="$PWD/.toolchain/go/go/bin:$PATH"
export CGO_ENABLED=0
export ORALCUT_FFMPEG="${ORALCUT_FFMPEG:-$PWD/.toolchain/ffmpeg/ffmpeg-7.0.2-amd64-static/ffmpeg}"
export ORALCUT_FFPROBE="${ORALCUT_FFPROBE:-$PWD/.toolchain/ffmpeg/ffmpeg-7.0.2-amd64-static/ffprobe}"
export ORALCUT_ASSETS="${ORALCUT_ASSETS:-$PWD/assets}"

echo "== generate test assets =="
go run ./cmd/genassets -out "$ORALCUT_ASSETS"

echo "== go tests =="
go test ./...

echo "== geometry parity (JS vs Go) =="
cd web && npm test && cd ..

echo "== frontend build =="
npm --prefix web run build

echo "== delivery bundle =="
go run ./cmd/delivercheck -assets "$ORALCUT_ASSETS" -out deliver
