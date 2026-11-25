---
title: "OAuth para usar Scriba como conector personalizado"
number: 10
date: 2026-11-24
domain: security
status: "aceptado"
tags: [oauth, mcp, auth, connectors, cloudflare-access]
---

# ADR-010: OAuth para usar Scriba como conector personalizado

## Contexto

`/mcp` solo aceptaba tokens `Bearer` creados a mano (ADR-004, ADR-007). Los conectores personalizados de Claude (web, Desktop y móvil) no admiten un header: autentican con la autorización OAuth de la especificación MCP y se conectan desde los servidores de Anthropic.

## Decisión

Scriba es a la vez *resource server* y *authorization server* de su propio `/mcp`. Enmienda la sección 2 de ADR-004; los tokens estáticos siguen igual.

### 1. Endpoints

| Ruta | Estándar | Detrás del proxy de autenticación |
|---|---|---|
| `/.well-known/oauth-protected-resource` (y `/mcp`) | RFC 9728 | No |
| `/.well-known/oauth-authorization-server` | RFC 8414 | No |
| `/oauth/register` | RFC 7591 | No |
| `/oauth/token` | code + PKCE y refresh | No |
| `/oauth/authorize` | consentimiento | **Sí** |

El `401` de `/mcp` lleva `WWW-Authenticate: Bearer resource_metadata="…"`.

### 2. Identidad

La pantalla de consentimiento es la única puerta: queda detrás de Cloudflare Access (igual que `/agentes`, que ya crea tokens) y exige mismo origen en el `POST`. Scriba no tiene login propio. El dueño elige el nombre del agente, que firma los commits (ADR-007).

### 3. Clientes

- Solo clientes públicos (`token_endpoint_auth_method: none`) con PKCE `S256` obligatorio.
- `redirect_uri` en lista blanca: el callback de Claude y loopback `http` (puerto libre, RFC 8252).
- Registro sin estado: el `client_id` es el nombre y los redirects cifrados con una subclave de `secret.key` (HKDF). Un registro anónimo no escribe en la base.

### 4. Tokens

- La concesión es una fila de `agent_tokens`: access token `scb_…` de 1 h y refresh token `scr_…` de 90 días deslizantes; solo se guardan sus SHA-256.
- Cada refresh rota los dos. Presentar el refresh anterior revoca la concesión (RFC 9700).
- Authorization codes en memoria, de un solo uso y 5 minutos.
- Se revoca desde **Agentes**. No hay endpoint de revocación.

## Consecuencias positivas

- Scriba se añade a Claude con solo la URL.
- Mismo modelo de identidad y revocación que los tokens estáticos.
- Sin dependencias nuevas.

## Consecuencias negativas

- Código de autenticación propio que mantener.
- El proxy debe dejar pasar las rutas públicas y proteger `/oauth/authorize`: abrir `/oauth/*` entero permitiría a cualquiera emitirse un token.
- Un conector de otro proveedor requiere añadir su callback a la lista.
- La lista blanca impide registrar un destino propio, no el phishing por completo: alguien puede iniciar la conexión desde su propia cuenta de Claude y enviarte el enlace de consentimiento. Aprueba solo conexiones que acabas de iniciar.
- Si se pierde la respuesta de un refresh y el cliente reintenta, o refresca dos veces a la vez con el mismo token, hay que reconectar.
- Un binario anterior a esta versión ignora la expiración: antes de volver atrás hay que revocar los conectores.

## Alternativas consideradas

| Alternativa | Razón de descarte |
|---|---|
| Managed OAuth de Cloudflare Access | Obliga a Scriba a validar el JWT de Access, rompe el Bearer propio en la misma ruta (necesitaría otra), pierde el agente por token y ata el producto a Cloudflare |
| Authorization server externo | Otro servicio que operar para un solo usuario |
| Client ID Metadata Documents | Scriba tendría que descargar URLs que elige el cliente; DCR basta para Claude |
| Tabla de clientes registrados | El registro es anónimo: crecería sin límite y habría que limpiarla |
| `redirect_uri` libre | Permite phishing de consentimiento |

## Links relacionados

- [ADR-004: autenticación](ADR-004-autenticacion.md)
- [ADR-007: MCP y tokens de agente](ADR-007-mcp-y-tokens-de-agente.md)
