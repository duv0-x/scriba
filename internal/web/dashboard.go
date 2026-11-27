package web

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/duv0-x/scriba/internal/gitvault"
	"github.com/duv0-x/scriba/internal/index"
)

// heatWeeks son las columnas del heatmap: 52 semanas completas más la actual.
const heatWeeks = 53

var mesesCortos = [...]string{"Ene", "Feb", "Mar", "Abr", "May", "Jun", "Jul", "Ago", "Sep", "Oct", "Nov", "Dic"}

type tile struct {
	Label, Value, Unit, Hint string
	Pill                     bool // el hint va como chip
}

type heatCell struct {
	Title  string
	Level  int
	Future bool // día posterior a hoy: celda invisible
}

type feedItem struct {
	Who   string
	Agent bool
	Verb  string // "creó", "actualizó" o el asunto truncado
	Title string
	Href  string // "" = la nota ya no está en el índice
	Ago   string
	At    time.Time
}

type storageSeg struct {
	Key, Name, Size string // Key: color del segmento ("0".."3", "o" = otros)
	Pct             float64
}

type dashData struct {
	Aviso   string
	Tiles   []tile
	Heat    []heatCell
	Months  []string // una etiqueta (o "") por columna
	Total   string
	Feed    []feedItem
	Storage []storageSeg
	Links   string // "resueltos / total"; "" = sin wikilinks
}

func (s *kbServer) dashboard(w http.ResponseWriter, r *http.Request) {
	sh, err := s.shellFor(r, "")
	if err != nil {
		serverError(w, err)
		return
	}
	d, err := s.dashData(r.Context(), time.Now())
	if err != nil {
		serverError(w, err)
		return
	}
	if r.URL.Query().Get("aviso") == "historial" {
		d.Aviso = avisoHistorial
	}
	page(w, r, http.StatusOK, layout(sh, "Scriba", dashboardPage(d)))
}

func (s *kbServer) dashData(ctx context.Context, now time.Time) (dashData, error) {
	var d dashData
	root := s.V.Root
	refs, err := s.Ix.Refs()
	if err != nil {
		return d, err
	}
	indexed, err := s.Ix.NoteCount()
	if err != nil {
		return d, err
	}
	ds, err := s.diskStats()
	if err != nil {
		return d, err
	}
	titles := make(map[string]string, len(refs))
	for _, r := range refs {
		titles[r.Path] = r.Title
	}

	today := day(now)
	weekAgo := today.AddDate(0, 0, -6)
	start := today.AddDate(0, 0, -int(today.Weekday())-7*(heatWeeks-1))
	gs, err := gitStats(ctx, root, weekAgo, start)
	if err != nil {
		return d, err
	}
	week := map[string]bool{}
	for _, c := range gs.week {
		for _, f := range c.Files {
			if strings.HasSuffix(f, ".md") {
				week[f] = true
			}
		}
	}
	for _, r := range refs {
		if t, ok := noteDay(r.Date); ok && !t.Before(weekAgo) && !t.After(today) {
			week[r.Path] = true
		}
	}

	d.Tiles = []tile{
		{Label: "Notas", Value: miles(indexed), Hint: fmt.Sprintf("+%s esta semana", miles(len(week))), Pill: true},
		wordsTile(ds.words),
		{Label: "Indexadas", Value: miles(indexed), Unit: "/ " + miles(ds.notes), Hint: "FTS5"},
		bytesTile(ds.total),
	}

	heatmap(&d, gs.days, refs, start, today)

	for _, c := range gs.feed {
		it := feedItem{Who: c.Author, Agent: c.Agent, Verb: verbOf(c.Subject), At: c.At, Ago: hace(c.At, now)}
		for _, f := range c.Files {
			if strings.HasSuffix(f, ".md") {
				it.Title = f
				if t, ok := titles[f]; ok {
					it.Title, it.Href = t, string(noteHref(ctx, f))
				}
				break
			}
		}
		d.Feed = append(d.Feed, it)
	}

	d.Storage = ds.storage
	if ds.links > 0 {
		d.Links = miles(ds.resolved) + " / " + miles(ds.links)
	}
	return d, nil
}

// heatmap llena d.Heat columna a columna (domingo a sábado), desde start hasta la semana de today.
// Cada día cuenta sus commits; si no tuvo ninguno, las notas fechadas ese día (así se ve el
// historial importado sin commits sin contar dos veces lo que sí tiene commit).
func heatmap(d *dashData, commits map[string]int, refs []index.NoteRef, start, today time.Time) {
	dated := map[string]int{}
	for _, r := range refs {
		if t, ok := noteDay(r.Date); ok {
			dated[t.Format(time.DateOnly)]++
		}
	}
	total := 0
	d.Months = make([]string, heatWeeks)
	for i := range heatWeeks * 7 {
		t := start.AddDate(0, 0, i)
		if i%7 == 0 {
			col := i / 7
			prev := t.AddDate(0, 0, -7)
			// La primera columna se etiqueta solo si el mes no cambia enseguida (evita solapes).
			if (col > 0 && t.Month() != prev.Month()) || (col == 0 && t.Month() == t.AddDate(0, 0, 14).Month()) {
				d.Months[col] = mesesCortos[t.Month()-1]
			}
		}
		if t.After(today) {
			d.Heat = append(d.Heat, heatCell{Future: true})
			continue
		}
		key := t.Format(time.DateOnly)
		n := commits[key]
		if n == 0 {
			n = dated[key]
		}
		total += n
		d.Heat = append(d.Heat, heatCell{Title: fmt.Sprintf("%s · %d", key, n), Level: heatLevel(n)})
	}
	d.Total = miles(total) + " contribuciones"
	if total == 1 {
		d.Total = "1 contribución"
	}
}

type gitStat struct {
	week, feed []gitvault.Commit // commits desde weekAgo; los 12 últimos
	days       map[string]int    // commits por día desde el inicio del heatmap
}

// shortcut: caché global como diskCache; se invalida al cambiar HEAD o el día (1 proceso git por petición).
var gitCache struct {
	sync.Mutex
	root, head string
	weekAgo    time.Time
	st         gitStat
}

// gitStats lee el historial que usa el dashboard; en caliente solo corre git rev-parse HEAD.
func gitStats(ctx context.Context, root string, weekAgo, start time.Time) (gitStat, error) {
	head, _ := gitvault.Head(root) // sin repo o sin commits: "" (Log y DayCounts devuelven vacío)
	gitCache.Lock()
	defer gitCache.Unlock()
	if gitCache.root == root && gitCache.head == head && gitCache.weekAgo.Equal(weekAgo) {
		return gitCache.st, nil
	}
	var st gitStat
	var err error
	if st.week, err = gitvault.Log(ctx, root, weekAgo, 0); err != nil {
		return st, err
	}
	if st.feed, err = gitvault.Log(ctx, root, time.Time{}, 12); err != nil {
		return st, err
	}
	if st.days, err = gitvault.DayCounts(ctx, root, start); err != nil {
		return st, err
	}
	gitCache.root, gitCache.head, gitCache.weekAgo, gitCache.st = root, head, weekAgo, st
	return st, nil
}

func heatLevel(n int) int {
	switch {
	case n == 0:
		return 0
	case n <= 2:
		return 1
	case n <= 5:
		return 2
	case n <= 9:
		return 3
	}
	return 4
}

type diskStat struct {
	total, words, notes, links, resolved int64
	storage                              []storageSeg
}

// shortcut: caché global por raíz del vault (30 s); si un Server necesita la suya, moverla al struct.
var diskCache struct {
	sync.Mutex
	root string
	at   time.Time
	st   diskStat
}

// diskStats recorre el vault (sin .git) una vez cada 30 s: tamaños, palabras y wikilinks.
func (s *kbServer) diskStats() (diskStat, error) {
	diskCache.Lock()
	defer diskCache.Unlock()
	if diskCache.root == s.V.Root && time.Since(diskCache.at) < 30*time.Second {
		return diskCache.st, nil
	}
	var st diskStat
	byDir := map[string]int64{}
	err := filepath.WalkDir(s.V.Root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			if e.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(s.V.Root, p)
		top, _, nested := strings.Cut(filepath.ToSlash(rel), "/")
		if !nested {
			top = "" // archivos sueltos en la raíz van a "otros"
		}
		byDir[top] += info.Size()
		st.total += info.Size()
		return nil
	})
	if err != nil {
		return st, err
	}
	err = s.V.Walk(func(rel string) error {
		st.notes++
		if n, err := s.V.Read(rel); err == nil {
			st.words += int64(n.Words)
		}
		links, err := s.Ix.Outlinks(rel)
		if err != nil {
			return err
		}
		for _, l := range links {
			st.links++
			if l.Exists {
				st.resolved++
			}
		}
		return nil
	})
	if err != nil {
		return st, err
	}
	st.storage = storage(byDir, st.total)
	diskCache.root, diskCache.at, diskCache.st = s.V.Root, time.Now(), st
	return st, nil
}

// storage: las 4 carpetas raíz más grandes y el resto como "otros".
func storage(byDir map[string]int64, total int64) []storageSeg {
	if total == 0 {
		return nil
	}
	names := make([]string, 0, len(byDir))
	for k := range byDir {
		if k != "" {
			names = append(names, k)
		}
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := byDir[names[i]], byDir[names[j]]
		return a > b || (a == b && names[i] < names[j])
	})
	var out []storageSeg
	rest := byDir[""]
	for i, n := range names {
		if i >= 4 {
			rest += byDir[n]
			continue
		}
		out = append(out, storageSeg{Key: strconv.Itoa(i), Name: n + "/", Size: humanBytes(byDir[n]), Pct: pct(byDir[n], total)})
	}
	if rest > 0 {
		out = append(out, storageSeg{Key: "o", Name: "otros", Size: humanBytes(rest), Pct: pct(rest, total)})
	}
	return out
}

func pct(n, total int64) float64 { return float64(n) * 100 / float64(total) }

func day(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// noteDay interpreta el date del frontmatter (los primeros 10 caracteres, YYYY-MM-DD) en hora local.
func noteDay(s string) (time.Time, bool) {
	if len(s) < 10 {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation(time.DateOnly, s[:10], time.Local)
	return t, err == nil
}

// verbOf deriva el verbo del asunto ("claude: crea journal/x.md" → "creó").
func verbOf(subject string) string {
	s := subject
	if _, after, ok := strings.Cut(s, ": "); ok {
		s = after
	}
	switch {
	case strings.HasPrefix(s, "crea "):
		return "creó"
	case strings.HasPrefix(s, "actualiza "):
		return "actualizó"
	}
	if r := []rune(subject); len(r) > 60 {
		return string(r[:59]) + "…"
	}
	return subject
}

func hace(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "ahora"
	case d < time.Hour:
		return fmt.Sprintf("hace %d min", int(d.Minutes()))
	case day(t).Equal(day(now)):
		return fmt.Sprintf("hace %d h", int(d.Hours()))
	}
	days := int(day(now).Sub(day(t)).Hours()/24 + 0.5)
	if days == 1 {
		return "ayer"
	}
	return fmt.Sprintf("hace %d d", days)
}

// miles formatea con punto como separador de miles (1.284).
func miles[T int | int64](n T) string {
	s := strconv.FormatInt(int64(n), 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// decimal: un decimal con coma (41,2).
func decimal(f float64) string {
	return strings.Replace(strconv.FormatFloat(f, 'f', 1, 64), ".", ",", 1)
}

func wordsTile(w int64) tile {
	t := tile{Label: "Palabras"}
	switch {
	case w < 1000:
		t.Value = miles(w)
	case w < 10000:
		t.Value, t.Unit = decimal(float64(w)/1000), "k"
	default:
		t.Value, t.Unit = miles((w+500)/1000), "k"
	}
	return t
}

func bytesTile(n int64) tile {
	v, u, _ := strings.Cut(humanBytes(n), " ")
	return tile{Label: "Disco", Value: v, Unit: u, Hint: "vault sin .git"}
}

func humanBytes(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return decimal(float64(n)/(1<<10)) + " KB"
	case n < 1<<30:
		return decimal(float64(n)/(1<<20)) + " MB"
	}
	return decimal(float64(n)/(1<<30)) + " GB"
}
