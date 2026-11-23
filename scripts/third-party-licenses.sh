#!/bin/bash
# Genera third_party_licenses/ con los avisos de licencia de todo lo compilado en el binario:
# MIT, BSD y Apache exigen incluirlos también en las distribuciones binarias. Lo corre GoReleaser.
set -euo pipefail

out=${1:-third_party_licenses}
chmod -R u+w "$out" 2>/dev/null || true # go-licenses deja los archivos de solo lectura
rm -rf "$out"

# go-licenses no reconoce MIT-0: esos módulos se copian a mano más abajo.
go run github.com/google/go-licenses/v2@v2.0.1 save ./cmd/scriba --save_path="$out" \
  --ignore github.com/duv0-x/scriba \
  --ignore github.com/ncruces/go-sqlite3-wasm/v6 \
  --ignore github.com/segmentio/asm

wasm=$(go list -m -f '{{.Dir}}' github.com/ncruces/go-sqlite3-wasm/v6)
mkdir -p "$out/github.com/ncruces/go-sqlite3-wasm/v6"
cp "$wasm/LICENSE" "$out/github.com/ncruces/go-sqlite3-wasm/v6/" # MIT-0; el SQLite que incluye es de dominio público

# MIT-0; llega por el go-sdk de MCP (segmentio/encoding).
asm=$(go list -m -f '{{.Dir}}' github.com/segmentio/asm)
mkdir -p "$out/github.com/segmentio/asm"
cp "$asm/LICENSE" "$out/github.com/segmentio/asm/"

# Fuentes embebidas en la web (SIL OFL 1.1).
mkdir -p "$out/fonts"
cp internal/web/static/fonts/LICENSE-*.txt "$out/fonts/"
