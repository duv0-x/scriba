---
name: scriba-documentar
description: Escribir en la base de conocimiento Scriba (MCP `scriba`) — notas de ticket, planes, ADR, runbooks, postmortems, referencias y el journal diario. Úsala al cerrar un ticket o una tarea, al terminar la sesión de trabajo, cuando el usuario pida documentar o guardar lo aprendido, o cuando haya que pasarle un plan para revisión.
---

# Documentar en Scriba

Cada escritura queda como un commit a nombre de tu agente. Antes de crear una nota nueva, busca con `kb_search` si ya existe una sobre lo mismo y, si existe, actualízala en lugar de duplicarla.

## 1. ¿Amerita nota?

**Sí**: plan de implementación, decisión de arquitectura o tecnología, investigación con conclusiones, cambio con impacto en producción, procedimiento operativo que emergió del trabajo, o un problema no trivial y cómo se resolvió.

**No**: tareas triviales, fixes simples y autoexplicativos, refactors menores sin decisiones de diseño. El journal del día se escribe siempre.

## 2. ¿En qué carpeta?

| Carpeta | Qué va ahí |
|---------|------------|
| `journal/` | Una entrada por día: `YYYY-MM-DD.md` |
| `tickets/` | Una nota por ticket o tarea: problema, contexto, solución y lecciones |
| `plans/` | Planes de implementación, antes de construir |
| `decisions/` | ADR: qué se decidió, por qué y qué se descartó |
| `runbooks/` | Procedimientos paso a paso, con validación y rollback |
| `postmortems/` | Incidentes: línea de tiempo, root cause y acciones correctivas |
| `references/` | Conocimiento validado y reutilizable: comandos, conceptos, enlaces |
| `drafts/` | Borradores que aún no tienen su lugar definitivo |

El trabajo en curso vive en `tickets/`, `plans/` o `drafts/`; lo validado pasa a `decisions/`, `runbooks/` o `references/`. Las plantillas están en `_templates/` (`ticket`, `adr`, `runbook`, `postmortem`): léelas con `kb_read` y sigue su estructura.

## 3. Naming

Todo en kebab-case y minúsculas. Las notas fechadas empiezan por la fecha `YYYY-MM-DD`:

| Tipo | Patrón | Ejemplo |
|------|--------|---------|
| Journal | `journal/YYYY-MM-DD.md` | `journal/2026-11-16.md` |
| Ticket | `tickets/YYYY-MM-DD-<id>-descripcion-corta.md` | `tickets/2026-11-16-api-42-timeout-login.md` |
| Plan | `plans/YYYY-MM-DD-<id>-slug.md` | `plans/2026-11-16-api-42-cache-de-sesiones.md` |
| ADR | `decisions/ADR-NNN-titulo.md` | `decisions/ADR-003-cola-de-eventos.md` |
| Runbook | `runbooks/nombre-del-proceso.md` | `runbooks/rotar-certificados.md` |

## 4. Frontmatter

YAML al inicio, entre líneas `---`, con las claves **en inglés**: `title`, `date` (`YYYY-MM-DD`), `status` y `tags`. El cuerpo va en el idioma del usuario.

```yaml
---
title: "Timeout en el login"
date: "2026-11-16"
status: "open"
tags: [api, auth]
---
```

## 5. Wikilinks

Enlaza las notas con `[[nombre-de-la-nota]]` (sin `.md`), o `[[ruta/nota|texto]]` si el nombre es ambiguo. Son las aristas del grafo y lo que alimenta `kb_backlinks`. Enlaza siempre: ticket → `[[YYYY-MM-DD]]` del journal; journal → tickets y planes del día; ADR y runbooks relacionados entre sí.

## 6. Cómo escribir

- **Nota completa** (nueva o reescrita): `kb_write` con la ruta y el contenido entero, frontmatter incluido. Reemplaza el archivo: para modificar una nota existente, léela antes con `kb_read`.
- **Añadir al final** sin reescribir: `kb_append` (crea la nota si no existe).
- **Journal del día**: `kb_log_journal` con un título y el cuerpo; añade una sección `## título` a `journal/YYYY-MM-DD.md` y crea la nota si no existe. Úsalo **al cerrar la sesión**.

Qué va en el journal: qué tarea se trabajó y por qué, las decisiones y su razonamiento, los descubrimientos no obvios, wikilinks a las notas del día y qué quedó pendiente. No es un resumen del código: es lo que se aprendió o decidió.

## 7. Plan para revisión

Cuando el usuario tenga que revisar un plan antes de implementarlo:

1. Escríbelo con `kb_write` en `plans/YYYY-MM-DD-<id>-slug.md` (`status: "proposed"`).
2. Llama a `kb_open` con esa ruta y dale al usuario la URL que devuelve para que lo lea en el lector web.
3. Espera su aprobación; al aprobarlo, actualiza el `status` y enlaza el plan desde el ticket y el journal.
