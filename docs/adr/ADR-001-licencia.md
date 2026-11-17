---
title: "Licencia del software"
number: 1
date: 2026-11-16
domain: governance
status: "aceptado"
tags: [license, agpl, governance]
---

# ADR-001: Licencia del software

## Contexto

Scriba se puede hospedar como servicio: una instancia sirve a sus usuarios por red y nunca les entrega el binario. Con una licencia permisiva o con la GPL, quien hospede una versión modificada no tendría que publicar sus cambios. Gremio, el proyecto hermano con el mismo stack, ya resolvió esta pregunta con la AGPL ([ADR-004 de Gremio](https://github.com/duv0-x/gremio/blob/main/docs/adr/ADR-004-licencia.md)).

## Decisión

### 1. Software: AGPL-3.0-or-later

- Todo el código del proyecto se licencia bajo **GNU AGPL-3.0-or-later** (`LICENSE` en la raíz, texto oficial de gnu.org, el mismo archivo que usa Gremio).
- "Or later" permite adoptar una futura versión de la AGPL publicada por la FSF sin pedir permiso a cada contribuidor.

### 2. Repositorio público desde el primer release

`duv0-x/scriba` es público a más tardar desde el primer release etiquetado. Así el código de cada versión desplegada está disponible desde el principio.

## Consecuencias positivas

- Quien hospede una versión modificada como servicio debe publicar sus cambios: nadie puede cerrar el proyecto.
- Misma licencia que Gremio: un solo modelo que entender y código que se puede mover entre los dos repos sin conflictos.

## Consecuencias negativas

- Algunas empresas prohíben usar o contribuir a software AGPL: menos adopción corporativa.
- Relicenciar el proyecto más adelante requerirá el acuerdo de cada contribuidor.

## Alternativas consideradas

| Alternativa | Razón de descarte |
|-------------|-------------------|
| MIT | Permite ofrecer una versión modificada como servicio cerrado |
| Apache-2.0 | Igual que MIT frente a servicios cerrados (su ventaja son las patentes) |
| GPL-3.0 | No cubre el uso por red: un servicio modificado no tiene que publicar nada |
| AGPL-3.0-only | Cambiar de versión requeriría a todos los contribuidores |

## Links relacionados

- Texto de la licencia: [`LICENSE`](../../LICENSE) (fuente: https://www.gnu.org/licenses/agpl-3.0.txt).
