---
title: "Índice de búsqueda FTS5 derivado y reconstruible"
number: 3
date: 2026-11-16
domain: search
status: "aceptado"
tags: [search, sqlite, fts5, index]
---

# ADR-003: Índice de búsqueda FTS5 derivado y reconstruible

## Contexto

Agentes y personas necesitan buscar en cientos de notas en español. La instancia corre en una Raspberry Pi 3B+ con 1 GB de RAM compartida con Gremio, así que no hay margen para un motor de búsqueda aparte ni para calcular embeddings. El Markdown es la fuente de verdad ([ADR-002](ADR-002-vault-markdown-y-git.md)), de modo que el índice puede perderse sin perder datos.

## Decisión

### 1. SQLite FTS5

- La búsqueda de texto completo usa una tabla virtual **FTS5** en el mismo SQLite de Scriba, con `tokenize = 'unicode61 remove_diacritics 2'`, para que "configuración" y "configuracion" coincidan en notas en español.
- FTS5 va embebido en el binario vía la extensión `ext/fts5` de ncruces: sin cgo y sin dependencias del sistema.

### 2. Índice derivado

- El índice se reconstruye al arrancar a partir de los `.md` del vault y se mantiene al día en cada escritura.
- Borrar la base de datos nunca pierde notas: basta con reiniciar.

### 3. Sin embeddings en la Pi

No se calculan embeddings en la instancia. La búsqueda semántica queda para el futuro, con embeddings calculados fuera de la Pi.

## Consecuencias positivas

- Un solo archivo SQLite, sin servicios extra, cabe en la RAM de la Pi.
- Búsqueda insensible a tildes, que es lo que se espera en español.
- El índice nunca es una fuente de inconsistencia: ante la duda se reconstruye.

## Consecuencias negativas

- Sin búsqueda semántica en el MVP: solo coincidencia de términos.
- Reconstruir al arrancar cuesta tiempo con vaults grandes.
- `unicode61` no hace stemming: "nota" y "notas" no coinciden sin prefijos (`nota*`).

## Alternativas consideradas

| Alternativa | Razón de descarte |
|-------------|-------------------|
| Bleve u otro motor en Go | Otra dependencia y otro índice que mantener, sin ventaja clara a esta escala |
| Meilisearch / Elasticsearch | Un servicio aparte que no cabe en 1 GB compartido |
| Embeddings locales | Demasiada CPU y RAM para la Pi 3B+ |
| Índice como fuente de verdad | Contradice [ADR-002](ADR-002-vault-markdown-y-git.md) |

## Links relacionados

- [ADR-002: vault en Markdown y git](ADR-002-vault-markdown-y-git.md).
- FTS5: https://www.sqlite.org/fts5.html.
