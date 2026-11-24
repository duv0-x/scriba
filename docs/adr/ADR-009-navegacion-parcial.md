---
title: "Navegación parcial con hx-boost"
number: 9
date: 2026-11-23
domain: frontend
status: "aceptado"
tags: [htmx, hx-boost, navegacion, frontend]
---

# ADR-009: Navegación parcial con hx-boost

## Contexto

Cada click en una nota recargaba la página entera. Con eso se perdían:
- el scroll y las carpetas abiertas de Folios;
- la conexión SSE, que se reabría;
- el grafo de la sidebar, que se redibujaba.

Lo que cambia entre una página y otra es solo la vista central (`main.view`). Scriba ya embebe htmx 4.0.1 (ADR-005), que trae `hx-boost`.

## Decisión

1. `<body hx-boost:inherited="target:.view select:.view swap:outerHTML">`.
   - En htmx 4 los atributos no se heredan salvo con `:inherited`. Así solo los enlaces y forms boosteados reciben `target`, `select` y `swap`; el resto de los `hx-*` (búsqueda, polling de Conectores) no los heredan.
   - Un click en un enlace interno pide la página completa, toma su `.view` y reemplaza la actual. htmx actualiza `<title>` y hace `pushState`.
   - La `.view` nueva llega con el scroll arriba.
2. `main.view` lleva `hx-history-elt`: atrás y adelante restauran solo la vista.
3. La `.view` trae `data-current` y `data-view`. Un listener de `htmx:after:settle` en el layout ajusta lo que vive fuera de ella:
   - `aria-current` y `unread` del árbol;
   - los `<details>` ancestros, que se abren;
   - la vista marcada en la nav, con la misma regla que el server;
   - los resultados de búsqueda, que se cierran.
4. No se boostean:
   - los forms POST (Bienvenida, Conectores, Agentes);
   - el form de filtros del grafo;
   - el enlace "Editar".

   El editor no lleva el atributo en `body`: es una isla de recarga completa que conserva el aviso `beforeunload` y no deja listeners de `editor.js` vivos.
5. Los scripts que viven dentro de `.view` corren en cada navegación y se limpian solos:
   - el del lector aborta los listeners de la nota anterior (`AbortController`);
   - `graph.js` inicia los `[data-grafo]` nuevos en `htmx:after:settle` y suelta los que salieron del DOM;
   - `live.js` lee la nota abierta en cada evento.
6. Lo que no trae `.view` no se intercambia:
   - Si una respuesta boosteada no trae `.view` (error en texto plano, JSON, estático), `htmx:before:swap` la cancela y hace recarga completa. htmx 4 hace swap también de 4xx/5xx.
   - `/buscar` devuelve el parcial solo a la búsqueda en vivo (`HX-Request` sin `HX-Boosted`).
7. Foco y scroll:
   - Si el enlace pulsado salió del DOM, el foco pasa a `.view` (`tabindex="-1"`).
   - Atrás y adelante ponen el scroll de `.view` arriba, porque el restore reusa el mismo elemento.

## Alternativas descartadas

- **Morph del `body` entero** (`innerMorph`): el estado de la sidebar llegaría del server sin JS, pero se re-evaluarían los scripts del layout. Además, el árbol perdería las carpetas que el usuario abrió.
- **Endpoint de fragmentos** (responder solo `.view` si hay `HX-Request`): ahorra HTML, pero duplica rutas de render. `select` basta. Se reconsidera si el árbol pesa.
- **Router propio en JS**: más código para lo mismo que hace htmx.

## Consecuencias

- Los clicks en nodos del grafo siguen con recarga completa (`location.href`).
- Un script nuevo dentro de `.view` debe tolerar correr varias veces en la misma página.
