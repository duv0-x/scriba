#!/bin/bash
# Empaqueta el editor en línea (CodeMirror 6, ADR-006 / ADR-014) con esbuild y regenera
# internal/web/static/inline-editor.js + THIRD_PARTY_LICENSES.txt (MIT, BSD y Apache
# exigen incluir los avisos en las distribuciones binarias).
# A propósito NO está en go:generate: el CI corre `go generate` sin Node. El bundle y
# THIRD_PARTY_LICENSES.txt se commitean y se regeneran a mano al cambiar internal/web/editor/.
# Uso: scripts/codemirror.sh
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root/internal/web/editor"

npm ci --no-audit --no-fund
./node_modules/.bin/esbuild src/main.js --bundle --minify --format=iife --target=es2022 \
  --legal-comments=none --charset=utf8 --outfile=../static/inline-editor.js

# Avisos de producción, en orden alfabético: el archivo debe salir idéntico en cada corrida
# (sin rutas absolutas ni fechas).
lic=THIRD_PARTY_LICENSES.txt
: >"$lic"
npm ls --omit=dev --all --parseable 2>/dev/null | tail -n +2 | LC_ALL=C sort |
  while read -r dir; do
    meta=$(node -p "const p=require('$dir/package.json');p.name+'@'+p.version+' ('+(typeof p.license==='string'?p.license:(p.license&&p.license.type)||'?')+')'")
    f=$(ls "$dir"/LICENSE* 2>/dev/null | head -n 1 || true)
    printf '==== %s ====\n' "$meta" >>"$lic"
    if [ -n "$f" ]; then printf '%s\n\n' "$(cat "$f")" >>"$lic"; else printf '%s\n\n' "(sin archivo LICENSE)" >>"$lic"; fi
  done

min=$(($(wc -c <../static/inline-editor.js)))
gz=$(gzip -9c ../static/inline-editor.js | wc -c | tr -d ' ')
echo "inline-editor.js: $min bytes min, $gz bytes gzip"
