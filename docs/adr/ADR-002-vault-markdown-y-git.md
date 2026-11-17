---
title: "El vault: Markdown plano dentro de un repositorio git"
number: 2
date: 2026-11-16
domain: storage
status: "aceptado"
tags: [vault, markdown, git, storage]
---

# ADR-002: El vault: Markdown plano dentro de un repositorio git

## Contexto

Scriba guarda notas que escriben agentes de IA y leen personas. Esas notas tienen que sobrevivir a Scriba: poder abrirse en Obsidian o en un editor cualquiera, migrarse a otra herramienta y auditarse (qué agente escribió qué y cuándo). La base actual (ia-kb) ya es Markdown con frontmatter YAML versionado en git, con cientos de notas y su historial.

## Decisión

### 1. Markdown plano como fuente de verdad

- Cada nota es un archivo `.md` con frontmatter YAML. Ese archivo es la fuente de verdad.
- Todo lo demás (índice de búsqueda, enlaces resueltos, metadatos) se deriva de los `.md` y se puede reconstruir ([ADR-003](ADR-003-indice-fts5-derivado.md)).

### 2. El vault siempre es un repositorio git

- Si al crear o importar un vault el directorio no es un repositorio git, Scriba ejecuta `git init`.
- Cada escritura de un agente o de la web es un commit, con autor `<agente> via Scriba` (p. ej. `claude via Scriba`).
- Las operaciones de git van por `os/exec` ([ADR-005](ADR-005-stack.md)).

### 3. Push opcional a GitHub

Cuando el vault está conectado a GitHub desde la pantalla de Conectores, Scriba hace `push` a `origin` con un debounce de 30 s, para agrupar ráfagas de escrituras en un solo push. Sin conector, el vault es solo local.

## Consecuencias positivas

- Sin lock-in: las notas son archivos legibles por cualquier herramienta.
- Trazabilidad completa: cada cambio tiene autor, fecha y diff, y se puede revertir.
- ia-kb se migra conservando su historial, y Obsidian local sigue funcionando con `git pull`.
- GitHub sirve de backup cuando el conector está activo.

## Consecuencias negativas

- El binario `git` es una dependencia de runtime (la provisión de la Pi lo instala).
- Un commit por escritura hace crecer el historial; habrá que vigilar el tamaño del repo.
- Las ediciones externas (un `git pull`, un editor) obligan a reindexar.

## Alternativas consideradas

| Alternativa | Razón de descarte |
|-------------|-------------------|
| Base de datos como fuente de verdad | Lock-in: las notas solo existen dentro de Scriba |
| Directorio plano de Markdown sin git | Pierde la trazabilidad de quién escribió qué y la posibilidad de revertir |

## Links relacionados

- [ADR-003: índice FTS5 derivado](ADR-003-indice-fts5-derivado.md).
- [ADR-005: stack](ADR-005-stack.md).
