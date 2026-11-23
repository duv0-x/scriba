# Scriba

Scriba es una base de conocimiento en Markdown que escriben tus agentes de IA y lees tú. Mantiene un índice propio para buscar rápido, expone un servidor MCP para que los agentes lean y escriban notas, y ofrece un lector web cuidado para las personas. El Markdown plano, con frontmatter YAML y versionado en git, es la fuente de verdad: el índice se puede borrar y reconstruir en cualquier momento.

## Estado

Primera release: `v0.1.0`. El MVP está completo; ver [releases](https://github.com/duv0-x/scriba/releases).

## Cómo correrlo

```sh
go run ./cmd/scriba serve --vault <dir>
```

Si `<dir>` no existe o está vacío, Scriba abre la bienvenida: crear una base nueva, importarla desde GitHub o subir un `.zip` (Obsidian o carpetas locales). Junto al vault guarda `scriba.db` (índice reconstruible), `state.db` (conectores) y `secret.key` (llave de cifrado, `0600`).

## Instalar con systemd

Cada release (`vX.Y.Z`) publica binarios para linux/amd64, linux/arm64 y darwin/arm64 con sus checksums. El `tar.gz` trae también `deploy/` y los avisos de licencia de las dependencias (`third_party_licenses/`). Scriba necesita `git` instalado.

```bash
sudo useradd --system --home /scriba --shell /usr/sbin/nologin scriba
sudo install -d -m 0700 -o scriba -g scriba /scriba/data /scriba/data/tmp
sudo install -m 0600 deploy/scriba.env.example /scriba/scriba.env   # edita la URL pública
sudo install -m 0755 deploy/scriba-update /usr/local/bin/scriba-update
sudo install -m 0644 deploy/scriba.service deploy/scriba-update.service deploy/scriba-update.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo /usr/local/bin/scriba-update                                   # instala el binario
sudo systemctl enable --now scriba scriba-update.timer
```

`scriba-update` instala la última release cada 5 minutos, con verificación del checksum, respaldo del binario y de `state.db`, y rollback si `/readyz` no responde. Una versión que no queda lista se anota en `/scriba/.failed` y no se reintenta hasta que cambie la versión. Para fijar la versión estilo GitOps, define `SCRIBA_VERSION_URL` en `/scriba/scriba-update.env`, apuntando a un archivo con el tag (p. ej. el `scriba.version` del repo de tu instancia). Si cambias el puerto de `SCRIBA_LISTEN`, pon también `SCRIBA_READY_URL=http://127.0.0.1:<puerto>/readyz` en ese archivo: el updater espera `/readyz` en 8090. Ver [ADR-008](docs/adr/ADR-008-release-y-despliegue.md).

Pon la web detrás de un proxy con autenticación (Scriba usa Cloudflare Access) y deja `/mcp` fuera de ella: los agentes se autentican con su token.

## Conectar GitHub

Scriba se conecta a GitHub desde la pantalla **Conectores** con el device flow de OAuth: muestra un código, lo introduces en `github.com/login/device` y listo. Nunca se configuran credenciales en variables de entorno.

El binario oficial ya trae el Client ID de la OAuth App "Scriba". Si prefieres que tu instancia use su propia app:

1. En GitHub → *Settings* → *Developer settings* → *OAuth Apps* → *New OAuth App*.
2. Nombre `Scriba`; *Homepage URL* y *Authorization callback URL*: la URL de tu instancia.
3. Marca **Enable Device Flow** y desmarca **Expire user access tokens** (Scriba aún no renueva tokens). No necesitas el client secret.
4. Pasa su *Client ID* (es público) con `--github-client-id`.

El token se guarda cifrado en la instancia (ver [ADR-006](docs/adr/ADR-006-conector-github-y-secretos.md)).

## Conectar un agente

Los agentes leen y escriben por MCP en `/mcp`. Cada agente usa su propio token:

1. En **Agentes** crea uno con un nombre (`claude-code-mac`, `opencode-pi`…). El token se muestra una sola vez; también puedes usar `scriba token create <nombre> --data <dir>`.
2. Regístralo en tu cliente. En Claude Code:

   ```sh
   claude mcp add --transport http scriba https://<tu-instancia>/mcp --header "Authorization: Bearer scb_…"
   ```

Mientras un agente escribe, la web lo muestra en vivo: el badge "<agente> editando <nota>", un punto en Folios y, si tienes la nota abierta, los bloques nuevos aparecen con efecto de escritura. Al abrir una nota ves resaltado lo que cambió desde tu última lectura (por persona: el correo de Cloudflare Access, o `default` en local). Las notas también se pueden editar desde la web; si un agente la cambió mientras editabas, Scriba no sobrescribe y te muestra el conflicto.

Cada escritura queda como un commit con el agente como autor (`claude-code-mac via Scriba`). Si GitHub está conectado con push automático, los commits se empujan cada 30 s. Ver [ADR-007](docs/adr/ADR-007-mcp-y-tokens-de-agente.md).

## Plugin de Claude Code

El plugin `scriba` (en [`plugin/`](plugin/)) registra el MCP, añade dos skills (`scriba-contexto` para buscar antes de trabajar y `scriba-documentar` para escribir notas y el journal) y un hook `SessionStart` que le recuerda al agente usarlas.

1. Crea un token en **Agentes** y exporta las dos variables en el shell donde corre Claude Code (por ejemplo, leyéndolas de tu gestor de secretos; nunca en el repo):

   ```sh
   export SCRIBA_URL=https://<tu-instancia>
   export SCRIBA_TOKEN=scb_…
   ```

2. Instala el plugin desde Claude Code:

   ```
   /plugin marketplace add duv0-x/scriba
   /plugin install scriba@scriba
   ```

Si faltan las variables, el hook lo avisa al iniciar la sesión. Sin plugin, basta con el `claude mcp add` que muestra la página **Agentes** (ver [Conectar un agente](#conectar-un-agente)), aunque entonces no tienes las skills ni el recordatorio.

## Diseño

Ver [`docs/design/README.md`](docs/design/README.md). Las decisiones de arquitectura viven en [`docs/adr/`](docs/adr/).

## Licencia

[AGPL-3.0-or-later](LICENSE).
