---
title: "Edición en línea en el lector: CodeMirror 6, autosave y commits web diferidos por ruta"
number: 14
date: 2026-11-27
domain: editor
status: "aceptado"
tags: [editor, autosave, git, codemirror, concurrencia]
---

# ADR-014: Edición en línea en el lector: CodeMirror 6, autosave y commits web diferidos por ruta

## Contexto

Hoy se edita en `/k/{kb}/editar/{path}` (textarea + vista previa, guardado manual). Se quiere editar sobre el lector como en Obsidian, sin botón Guardar. Cada escritura es un commit que se empuja a GitHub, así que un autosave ingenuo llena el historial. Los agentes escriben por MCP en las mismas notas. Spec en la KB: `docs/specs/2026-11-26-scriba-edicion-en-linea-live-preview.md`; ADR-006 y ADR-007 de la KB (`decisions/personal/`). Epic duv0-x/scriba#56: #53 (servidor, este), #54 (CodeMirror 6 detrás de un flag), #55 (presencia, activación, eliminación de `/editar`).

## Decisión

1. **Editor: CodeMirror 6** con `@codemirror/lang-markdown` y una extensión Live Preview propia, empaquetado una vez con esbuild (script en `scripts/`) y vendorizado en `internal/web/static/` (se commitea, como el CSS). Excepción acotada al "templ + htmx" de ADR-005. El documento siempre es el Markdown: nunca se reformatea. Sin JS la nota se lee pero no se edita.
2. **Autosave**: `PUT /k/{kb}/n/{path}` (la ruta del lector) con body JSON `{"content", "base", "force"?}`. Respuestas: `200 {"base": sha256 nuevo}`; `409 {"current", "base"}` si el hash del disco no es `base` (y no hay `force` válido); `423 {"agent"}` si un agente tiene presencia en la nota; `400`/`404`/`413 {"error"}` para contenido o ruta inválidos, nota inexistente o más de 1 MB. `force` = el `base` del 409, con la semántica de `writer.WriteIfUnchanged`. El hash es SHA-256 hex del contenido tal como queda en disco (el Writer normaliza a un único `\n` final): el cliente debe adoptar el `base` que devuelve el 200. Mismos límites y validación que el editor viejo; lo protege `sameOrigin` como a todo lo que no es GET.
3. **Flush explícito**: `PUT …?flush=1`. Con body, guarda y además hace el commit pendiente de esa ruta en el momento. Con body vacío, solo hace el commit pendiente y responde `204`. El cliente lo usa en `pagehide` (`fetch` con `keepalive`) y al navegar a otra nota.
4. **Commit web diferido por ruta** (`writer.Autosave`): el archivo se escribe, se reindexa y se emite el evento al momento; el commit `web: actualiza <path>` se hace tras `WebIdle` (60 s) sin otro guardado web en esa ruta, en un flush, al cerrar la KB (shutdown ordenado o borrado en el Registry: `Writer.Close`) o justo antes de cualquier otra escritura en esa ruta, para que la historia quede `web: …` → `<agente>: …`. Sin amend ni force-push; el `Pusher` solo cambia en lo del punto 6.
5. **Commits por ruta**: el Writer deja `gitvault.CommitAll` (`git add -A`) y usa `gitvault.CommitPaths` (`git add -A -- <rutas>` + `git commit -- <rutas>`, con `--literal-pathspecs`), para que el commit de un agente no se lleve los cambios web pendientes de otra nota con la autoría equivocada. `CommitAll` queda solo para el commit inicial de una KB. Las rutas ignoradas por el `.gitignore` del vault se saltan sin error, como hacía `add -A`.
6. **El Pusher hace los commits web pendientes antes del pull** (`Pusher.BeforePull` → `Writer.FlushPendingHeld`, con el lock del repo tomado), y `Pull` usa `--autostash` solo para lo que quede sucio por fuera de Scriba. Si al reaplicar el stash hay conflicto, las rutas en conflicto vuelven a la versión de `HEAD`, el cambio local queda en `git stash` y el ciclo devuelve un error (se ve en Conectores).
7. **Barrido al arrancar** (`Writer.Sweep`, al abrir cada KB): si el proceso murió con commits web pendientes, las rutas sucias (`gitvault.Dirty`) se commitean en un solo commit con autor `web` (`web: actualiza <path>` o `web: actualiza <N> rutas pendientes`).
8. **Los agentes tienen prioridad**: mientras uno tiene presencia en la nota (`Presence.Writing`, ventana de 60 s), el PUT responde 423 y la web queda en solo lectura.
9. **Eco propio**: `events.Event` y el evento SSE `changed` llevan `hash` (SHA-256 del contenido escrito), para que la pestaña que guardó reconozca su propio eco comparándolo con su `base`.
10. **El lector incrusta**, solo si la nota es editable, `<script id="nota-editable" type="application/json">` con `raw` (Markdown crudo), `base`, `links` (mapa wikilink crudo → ruta resuelta, de `Outlinks`) y `agent` (agente con presencia al abrir, o vacío).

## Consecuencias

### Positivas

- Disco e índice siempre al día.
- Historial legible.
- Los agentes nunca encuentran un bloqueo de la web.
- Se reutiliza `WriteIfUnchanged`/`force`.

### Negativas

- Estado nuevo en memoria (commits pendientes; si el proceso muere, lo cubre el barrido).
- El dueño no puede editar mientras un agente escribe la nota.
- Los cambios hechos en el vault por fuera de Scriba ya no se cuelan en el commit del siguiente agente: quedan hasta el barrido del siguiente arranque.
- Si un cambio hecho por fuera de Scriba choca con el remoto al reaplicar el autostash, se queda en `git stash` y hay que recuperarlo a mano.
- Un bundle de CM6 de unos 200–300 KB, que solo se carga en notas editables.
- La actividad de los guardados web se registra una vez por commit diferido, no por guardado.

## Activación (scriba#55)

- El editor en línea se monta en toda nota editable, sin flag. Se retiraron `GET/POST /editar/{path}`, `POST /vista-previa`, `editor.templ` y el `editor.js` viejo; `GET /k/{kb}/editar/{path}` responde 301 al lector de la misma nota.
- **Presencia que expira:** el SSE reenvía `presence` en el instante en que vence la presencia más próxima (`At` + ventana), además de al conectar y tras cada `changed`. El cliente no lleva temporizadores propios de presencia.
- **Solo lectura por agente:** con presencia de un agente en la nota abierta, el editor se oculta y vuelve a verse la prosa del servidor, que `live.js` sigue actualizando con la reproducción en vivo. Antes se intenta guardar lo pendiente; si el servidor responde 409 o 423, al terminar la presencia se muestra el conflicto.
- **Recarga:** al expirar la presencia (o ante un cambio ajeno sin cambios locales) el editor pide `GET <nota>?datos=1`, que devuelve el mismo JSON que `nota-editable` (raw, base, links, agent) sin marcar la nota como leída.
- **Eco propio:** un `changed` con agente `web` cuyo hash es uno de los `base` que devolvió esta pestaña no recarga ni reproduce. Otra pestaña cuenta como cambio ajeno.
- **Flush:** al desmontar (navegación parcial o atrás) y en `pagehide` se envía lo pendiente con `PUT …?flush=1` y `keepalive`; en `visibilitychange→hidden`, guardado con `keepalive`. `beforeunload` solo avisa si queda algo sin confirmar.

## Alternativas descartadas

| Alternativa | Razón |
|---|---|
| Un commit por guardado | historial lleno de micro-commits |
| Amend del último commit web | el push es inmediato: obligaría a force-push al respaldo |
| Mantener `git add -A` | mezclaría cambios web pendientes en commits de agentes |
| Bloquear al agente mientras la web edita | los agentes son los escritores principales |
| Merge de tres vías | más lógica y riesgo para un caso raro |
| WYSIWYG sobre un árbol de documento (TipTap, Milkdown) | reformatea el Markdown de los agentes |
| Bloques renderizados por el servidor y edición por bloque | no es la experiencia pedida |

## Links relacionados

- [ADR-002: El vault: Markdown plano dentro de un repositorio git](ADR-002-vault-markdown-y-git.md)
- [ADR-005: Stack técnico](ADR-005-stack.md)
- [ADR-009: Navegación parcial con hx-boost](ADR-009-navegacion-parcial.md)
- [ADR-013: Varias bases de conocimiento por instancia: Registry en un proceso](ADR-013-varias-kbs-registry.md)
