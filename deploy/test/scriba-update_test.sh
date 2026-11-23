#!/bin/bash
# Prueba deploy/scriba-update con curl y systemctl simulados. Requiere Linux, root y el usuario de servicio
# (en CI: `sudo useradd --system scriba && sudo deploy/test/scriba-update_test.sh`).
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
updater="$here/../scriba-update"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
export FIXTURES="$work/fixtures" SCRIBA_DIR="$work/scriba" SCRIBA_READY_TRIES=2 SCRIBA_READY_URL=http://127.0.0.1:1/readyz
mkdir -p "$FIXTURES" "$work/bin" "$SCRIBA_DIR/data"
fail() { echo "FALLA: $*" >&2; exit 1; }

# Stubs: curl sirve fixtures locales y /readyz falla si el binario instalado dice "broken"; systemctl solo registra.
cat > "$work/bin/curl" <<'STUB'
#!/bin/bash
out=""; url=""
while [ $# -gt 0 ]; do case "$1" in -o) out=$2; shift 2 ;; -*) shift ;; *) url=$1; shift ;; esac; done
case "$url" in
  */readyz)
    if grep -q broken "$SCRIBA_DIR/scriba"; then
      # El binario roto alcanzó a migrar el estado y a escribir el índice antes de fallar.
      d="$SCRIBA_DIR/data"
      echo "estado v0.2.0" > "$d/state.db"; echo wal > "$d/state.db-wal"
      echo "índice v0.2.0" > "$d/scriba.db"; echo wal > "$d/scriba.db-wal"
      exit 22
    fi
    exit 0 ;;
  */scriba.version) cat "$FIXTURES/scriba.version" ;;
  */releases/latest) printf '{"tag_name": "%s"}' "$(cat "$FIXTURES/latest")" ;;
  */releases/download/*) cp "$FIXTURES/$(basename "$url")" "$out" ;;
  *) exit 22 ;;
esac
STUB
# shellcheck disable=SC2016 # el stub debe escribir $* literal
printf '#!/bin/bash\necho "$*" >> "$FIXTURES/systemctl.log"\n' > "$work/bin/systemctl"
chmod +x "$work/bin/curl" "$work/bin/systemctl"
export PATH="$work/bin:$PATH"

arch=$(uname -m); case "$arch" in aarch64) arch=arm64 ;; x86_64) arch=amd64 ;; esac
release() { # release <tag> <contenido del binario>
  local tag=$1 dir="$work/build-$1"
  mkdir -p "$dir"
  printf '%s\n' "$2" > "$dir/scriba"
  tar -czf "$FIXTURES/scriba_${tag#v}_linux_${arch}.tar.gz" -C "$dir" scriba
  (cd "$FIXTURES" && sha256sum scriba_*.tar.gz > checksums.txt)
}

# 1. Instalación inicial desde la última release: sin binario ni state.db previos.
release v0.1.0 "binario v0.1.0"
echo v0.1.0 > "$FIXTURES/latest"
"$updater" || fail "la instalación inicial falló"
grep -q "binario v0.1.0" "$SCRIBA_DIR/scriba" || fail "no instaló v0.1.0"
[ "$(cat "$SCRIBA_DIR/.release")" = v0.1.0 ] || fail ".release no quedó en v0.1.0"
# Lo que v0.1.0 escribió al correr; el -wal trae transacciones sin volcar (murió por SIGKILL u OOM).
echo "estado v0.1.0" > "$SCRIBA_DIR/data/state.db"
echo "wal v0.1.0" > "$SCRIBA_DIR/data/state.db-wal"
echo "índice v0.1.0" > "$SCRIBA_DIR/data/scriba.db"

# 2. Misma versión: no toca nada.
: > "$FIXTURES/systemctl.log"
"$updater" || fail "la segunda corrida falló"
[ ! -s "$FIXTURES/systemctl.log" ] || fail "reinició el servicio sin versión nueva"

# 3. Versión fijada (GitOps) que no queda lista: rollback del binario y de state.db, y borrado del índice.
release v0.2.0 "binario broken v0.2.0"
echo v0.2.0 > "$FIXTURES/scriba.version"
export SCRIBA_VERSION_URL=https://example.org/scriba.version
if "$updater"; then fail "una versión que no queda lista debería devolver error"; fi
grep -q "binario v0.1.0" "$SCRIBA_DIR/scriba" || fail "el rollback no restauró el binario"
grep -q "estado v0.1.0" "$SCRIBA_DIR/data/state.db" || fail "el rollback no restauró state.db"
grep -q "wal v0.1.0" "$SCRIBA_DIR/data/state.db-wal" || fail "el rollback no restauró el state.db-wal anterior"
[ "$(stat -c '%U %a' "$SCRIBA_DIR/data/state.db")" = "scriba 600" ] || fail "state.db restaurado sin dueño scriba y 600"
[ ! -e "$SCRIBA_DIR/data/scriba.db" ] || fail "el rollback no borró scriba.db"
[ ! -e "$SCRIBA_DIR/data/scriba.db-wal" ] || fail "el rollback no borró scriba.db-wal"
[ "$(cat "$SCRIBA_DIR/.release")" = v0.1.0 ] || fail ".release cambió tras el rollback"
[ "$(cat "$SCRIBA_DIR/.failed")" = v0.2.0 ] || fail "no registró v0.2.0 como fallida"

# 3b. La versión que ya falló no se reintenta en cada pasada del timer.
: > "$FIXTURES/systemctl.log"
"$updater" 2>/dev/null || fail "reintentar una versión fallida debería salir sin error"
[ ! -s "$FIXTURES/systemctl.log" ] || fail "reintentó la versión que ya había fallado"

# 3c. Un archivo sin binario se rechaza sin detener el servicio.
mkdir -p "$work/build-v0.3.0" && echo x > "$work/build-v0.3.0/LEEME"
tar -czf "$FIXTURES/scriba_0.3.0_linux_${arch}.tar.gz" -C "$work/build-v0.3.0" LEEME
(cd "$FIXTURES" && sha256sum scriba_*.tar.gz > checksums.txt)
echo v0.3.0 > "$FIXTURES/scriba.version"
if "$updater" 2>/dev/null; then fail "aceptó un archivo sin binario"; fi
[ ! -s "$FIXTURES/systemctl.log" ] || fail "detuvo el servicio con un archivo sin binario"

# 3d. Una versión nueva que sí queda lista se instala y limpia la marca de fallida.
release v0.4.0 "binario v0.4.0"
echo v0.4.0 > "$FIXTURES/scriba.version"
"$updater" || fail "no instaló v0.4.0"
grep -q "binario v0.4.0" "$SCRIBA_DIR/scriba" || fail "no instaló v0.4.0"
[ ! -e "$SCRIBA_DIR/.failed" ] || fail "no borró .failed tras una actualización buena"

# 4. Un tag inválido se rechaza antes de descargar nada.
echo 'v1.0.0;rm -rf /' > "$FIXTURES/scriba.version"
if "$updater" 2>/dev/null; then fail "aceptó un tag inválido"; fi

echo "scriba-update: ok"
