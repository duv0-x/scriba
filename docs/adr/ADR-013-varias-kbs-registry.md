---
title: "Varias bases de conocimiento por instancia: Registry en un proceso"
number: 13
date: 2026-11-26
domain: arquitectura
status: "aceptado"
tags: [kbs, registry, multi-tenant, vault, índice, migración]
---

# ADR-013: Varias bases de conocimiento por instancia: Registry en un proceso

## Contexto

Una instancia servía un solo vault (`scriba serve --vault`). Se quieren N bases de conocimiento (KB) en la misma instancia, cada una con su repo git y su backup, y cada agente limitado a una. Spec: `docs/specs/2026-11-26-scriba-varias-bases-de-conocimiento.md` en la base de conocimiento. Issues del epic duv0-x/scriba#44: #45 (este), #46 (web `/k/{kb}/`), #47 (MCP por KB).

## Decisión

1. **Registry en un solo proceso** (paquete `internal/kbs`). Descartado: una instancia por KB (otra web, otro Access, otros conectores) y carpetas dentro de un vault (repo compartido, aislamiento por filtros). Cada KB tiene vault (repo git), índice FTS5, watcher, writer, pusher, bus y presencia propios.
2. **Una KB = un directorio**. Layout:
   ```
   <data>/
     state.db              # compartido
     secret.key            # compartido
     kbs/<slug>/vault/     # repo git de la KB (ADR-002)
     kbs/<slug>/index.db   # índice derivado (ADR-003)
   ```
   La lista de KBs es el contenido de `<data>/kbs/` (sin tabla `kbs`, sin desincronización). La KB por defecto va en `settings.default_kb`.
3. **Slug inmutable**, `^[a-z0-9][a-z0-9-]{0,39}$`, validado en el borde: termina en rutas de disco, URLs y tokens.
4. **Ningún handler usa una KB que no le resolvió el Registry.** En #45 `serve.go` cablea la web y `/mcp` con la KB por defecto que entrega el Registry; #46 resuelve por ruta y #47 por token.
5. **state.db**: migración goose 00005 (en transacción) añade `kb` a `agent_tokens` (cubre los tokens estáticos y las concesiones OAuth de ADR-010, que viven en la misma tabla) y a `activity`; recrea `reads` con PK `(kb, user, path)`; crea `kb_connectors(name, kb)` con `repo/push/last_push/last_error`. El slug por defecto no se conoce en SQL, así que las filas viejas quedan con `kb = ''` y `AdoptKB` las asigna en una segunda transacción idempotente que corre en cada arranque. Hasta #47, los tokens nuevos toman `default_kb`.
6. **GitHub**: la credencial (cuenta y secreto) es de la instancia, en `connectors`; el repo destino y el estado del push son por KB, en `kb_connectors`. Cada KB tiene su pusher. **Kindle** sigue siendo de la instancia.
7. **Migración del layout** al arrancar, si existe el vault viejo y no `<data>/kbs/`: slug = nombre del repo de GitHub conectado, normalizado (minúsculas; lo que no sea `[a-z0-9-]` pasa a `-`; en la instancia: `ia-kb`), o `principal` sin conector. `vault/` se mueve a `<data>/.kbs-migrating/<slug>/vault` y luego `.kbs-migrating` a `kbs` (dos renames en el mismo filesystem): si el proceso muere entre los dos, el siguiente arranque termina la migración. El índice viejo (`scriba.db`) se borra: se reconstruye. Idempotente. Entre filesystems distintos falla en vez de copiar.
8. **CLI**: `scriba serve --data <dir>`. `--vault <dir>` (y `--db`) siguen como compatibilidad hasta que `deploy/scriba.service` pase a `--data`: `<data>` es el directorio de `--db` si viene, o el padre de `--vault`, para que `state.db` siga donde estaba. `scriba view` no cambia.
9. **Borrar una KB**: para su watcher y su pusher, borra sus filas de `reads`, `activity` y `kb_connectors` y luego el directorio. La KB por defecto no se puede borrar. Los tokens de la KB borrada los revoca #47.
10. **Rollback de `scriba-update`**: se restaura el layout, no se deja un symlink. Antes de instalar, si existe `data/vault` y no `data/kbs`, se marca `.previous/layout-vault`; si la versión nueva no queda lista, el rollback devuelve la única `kbs/<slug>/vault` (o `.kbs-migrating/<slug>/vault`) a `data/vault` y borra `kbs/`, junto con el `state.db` de antes. Un symlink dejaría dos rutas al mismo repo para siempre. Esto solo cubre el salto mientras `ExecStart` siga con `--vault`, porque un binario anterior no entiende `--data`: por eso `deploy/scriba.service` sigue con `--vault` en esta release (la compatibilidad de la decisión 8 hace la migración) y el cambio a `--data` llega en una release posterior. El `scriba-update` nuevo tiene que estar instalado antes de subir la versión: el que ya está en la máquina no sabe devolver el layout. Con más de una KB no hay rollback automático a una versión anterior a ADR-013.
11. La muerte del watcher de cualquier KB sigue apagando el proceso para que systemd lo reinicie.

## Consecuencias

- RAM de la Pi (un índice SQLite, un watcher y un pusher por KB; se mide con dos KBs en el despliegue).
- `baseline:<user>` sigue siendo de instancia.
- Las rutas `/k/{kb}/` y la pantalla de KBs llegan en #46.

## Links relacionados

- [ADR-002: El vault: Markdown plano dentro de un repositorio git](ADR-002-vault-markdown-y-git.md)
- [ADR-003: Índice de búsqueda FTS5 derivado y reconstruible](ADR-003-indice-fts5-derivado.md)
- [ADR-004: Autenticación de la web y del endpoint MCP](ADR-004-autenticacion.md)
- [ADR-006: Conector de GitHub por device flow y secretos cifrados](ADR-006-conector-github-y-secretos.md)
- [ADR-007: MCP para agentes y tokens de agente](ADR-007-mcp-y-tokens-de-agente.md)
- [ADR-008: Release y despliegue con systemd](ADR-008-release-y-despliegue.md)
- [ADR-010: OAuth para usar Scriba como conector personalizado](ADR-010-oauth-conectores.md)
- [ADR-012: Conector Kindle: EPUB generado en Scriba y envío por Resend](ADR-012-conector-kindle.md)
