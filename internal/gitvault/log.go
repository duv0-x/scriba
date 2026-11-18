package gitvault

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// agentSuffix es lo que CommitAll añade al nombre del agente.
const agentSuffix = " via Scriba"

// Commit es un commit del vault. Agent indica que lo hizo Scriba en nombre de
// Author (el sufijo " via Scriba" ya viene quitado).
type Commit struct {
	Hash, Author, Subject string
	Agent                 bool
	At                    time.Time
	Files                 []string
}

// hasCommits indica si dir es un repo con al menos un commit.
func hasCommits(ctx context.Context, dir string) bool {
	if !IsRepo(dir) {
		return false
	}
	_, err := run(ctx, dir, Cred{}, "rev-parse", "--verify", "-q", "HEAD")
	return err == nil
}

func sinceArgs(since time.Time) []string {
	if since.IsZero() {
		return nil
	}
	return []string{"--since=" + since.Format(time.RFC3339)}
}

// Log devuelve los commits desde since (cero = todos), más reciente primero,
// como mucho limit (<= 0 = sin límite). Sin repo o sin commits devuelve vacío.
func Log(ctx context.Context, dir string, since time.Time, limit int) ([]Commit, error) {
	if !hasCommits(ctx, dir) {
		return nil, nil
	}
	// Cada commit empieza con un NUL (token vacío al partir), luego 4 campos;
	// los archivos van después, NUL-separados, el primero precedido de "\n".
	args := []string{"log", "-z", "--name-only", "--no-renames", "--format=%x00%H%x00%an%x00%at%x00%s"}
	if limit > 0 {
		args = append(args, "-n", strconv.Itoa(limit))
	}
	out, err := run(ctx, dir, Cred{}, append(args, sinceArgs(since)...)...)
	if err != nil {
		return nil, err
	}
	toks := strings.Split(out, "\x00")
	var commits []Commit
	for i := 0; i < len(toks); i++ {
		if toks[i] != "" {
			f := strings.TrimPrefix(toks[i], "\n")
			if len(commits) == 0 {
				return nil, fmt.Errorf("gitvault: log: salida inesperada %q", f)
			}
			if f != "" {
				c := &commits[len(commits)-1]
				c.Files = append(c.Files, f)
			}
			continue
		}
		if i+4 >= len(toks) {
			break // NUL final sin cabecera detrás
		}
		ts, err := strconv.ParseInt(toks[i+3], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("gitvault: log: fecha %q: %w", toks[i+3], err)
		}
		author, agent := strings.CutSuffix(toks[i+2], agentSuffix)
		commits = append(commits, Commit{Hash: toks[i+1], Author: author, Agent: agent, At: time.Unix(ts, 0), Subject: toks[i+4]})
		i += 4
	}
	return commits, nil
}

// DayCounts cuenta commits por día ("YYYY-MM-DD", hora local) desde since.
func DayCounts(ctx context.Context, dir string, since time.Time) (map[string]int, error) {
	m := map[string]int{}
	if !hasCommits(ctx, dir) {
		return m, nil
	}
	out, err := run(ctx, dir, Cred{}, append([]string{"log", "--format=%at"}, sinceArgs(since)...)...)
	if err != nil {
		return nil, err
	}
	for _, l := range strings.Fields(out) {
		ts, err := strconv.ParseInt(l, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("gitvault: log: fecha %q: %w", l, err)
		}
		m[time.Unix(ts, 0).Format(time.DateOnly)]++
	}
	return m, nil
}
