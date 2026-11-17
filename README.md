# Scriba

Scriba es una base de conocimiento en Markdown que escriben tus agentes de IA y lees tú. Mantiene un índice propio para buscar rápido, expone un servidor MCP para que los agentes lean y escriban notas, y ofrece un lector web cuidado para las personas. El Markdown plano, con frontmatter YAML y versionado en git, es la fuente de verdad: el índice se puede borrar y reconstruir en cualquier momento.

## Estado

En construcción: todavía no hay un release usable.

## Cómo correrlo

```sh
go run ./cmd/scriba serve --vault <dir>
```

Si `<dir>` no existe o está vacío, Scriba abre la bienvenida: crear una base nueva, importarla desde GitHub o subir un `.zip` (Obsidian o carpetas locales). Junto al vault guarda `scriba.db` (índice reconstruible), `state.db` (conectores) y `secret.key` (llave de cifrado, `0600`).

## Conectar GitHub

Scriba se conecta a GitHub desde la pantalla **Conectores** con el device flow de OAuth: muestra un código, lo introduces en `github.com/login/device` y listo. Nunca se configuran credenciales en variables de entorno.

Para tu propia instancia necesitas una OAuth App:

1. En GitHub → *Settings* → *Developer settings* → *OAuth Apps* → *New OAuth App*.
2. Nombre `Scriba`; *Homepage URL* y *Authorization callback URL*: la URL de tu instancia.
3. Marca **Enable Device Flow** y guarda. No necesitas el client secret.
4. Pasa el *Client ID* (es público) con `--github-client-id`, o compílalo como `github.DefaultClientID`.

El token se guarda cifrado en la instancia (ver [ADR-006](docs/adr/ADR-006-conector-github-y-secretos.md)).

## Conectar un agente

Los agentes leen y escriben por MCP en `/mcp`. Cada agente usa su propio token:

1. En **Agentes** crea uno con un nombre (`claude-code-mac`, `opencode-pi`…). El token se muestra una sola vez; también puedes usar `scriba token create <nombre> --data <dir>`.
2. Regístralo en tu cliente. En Claude Code:

   ```sh
   claude mcp add --transport http scriba https://<tu-instancia>/mcp --header "Authorization: Bearer scb_…"
   ```

Cada escritura queda como un commit con el agente como autor (`claude-code-mac via Scriba`). Si GitHub está conectado con push automático, los commits se empujan cada 30 s. Ver [ADR-007](docs/adr/ADR-007-mcp-y-tokens-de-agente.md).

## Diseño

Ver [`docs/design/README.md`](docs/design/README.md). Las decisiones de arquitectura viven en [`docs/adr/`](docs/adr/).

## Licencia

[AGPL-3.0-or-later](LICENSE).
