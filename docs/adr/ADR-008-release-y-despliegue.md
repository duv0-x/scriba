---
title: "Release y despliegue con systemd"
number: 8
date: 2026-11-22
domain: operations
status: "aceptado"
tags: [release, goreleaser, systemd, deploy, rollback]
---

# ADR-008: Release y despliegue con systemd

## Contexto

La primera instancia de Scriba corre en una Raspberry Pi 3B+ (arm64, 1 GB de RAM) que ya sirve Gremio, detrás de un túnel de Cloudflare. Hace falta publicar binarios verificables y actualizarlos sin entrar a la Pi, con vuelta atrás si una versión no arranca. Gremio ya resolvió lo mismo y su esquema funciona en esa Pi.

## Decisión

### 1. Releases con GoReleaser

Un tag `vX.Y.Z` dispara `.github/workflows/release.yml`, que corre los tests y GoReleaser. La release publica binarios sin cgo para linux/amd64, linux/arm64 y darwin/arm64 con `checksums.txt`. Cada `tar.gz` incluye `deploy/` y `third_party_licenses/` (avisos de las dependencias; `scripts/third-party-licenses.sh`).

### 2. Layout en el servidor

```
/scriba/scriba            binario (root, 0755)
/scriba/scriba.env        SCRIBA_LISTEN, SCRIBA_PUBLIC_URL, SCRIBA_TZ (root, 0600)
/scriba/data/             usuario scriba, 0700
  vault/                  el repo git de notas
  scriba.db               índice (reconstruible, ADR-003)
  state.db, secret.key    estado y llave (ADR-006)
  tmp/                    TMPDIR: zips subidos y askpass de git, en disco y no en el tmpfs de /tmp
```

Scriba no lee variables de entorno (ADR-006): `scriba.service` las expande en los flags de `ExecStart`.

### 3. systemd endurecido

`scriba.service` corre como `scriba`, solo puede escribir en `/scriba/data` y lleva el mismo endurecimiento que Gremio, más `MemoryMax=192M` para convivir con Gremio en 1 GB.

### 4. Actualización con rollback

`scriba-update` (timer cada 5 min) instala la versión que indique `SCRIBA_VERSION_URL` (el `scriba.version` del repo de la instancia: subir de versión es un PR) o, sin ella, la última release. Verifica el checksum, detiene el servicio, respalda el binario y `state.db`, instala y espera `/readyz`. Si no responde, restaura el binario y `state.db` (con su `-wal`) y **borra** `scriba.db`: el binario anterior reconstruye el índice desde el vault, así que una migración nueva del índice nunca bloquea la vuelta atrás. La versión fallida se anota en `.failed` y no se reintenta hasta que cambie la versión pedida. Si algo falla a mitad, el servicio se vuelve a arrancar.

## Consecuencias positivas

- Actualizar es un PR en el repo de la instancia; la Pi no necesita acceso entrante.
- Una versión rota vuelve sola a la anterior.
- Mismo esquema operativo que Gremio en la misma Pi.

## Consecuencias negativas

- Sin backups propios en el MVP. El vault está en GitHub con todo su historial y el índice se reconstruye; perder `state.db` y `secret.key` obliga a reconectar GitHub y crear tokens de agente nuevos.
- El rollback no deshace cambios en el vault hechos por la versión nueva (son commits normales de git).
- La primera instalación no tiene rollback: si la primera versión no arranca, no hay binario anterior que reponer y queda instalada la rota (`provision.sh` lo reporta al no responder `/readyz`).

## Alternativas consideradas

| Alternativa | Por qué no |
|---|---|
| Imagen OCI y Docker en la Pi | Más memoria y piezas en una Pi de 1 GB; nadie la pide todavía |
| Backups diarios cifrados con `age` | El vault ya vive en GitHub; lo único que se perdería (`state.db`) se recrea en minutos |
| Actualizar a mano por SSH | Sin rollback y con acceso a producción en cada versión |

## Links relacionados

- ADR-003: índice FTS5 derivado.
- ADR-005: stack.
- ADR-006: conector de GitHub y secretos.
