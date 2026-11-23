---
name: scriba-contexto
description: Buscar contexto previo en la base de conocimiento Scriba (MCP `scriba`) antes de empezar. Úsala antes de una tarea técnica, al recibir un ticket, al toparte con un error que pudo pasar antes o antes de tomar una decisión de arquitectura.
---

# Buscar contexto en Scriba

Objetivo: no re-resolver algo ya resuelto ni contradecir una decisión anterior. Scriba guarda notas de sesiones anteriores (tuyas y de otros agentes): journal, tickets, planes, ADR, runbooks y postmortems.

## Cuándo buscar

- **Antes de una tarea técnica**: por si ya hay un plan, una nota o un journal sobre lo mismo.
- **Al recibir un ticket**: notas del mismo ticket, servicio o problema parecido (`tickets/`, `plans/`).
- **Ante un error**: `postmortems/` y `tickets/`, por si ya tiene root cause conocido.
- **Antes de decidir arquitectura**: `decisions/`, para no contradecir un ADR vigente.
- **Antes de escribir un runbook**: `runbooks/`, por si ya existe uno equivalente.

## Cómo

1. Saca 2–4 palabras clave: el ID del ticket, el servicio o repositorio, el mensaje de error y la tecnología.
2. Lanza una `kb_search` por cada una (consultas cortas; varias búsquedas acotadas rinden más que una larga).
3. Lee con `kb_read` las notas que parezcan relevantes, completas.
4. Amplía con `kb_backlinks` sobre las notas clave: lo que las enlaza suele ser el ticket, el plan o el journal relacionado. `kb_list` sirve para ver qué hay en una carpeta (`plans/`, `decisions/`…).
5. **Dile al usuario qué encontraste** (rutas y en una línea qué aporta cada nota), **o que no había nada**, antes de empezar a trabajar.

Si ya existe un plan para la tarea en `plans/`, díselo al usuario y pregúntale si lo retoma o prefiere uno nuevo.

Si las tools de `scriba` no responden (falta `SCRIBA_URL` o `SCRIBA_TOKEN`, o el token se revocó), díselo al usuario y sigue sin el contexto; no inventes lo que habría en la base.
