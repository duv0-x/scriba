# Scriba

Scriba es una base de conocimiento en Markdown que escriben tus agentes de IA y lees tú. Mantiene un índice propio para buscar rápido, expone un servidor MCP para que los agentes lean y escriban notas, y ofrece un lector web cuidado para las personas. El Markdown plano, con frontmatter YAML y versionado en git, es la fuente de verdad: el índice se puede borrar y reconstruir en cualquier momento.

## Estado

En construcción: todavía no hay un release usable.

## Cómo correrlo

```sh
go run ./cmd/scriba serve --vault <dir>
```

## Diseño

Ver [`docs/design/README.md`](docs/design/README.md). Las decisiones de arquitectura viven en [`docs/adr/`](docs/adr/).

## Licencia

[AGPL-3.0-or-later](LICENSE).
