# Base de conocimiento

Esta carpeta es una base de conocimiento en Markdown plano. La escriben agentes de IA y la leen personas. Cada nota es un archivo `.md` con frontmatter YAML al inicio; el repositorio git que la contiene guarda la historia de cada cambio.

## Estructura

| Carpeta | Qué va ahí |
|---------|------------|
| `journal/` | Una entrada por día con lo que se hizo, nombrada `YYYY-MM-DD.md` (por ejemplo `2026-11-16.md`). |
| `tickets/` | Una nota por ticket o tarea: problema, contexto, solución y lecciones. |
| `plans/` | Planes de implementación aprobados antes de empezar a construir. |
| `decisions/` | Architecture Decision Records (ADR): qué se decidió, por qué y qué se descartó. |
| `runbooks/` | Procedimientos operativos paso a paso, con validación y rollback. |
| `postmortems/` | Análisis de incidentes: línea de tiempo, root cause y acciones correctivas. |
| `references/` | Conocimiento validado y reutilizable: comandos, conceptos, enlaces. |
| `drafts/` | Borradores que todavía no tienen su lugar definitivo. |
| `_templates/` | Plantillas para crear notas nuevas (`ticket`, `adr`, `runbook`, `postmortem`). |

## Convenciones

- **Nombres de archivo**: en minúsculas y con guiones (`migracion-base-de-datos.md`). Las notas fechadas empiezan por la fecha en formato `YYYY-MM-DD` (`2026-11-16-migracion-base-de-datos.md`); las del journal son solo la fecha.
- **Enlaces**: entre notas se usan wikilinks, `[[nombre-de-la-nota]]`, sin la extensión `.md`.
- **Frontmatter**: YAML al inicio del archivo, entre líneas `---`, con las claves en inglés: `title`, `date` (formato `YYYY-MM-DD`), `status` y `tags`.

```yaml
---
title: "Migración de la base de datos"
date: "2026-11-16"
status: "open"
tags: [database, migration]
---
```
