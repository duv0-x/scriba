---
title: "MCP para agentes y tokens de agente"
number: 7
date: 2026-11-16
domain: agents
status: "aceptado"
tags: [mcp, agents, auth, tokens]
---

# ADR-007: MCP para agentes y tokens de agente

## Contexto

Scriba existe para que los agentes de IA escriban la base de conocimiento y las personas la lean. Los agentes (Claude Code, opencode, Kimi, Codex, goose…) hablan MCP. Necesitan leer, buscar y escribir notas, y cada escritura debe quedar atribuida al agente que la hizo (ADR-002: commit por escritura). Un agente no puede hacer el login interactivo de la web (ADR-004).

## Decisión

### 1. MCP por streamable HTTP en `/mcp`

Servidor MCP con el SDK oficial (`github.com/modelcontextprotocol/go-sdk`), transporte *streamable HTTP*, montado en `/mcp` del mismo binario. Ocho tools: `kb_search`, `kb_read`, `kb_list`, `kb_backlinks`, `kb_write`, `kb_append`, `kb_log_journal` y `kb_open`. Los errores de validación vuelven como resultado de tool (`IsError`) con un mensaje en español, no como error de protocolo.

### 2. Tokens de agente

- Formato `scb_` + 43 caracteres base62 de `crypto/rand` (≥ 256 bits). En `state.db` solo se guarda su SHA-256; la verificación compara en tiempo constante.
- Se crean y revocan desde la web (`/agentes`, el valor se muestra una sola vez) y por CLI (`scriba token`). Nunca en variables de entorno del servidor.
- `Authorization: Bearer` obligatorio en `/mcp`; sin token válido, 401 antes de llegar al SDK.
- **El nombre del token es la identidad del agente**: autor de los commits (`<agente> via Scriba`) y de la actividad. El SDK ata cada sesión MCP a ese agente.

### 3. Escritura

Todas las escrituras pasan por un `writer` que las serializa, valida la ruta (solo `.md`, dentro del vault, sin carpetas ocultas ni `_templates`, ≤ 1 MB, UTF-8), escribe de forma atómica, reindexa la nota al momento, hace commit y, si el conector de GitHub tiene push activo, dispara un push con debounce de 30 s.

### 4. Protección de localhost del SDK desactivada

El SDK rechaza por defecto las peticiones a `127.0.0.1` con un `Host` distinto de localhost (defensa contra DNS rebinding). Detrás del túnel de Cloudflare todas las peticiones llegan así, de modo que se desactiva: el Bearer obligatorio ya impide que una página maliciosa use el endpoint.

## Consecuencias positivas

- Cualquier agente compatible con MCP se conecta con una URL y un token.
- Cada cambio en la base tiene un autor verificable en el historial de git.
- Revocar un agente no afecta a los demás.

## Consecuencias negativas

- Quien tenga un token puede escribir cualquier nota: no hay permisos por carpeta en el MVP.
- `kb_write` reemplaza el archivo completo; el "en vivo" de la web (PR3b) reproduce el cambio, no lo transmite mientras se escribe.

## Alternativas consideradas

| Alternativa | Por qué no |
|---|---|
| MCP por stdio con un binario local que hable con la instancia | Obliga a instalar algo en cada máquina; HTTP basta con una URL |
| Service tokens de Cloudflare Access para `/mcp` | Atan la identidad a Cloudflare y no distinguen agentes en los commits |
| Un único token compartido | No se puede revocar un agente sin cortar a todos ni saber quién escribió qué |

## Links relacionados

- ADR-002: vault en Markdown y git.
- ADR-004: autenticación de la web y de `/mcp`.
- ADR-006: conector de GitHub y secretos.
