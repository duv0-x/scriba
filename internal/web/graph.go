package web

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/duv0-x/scriba/internal/index"
)

type graphNode struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Type   string `json:"type"`
	Degree int    `json:"degree"`
}

type graphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type graphJSON struct {
	Nodes []graphNode `json:"nodes"`
	Edges []graphEdge `json:"edges"`
}

// graphType es una fila del panel de filtros: tipo (o "otras"), etiqueta y conteo.
type graphType struct {
	Key, Label string
	N          int
}

type graphView struct {
	Types          []graphType
	Notes, Links   int
	Orphans, Total int
}

// graphTypes son los tipos con color propio, en el orden del panel; el resto va a "otras".
var graphTypes = []graphType{
	{Key: "journal", Label: "Journal"}, {Key: "tickets", Label: "Tickets"}, {Key: "decisions", Label: "Decisiones"},
	{Key: "runbooks", Label: "Runbooks"}, {Key: "references", Label: "Referencias"},
	{Key: "postmortems", Label: "Postmortems"}, {Key: "otras", Label: "Otras"},
}

// graphRoutes registra el grafo de cada KB.
func (s *Server) graphRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /k/{kb}/grafo", s.kb((*kbServer).graphPage))
	mux.HandleFunc("GET /k/{kb}/api/grafo", s.kb((*kbServer).graphAPI))
}

func (s *kbServer) graphAPI(w http.ResponseWriter, r *http.Request) {
	g, err := s.graph(false)
	if err != nil {
		serverError(w, err)
		return
	}
	if c := r.URL.Query().Get("centro"); c != "" {
		// Solo profundidad 1: la nota y sus vecinos directos (entrantes y salientes).
		n, ok := neighborhood(g, c)
		if !ok { // quizá la creó un agente después de cachear: se reconstruye una vez
			if g, err = s.graph(true); err != nil {
				serverError(w, err)
				return
			}
			n, ok = neighborhood(g, c)
		}
		if !ok {
			http.Error(w, "la nota no está en el índice", http.StatusNotFound)
			return
		}
		g = n
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(g)
}

func (s *kbServer) graphPage(w http.ResponseWriter, r *http.Request) {
	sh, err := s.shellFor(r, "")
	if err != nil {
		serverError(w, err)
		return
	}
	sh.View = "grafo"
	g, err := s.graph(false)
	if err != nil {
		serverError(w, err)
		return
	}
	count := map[string]int{}
	v := graphView{Links: len(g.Edges), Total: len(g.Nodes)}
	for _, n := range g.Nodes {
		count[graphGroup(n.Type)]++
		if n.Degree == 0 {
			v.Orphans++
		}
	}
	v.Notes = v.Total - v.Orphans // las huérfanas arrancan ocultas
	for _, t := range graphTypes {
		if t.N = count[t.Key]; t.N > 0 {
			v.Types = append(v.Types, t)
		}
	}
	page(w, r, http.StatusOK, layout(sh, "Grafo · Scriba", graphPageView(v)))
}

func graphGroup(t string) string {
	for _, g := range graphTypes {
		if g.Key == t {
			return t
		}
	}
	return "otras"
}

// shortcut: caché global por índice (30 s) como diskCache; los enlaces nuevos tardan hasta 30 s en verse
// (una nota nueva pedida como centro fuerza la reconstrucción). Invalidar desde el bus si molesta.
var graphCache struct {
	sync.Mutex
	ix *index.Index
	at time.Time
	g  graphJSON
}

// graph arma nodos y aristas resueltas del índice, sin duplicados (a→b y b→a son una) ni bucles.
func (s *kbServer) graph(fresh bool) (graphJSON, error) {
	graphCache.Lock()
	defer graphCache.Unlock()
	if !fresh && graphCache.ix == s.Ix && time.Since(graphCache.at) < 30*time.Second {
		return graphCache.g, nil
	}
	refs, err := s.Ix.Refs()
	if err != nil {
		return graphJSON{}, err
	}
	g := graphJSON{Nodes: make([]graphNode, len(refs)), Edges: []graphEdge{}}
	pos := make(map[string]int, len(refs))
	for i, r := range refs {
		g.Nodes[i] = graphNode{ID: r.Path, Title: r.Title, Type: r.Type}
		pos[r.Path] = i
	}
	links, err := s.Ix.Edges()
	if err != nil {
		return graphJSON{}, err
	}
	seen := map[[2]string]bool{}
	for _, l := range links {
		src, dst := l[0], l[1]
		i, ok1 := pos[src]
		j, ok2 := pos[dst]
		if !ok1 || !ok2 || src == dst {
			continue
		}
		key := [2]string{min(src, dst), max(src, dst)}
		if seen[key] {
			continue
		}
		seen[key] = true
		g.Edges = append(g.Edges, graphEdge{Source: src, Target: dst})
		g.Nodes[i].Degree++
		g.Nodes[j].Degree++
	}
	graphCache.ix, graphCache.at, graphCache.g = s.Ix, time.Now(), g
	return g, nil
}

// neighborhood deja c, sus vecinos directos y las aristas entre ellos; false si c no existe.
func neighborhood(g graphJSON, c string) (graphJSON, bool) {
	keep := map[string]bool{}
	for _, n := range g.Nodes {
		if n.ID == c {
			keep[c] = true
		}
	}
	if !keep[c] {
		return graphJSON{}, false
	}
	for _, e := range g.Edges {
		if e.Source == c {
			keep[e.Target] = true
		} else if e.Target == c {
			keep[e.Source] = true
		}
	}
	out := graphJSON{Nodes: []graphNode{}, Edges: []graphEdge{}}
	for _, n := range g.Nodes {
		if keep[n.ID] {
			out.Nodes = append(out.Nodes, n)
		}
	}
	for _, e := range g.Edges {
		if keep[e.Source] && keep[e.Target] {
			out.Edges = append(out.Edges, e)
		}
	}
	return out, true
}
