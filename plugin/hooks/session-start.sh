#!/bin/sh
# SessionStart: lo que se imprime en stdout entra como contexto de la sesión. Nunca imprime el token.
if [ -z "$SCRIBA_URL" ] || [ -z "$SCRIBA_TOKEN" ]; then
	echo "El plugin scriba está instalado pero sin configurar: faltan SCRIBA_URL y/o SCRIBA_TOKEN en el entorno de Claude Code, así que el MCP \`scriba\` no podrá conectarse. Para configurarlo: crea un token en la página /agentes de tu instancia de Scriba y exporta SCRIBA_URL (URL de la instancia, sin /mcp) y SCRIBA_TOKEN en el shell antes de abrir Claude Code."
	exit 0
fi
case "$SCRIBA_URL" in
*/) echo "Aviso: SCRIBA_URL termina en '/', así que el MCP queda en '//mcp' y puede fallar. Quita la barra final." ;;
esac
echo "Esta sesión tiene acceso a la base de conocimiento Scriba (MCP \`scriba\`). Antes de una tarea técnica busca contexto (skill scriba-contexto). Al terminar, documenta (skill scriba-documentar) y escribe el journal del día. Para que el usuario revise un plan, guárdalo en plans/ y ábrelo con kb_open."
