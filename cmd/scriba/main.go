// Command scriba sirve una base de conocimiento en Markdown a personas (web) y agentes (MCP).
package main

import (
	"fmt"
	"os"
)

// version la sobrescribe el build con -ldflags "-X main.version=...".
var version = "dev"

const usage = `uso: scriba <comando> [flags]

comandos:
  serve    sirve el vault (--vault <dir> obligatorio)
  token    crea, lista o revoca tokens de agente (create|list|revoke)
  version  imprime la versión
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Println(version)
		return 0
	case "serve":
		return serve(args[1:])
	case "token":
		return token(args[1:])
	default:
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
}
