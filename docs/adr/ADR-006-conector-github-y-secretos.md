---
title: "Conector de GitHub por device flow y secretos cifrados"
number: 6
date: 2026-11-16
domain: security
status: "aceptado"
tags: [github, oauth, device-flow, secrets, connectors]
---

# ADR-006: Conector de GitHub por device flow y secretos cifrados

## Contexto

Scriba importa una base existente desde GitHub y, tras cada escritura, empuja los commits al repositorio de origen (ADR-002). Para eso necesita una credencial de GitHub. El dueño decidió que ninguna credencial vive en variables de entorno ni en el repositorio: la integración se configura desde la pantalla de **Conectores** de la propia aplicación.

## Decisión

### 1. OAuth device flow

El conector de GitHub usa el **device flow** de OAuth. Scriba pide un código a GitHub, lo muestra en Conectores, el usuario lo introduce en `github.com/login/device` y Scriba recibe el token por sondeo. No hace falta callback ni client secret.

- La OAuth App "Scriba" se registra una vez con *Enable Device Flow*. Su `client_id` es público y va como constante en el binario (`github.DefaultClientID`); `--github-client-id` permite usar otra app.
- Scope `repo`, necesario para clonar y empujar repositorios privados. La UI lo explica y ofrece desconectar.

### 2. Secretos cifrados en la instancia

- El token se guarda en `state.db`, cifrado con **AES-256-GCM** (nonce aleatorio de 12 bytes prefijado).
- La llave son 32 bytes aleatorios en `secret.key`, junto a la base y fuera del vault, con permisos `0600`. Se genera en el primer arranque; si existe con permisos más abiertos, Scriba no arranca.
- El token solo existe descifrado en memoria dentro del handler que lo usa. Nunca va al HTML, a los logs ni a la URL de git: se entrega a git con un `GIT_ASKPASS` temporal.

### 3. Estado separado del índice

`state.db` (conectores, ajustes) es un archivo distinto de `scriba.db`. El índice se puede borrar y reconstruir (ADR-003); el estado no.

## Consecuencias positivas

- Ninguna credencial en variables de entorno, archivos de configuración ni en el repositorio.
- Conectar GitHub es un clic y un código; no hay que crear tokens a mano.
- Quien copie solo el vault o solo la base no obtiene el token en claro.

## Consecuencias negativas

- El scope `repo` da acceso a todos los repositorios privados de la cuenta, no solo al vault.
- La llave y la base viven en el mismo disco: protege contra quien copia la base, no contra quien tiene el disco completo (ver el riesgo de cifrado en reposo del plan).
- Cada instancia depende de una OAuth App registrada.

## Alternativas consideradas

| Alternativa | Por qué no |
|---|---|
| Token en variable de entorno o `secrets.env` | Descartada por el dueño: las credenciales se gestionan desde Conectores |
| Token fine-grained pegado en la UI | Permiso más acotado (un solo repo), pero obliga a crearlo a mano en GitHub; el dueño prefirió el device flow |
| GitHub App | Acceso por repo y tokens cortos, pero cada instancia debe registrar su app y custodiar una llave privada |

## Links relacionados

- ADR-002: vault en Markdown y git.
- ADR-004: autenticación de la web y de `/mcp`.
- Plan: `/Users/lx-duv0-x/Documents/ia-kb/plans/2026-11-16-scriba-1-mvp-y-migracion.md` (PR2).
