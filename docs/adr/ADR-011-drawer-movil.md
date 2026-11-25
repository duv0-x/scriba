---
title: "Drawer móvil con popover y heatmap desde la actividad reciente"
number: 11
date: 2026-11-24
domain: frontend
status: "aceptado"
tags: [popover, responsive, drawer, heatmap, accesibilidad]
---

# ADR-011: Drawer móvil con popover y heatmap desde la actividad reciente

## Contexto

Bajo 720 px, `.side` y `.gfilters` tienen `display:none`: no hay Vistas, Folios, marca ni filtros del grafo. El heatmap horizontal abre en la semana más vieja. Duplicar la navegación rompería el estado de Folios y ADR-009.

## Decisión

1. El mismo `<aside class="side">` es sidebar en escritorio y `popover="auto"` bajo 720 px. Un botón hamburguesa en `.top` lo invoca. El drawer ocupa `min(84vw, 320px)` y toda la altura, con backdrop `rgb(28 34 39 / 0.28)`, sin swipe ni animación.
2. Los botones de navegación y filtros tienen target de 44×44 px e iconos de 18×18 px. `form.gfilters` sigue el mismo patrón popover no modal.
3. En escritorio, CSS resetea los estilos UA de `[popover]`. En móvil, solo `:popover-open` muestra los overlays. Clic fuera y Escape son nativos; `htmx:after:settle` y el cambio de breakpoint los cierran. No se duplica el árbol ni sus `<details>`.
4. Si la navegación partió del drawer, el foco pasa a `.view`; en los demás casos se mantiene la regla de ADR-009.
5. El heatmap agrupa días y meses en `.heat-inner`. Solo bajo 720 px, `row-reverse` inicia el scroll en la semana actual. `.heat-wrap` es una región enfocables con teclado llamada "Historial anual de notas".

## Alternativas descartadas

- `popover="manual"` con swipe y scroll snap: exige modalidad, `inert`, foco y más JavaScript a mano; el gesto no se pidió.
- `<dialog>` modal: cambia semántica y foco; la navegación es transitoria y no necesita focus trap.
- Duplicar el árbol en un drawer separado: dos estados de `<details>` que sincronizar.
- Inicializar el heatmap con `scrollLeft` en JavaScript: más código y otra inicialización tras cada navegación; CSS basta.

## Consecuencias

- Requiere Popover API (Chrome 114, Firefox 125, Safari 17); no se añade polyfill.
- El mismo DOM sirve ambos layouts, así que Folios conserva carpetas abiertas y la navegación parcial no recarga.
- Al cruzar 720 px con un overlay abierto, este se cierra para que el aside vuelva correctamente al grid.
- En escritorio no cambian la alineación del heatmap ni la presentación de sidebar/filtros.
