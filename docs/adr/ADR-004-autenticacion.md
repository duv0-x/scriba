---
title: "Autenticación de la web y del endpoint MCP"
number: 4
date: 2026-11-16
domain: security
status: "aceptado"
tags: [auth, security, mcp, cloudflare-access]
---

# ADR-004: Autenticación de la web y del endpoint MCP

## Contexto

Scriba tiene dos puertas: la web, que usan personas desde un navegador, y `/mcp`, que usan agentes de IA desde un cliente MCP. La instancia ya se publica con Cloudflare Tunnel, igual que Gremio. Un agente no puede hacer un login interactivo, y un SSO que solo protege la interfaz no protege un endpoint que acepta peticiones sin token.

## Decisión

### 1. Web detrás de Cloudflare Access

En la instancia, la web va detrás de **Cloudflare Access**. El MVP no tiene login propio.

### 2. `/mcp` con tokens propios por agente

- `/mcp` lo protege la aplicación con un token propio por agente, enviado como `Authorization: Bearer`.
- Scriba guarda solo el **sha256** del token; el valor en claro se muestra una única vez al crearlo.
- Cada token se puede revocar por separado sin afectar a los demás agentes.

### 3. Login propio fuera del MVP

Un login propio con enlace mágico, como el [ADR-009 de Gremio](https://github.com/duv0-x/gremio/blob/main/docs/adr/ADR-009-autenticacion-del-panel.md), queda para después del MVP.

## Consecuencias positivas

- Cero código de autenticación de personas en el MVP.
- Cada agente tiene su identidad: se puede revocar uno y los commits muestran quién escribió qué.
- Un token filtrado de la base de datos no sirve, porque solo se guarda su hash.

## Consecuencias negativas

- La web depende de Cloudflare Access: quien la corra fuera de la instancia tiene que poner su propio proxy de autenticación.
- Dos mecanismos de autenticación distintos que mantener.

## Alternativas consideradas

| Alternativa | Razón de descarte |
|-------------|-------------------|
| Cloudflare Access también para `/mcp` | Un agente no puede hacer el login interactivo; los service tokens de Access no identifican a cada agente |
| Un solo token compartido para todos los agentes | No se puede revocar ni atribuir por agente |
| Login propio en el MVP | Más alcance sin necesidad mientras haya un solo usuario detrás de Access |

## Links relacionados

- [ADR-002: vault en Markdown y git](ADR-002-vault-markdown-y-git.md) (autor de los commits por agente).
