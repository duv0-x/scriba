---
title: "Stack técnico"
number: 5
date: 2026-11-16
domain: architecture
status: "aceptado"
tags: [go, sqlite, templ, htmx, tailwind, mcp, stack]
---

# ADR-005: Stack técnico

## Contexto

Scriba la mantiene la misma persona que Gremio y se despliega en la misma Raspberry Pi. Gremio ya validó un stack (Go puro, SQLite, templ + htmx + Tailwind, GoReleaser, despliegue GitOps) y su tooling. Repetirlo reduce lo que hay que aprender y mantener. Scriba, en cambio, tiene pocas consultas SQL, necesita git sobre el vault y expone un servidor MCP.

## Decisión

### 1. Lo mismo que Gremio

- **Go puro** (`CGO_ENABLED=0`), un binario con todo embebido.
- **SQLite** con ncruces y migraciones con **goose**.
- Web renderizada en el servidor con **templ + htmx** y estilos con **Tailwind standalone**. El código generado (templ, CSS) se commitea.
- Binarios e imagen OCI con **GoReleaser**; despliegue **GitOps** en la Pi.

### 2. Diferencias con Gremio

- **Sin sqlc**: son pocas consultas, `database/sql` directo es suficiente.
- **git por `os/exec`** en vez de go-git: menos dependencias; el binario `git` lo instala la provisión de la Pi.
- **MCP** con el SDK oficial `github.com/modelcontextprotocol/go-sdk`.

## Consecuencias positivas

- Un solo stack para los dos proyectos: patrones, scripts y workflows reutilizables.
- Binario único sin cgo, fácil de compilar para arm64.
- El SDK oficial de MCP sigue la especificación sin implementarla a mano.

## Consecuencias negativas

- Sin sqlc, los errores de SQL se detectan en tests y no al generar código.
- `os/exec` depende de la versión de `git` del sistema y obliga a parsear su salida.

## Alternativas consideradas

| Alternativa | Razón de descarte |
|-------------|-------------------|
| sqlc (como Gremio) | Tooling y código generado que no compensan con tan pocas consultas |
| go-git | Dependencia grande y cobertura incompleta frente al `git` real |
| Implementar MCP a mano | Duplica el SDK oficial y se desalinea con la especificación |

## Links relacionados

- [ADR-002: vault en Markdown y git](ADR-002-vault-markdown-y-git.md).
- [ADR-003: índice FTS5 derivado](ADR-003-indice-fts5-derivado.md).
- Stack de Gremio: https://github.com/duv0-x/gremio/tree/main/docs/adr.
