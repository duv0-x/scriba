package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/duv0-x/scriba/internal/agents"
	"github.com/duv0-x/scriba/internal/state"
)

const tokenUsage = `uso: scriba token create <nombre> [--data <dir>]
       scriba token list [--data <dir>]
       scriba token revoke <nombre> [--data <dir>]
`

func token(args []string) int { return tokenCmd(args, os.Stdout, os.Stderr) }

func tokenCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, tokenUsage)
		return 2
	}
	sub := args[0]
	fs := flag.NewFlagSet("token "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	data := fs.String("data", ".", "directorio de state.db")
	// Se aceptan flags antes y después del nombre: flag se detiene en el primer posicional.
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	var name string
	if fs.NArg() > 0 {
		name = fs.Arg(0)
		if err := fs.Parse(fs.Args()[1:]); err != nil {
			return 2
		}
	}
	needName := sub == "create" || sub == "revoke"
	if (sub != "list" && !needName) || needName != (name != "") || fs.NArg() > 0 {
		fmt.Fprint(stderr, tokenUsage)
		return 2
	}

	// No se crea la base: un --data equivocado dejaría tokens en una state.db que nadie lee.
	dbPath := filepath.Join(*data, "state.db")
	if _, err := os.Stat(dbPath); err != nil {
		fmt.Fprintf(stderr, "error: no hay state.db en %s: pasa --data con el directorio de datos de tu instancia (junto a scriba.db)\n", *data)
		return 1
	}
	st, err := state.Open(dbPath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	defer st.Close()
	s := agents.Store{S: st}

	switch sub {
	case "create":
		tok, err := s.Create(name)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		fmt.Fprintln(stderr, "Guárdalo ahora: no se vuelve a mostrar.")
		fmt.Fprintln(stdout, tok)
	case "revoke":
		ok, err := s.Revoke(name)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		if !ok {
			fmt.Fprintf(stderr, "no hay un token activo llamado %q\n", name)
			return 1
		}
		fmt.Fprintf(stdout, "revocado %s\n", name)
	case "list":
		l, err := s.List()
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NOMBRE\tCREADO\tÚLTIMO USO\tREVOCADO")
		for _, a := range l {
			used := "nunca"
			if !a.LastUsed.IsZero() {
				used = a.LastUsed.Local().Format(time.DateTime)
			}
			revoked := "no"
			if a.Revoked {
				revoked = "sí"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", a.Name, a.Created.Local().Format(time.DateTime), used, revoked)
		}
		w.Flush()
	}
	return 0
}
