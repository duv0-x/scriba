---
title: "Conector Kindle: EPUB generado en Scriba y envío por Resend"
number: 12
date: 2026-11-25
domain: integraciones
status: "aceptado"
tags: [kindle, epub, resend, conectores, secretos]
---

# ADR-012: Conector Kindle: EPUB generado en Scriba y envío por Resend

## Contexto

El dueño quiere leer notas de Scriba (planes, ADR, runbooks) en su Kindle, enviándolas desde el lector web. Scriba es público y multi-instancia (ADR-001), así que no puede depender de Papiro ni del dominio del dueño. Papiro ya genera EPUB que Amazon acepta y los envía por Resend. Spec: `docs/specs/2026-11-25-scriba-conector-kindle.md` en la base de conocimiento.

## Decisión

1. **Resend llamado desde Scriba**, configurable por instancia en la pantalla Conectores: correo de la Kindle, remitente y API key. Conector activo = los tres datos presentes.
2. **La API key se guarda cifrada en `state.db`** con el mecanismo de ADR-006 (AES-256-GCM, llave en `secret.key`). Nunca vuelve al HTML, ni aparece en errores, logs o en el EPUB.
3. **EPUB portado de Papiro**, en el paquete `internal/kindle`: misma hoja de estilo, sin portada ni imágenes. Se escribe a mano con `archive/zip` (sin `go-epub`): `mimetype` primero y sin comprimir, `META-INF/container.xml`, OPF con `dc:title`, `dc:language` y `dc:identifier`, `nav.xhtml` y `toc.ncx`.
4. El cuerpo es el HTML de `internal/render`, pasado a XHTML con `golang.org/x/net/html`. **Los wikilinks salen como texto plano**; los enlaces externos se conservan. Cabecera visible con título, fecha y ruta de la nota.
5. **Una nota por envío.** El nombre del adjunto es ASCII y se deriva del título, porque Send to Kindle lo muestra como título del documento.
6. No se embeben las fuentes Newsreader de Papiro: allí solo las usa la portada, y el CSS deja la fuente al lector.

## Alternativas descartadas

- Delegar el envío en Papiro: ata una instancia pública al servicio del dueño.
- SMTP genérico: más configuración y otro protocolo que probar; Resend ya es lo que usa el dueño.
- Adjuntar HTML o `.md`: `.md` no es formato de Send to Kindle y el HTML deja el aspecto a la conversión de Amazon.
- Módulo Go compartido con Papiro: un tercer repo para unas 200 líneas estables.
- Dependencia `go-shiori/go-epub`: la estructura necesaria es poca y se evita una dependencia.
- Variable de entorno o Secret Manager para la key: ADR-006 prohíbe credenciales en el entorno del servidor.
- Wikilinks como enlaces absolutos a la instancia: en la Kindle no navegan y la web está detrás de Cloudflare Access (ADR-004).

## Consecuencias

- Hay dos copias del generador de EPUB (Papiro y Scriba) que pueden divergir; se acepta.
- Amazon descarta en silencio lo que llega de un remitente no aprobado aunque Resend responda 200: el conector lleva un botón de prueba y un texto de ayuda.
- Las imágenes, la portada y los diagramas mermaid no llegan a la Kindle (mermaid sale como código).
- Las tablas anchas se ven apretadas en pantallas pequeñas.
