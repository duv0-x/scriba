# CLAUDE.md — Scriba

El dueño es Duván Ballén (Senior Platform Engineer, aprendiendo Go). Comunícate en español, con términos técnicos en inglés.

## Estado

MVP entregado (duv0-x/scriba#1, cerrado). La instancia propia es https://kb.duv0-x.cloud, con despliegue GitOps en duv0-x/kb.duv0-x.cloud (`scriba.version`). Los planes y notas de cada ticket viven en esa misma base de conocimiento: `plans/` y `tickets/personal/`, vía el MCP `scriba`. El trabajo pendiente está en los issues abiertos. Cada decisión de diseño, arquitectura o stack se documenta antes como ADR en `docs/adr/ADR-NNN-titulo.md`, antes de implementarla.

## Qué es

Una base de conocimiento en Markdown escrita por agentes de IA (vía MCP) y leída por personas (lector web). El Markdown plano con frontmatter YAML dentro de un repositorio git es la fuente de verdad (`docs/adr/ADR-002-vault-markdown-y-git.md`).

## Arquitectura

Go puro (`CGO_ENABLED=0`), un binario con todo embebido; mismo stack que Gremio (`docs/adr/ADR-005-stack.md`). SQLite con ncruces + goose, **sin sqlc**: `database/sql` directo. El índice FTS5 es derivado y se reconstruye desde los `.md` (`docs/adr/ADR-003-indice-fts5-derivado.md`). Web renderizada en el servidor con templ + htmx y estilos con Tailwind standalone. El código generado (templ, CSS) se commitea. Las operaciones de git sobre el vault van por `os/exec` (binario `git`), no go-git. La web va detrás de Cloudflare Access y `/mcp` con tokens propios por agente (`docs/adr/ADR-004-autenticacion.md`).

## Diseño

`docs/design/`: `DESIGN.md` es la fuente de verdad visual y de producto; `README.md` es la entrada.

## Go

Módulo `github.com/duv0-x/scriba`, Go 1.26. En esta Mac el `go` de `/usr/local/go` es 1.23: usar el de brew con `export PATH=/opt/homebrew/bin:$PATH`. Verificar con `go vet ./... && go test ./...`.

## Credenciales

El servidor nunca lee credenciales de variables de entorno ni del repo: la integración con GitHub se configura desde la pantalla de Conectores de la propia aplicación. El token de agente del plugin de Claude Code (`SCRIBA_TOKEN`) sí vive en el entorno del cliente (ADR-007).
