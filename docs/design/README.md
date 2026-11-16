# Diseño de Scriba

## Fuentes

- **Tokens y reglas:** [`DESIGN.md`](DESIGN.md). Es la fuente de verdad para la implementación (colores claro/oscuro, tipografía, espaciado, componentes, tono).
- **Pantallas de referencia:** proyecto de Google Stitch **Scriba**: https://stitch.withgoogle.com/projects/12401527686268380794
  - Sistema de diseño en Stitch: `assets/15270267426254548184` (cargado desde `DESIGN.md`).
  - El proyecto evoluciona con el tiempo; por eso aquí no se guardan capturas.

## Pantallas en Stitch

| Pantalla | Claro | Oscuro |
|---|---|---|
| Dashboard (actividad) | ✓ | ✓ |
| Lector de notas con grafo local | ✓ | ✓ |
| Editor (Markdown + vista previa en vivo) | ✓ | — |
| Grafo global | — | ✓ |

## Al cambiar el diseño

1. Edita `DESIGN.md` en este repo.
2. Sincroniza el sistema de diseño en Stitch con ese contenido (`update_design_system` del MCP de Stitch, campo `designMd`).
3. Regenera o ajusta las pantallas afectadas en Stitch.

## Avisos sobre Stitch

- Stitch ignora `labelFont`, `typography` y `spacing` del sistema de diseño y deriva su propia paleta desde los colores semilla. Lo que respeta es el `designMd`.
- Stitch inventa contenido (botones, nombres, métricas). Revisa las pantallas contra `DESIGN.md` antes de tomarlas como referencia.
- El grafo de Stitch está dibujado a mano (unos 120 nodos). La densidad real la dará la simulación de fuerzas con los datos del vault.
