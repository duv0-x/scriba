package web

import (
	"cmp"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/duv0-x/scriba/internal/index"
	"github.com/duv0-x/scriba/internal/vault"
)

// treeNode es una carpeta (Dir) o una nota del árbol de la sidebar.
type treeNode struct {
	Dir      bool
	Label    string // nombre de la carpeta o título de la nota
	Path     string // ruta de la nota, o de la carpeta (vacía en los grupos de fecha)
	N        int    // notas dentro de la carpeta, recursivo
	Open     bool   // la nota actual está dentro
	Current  bool
	Unread   bool // cambió desde la última lectura del usuario (PR3b)
	Writing  bool // un agente la está escribiendo (PR3b)
	Children []treeNode
}

// buildTree arma el árbol desde refs (ordenadas por path) y despliega la ruta de current.
func buildTree(refs []index.NoteRef, current string) []treeNode {
	return buildDir(refs, "", current)
}

// buildDir arma el contenido de la carpeta prefix ("" = raíz): subcarpetas y luego notas.
func buildDir(refs []index.NoteRef, prefix, current string) []treeNode {
	sub := map[string][]index.NoteRef{}
	var dirs []string
	var notes []treeNode
	for _, r := range refs {
		rest := strings.TrimPrefix(r.Path, prefix)
		if d, _, ok := strings.Cut(rest, "/"); ok {
			if _, seen := sub[d]; !seen {
				dirs = append(dirs, d)
			}
			sub[d] = append(sub[d], r)
			continue
		}
		notes = append(notes, treeNode{Label: datedLabel(r.Path, r.Title), Path: r.Path, Current: r.Path == current})
	}
	slices.SortFunc(dirs, func(a, b string) int {
		if prefix == "" {
			ia, ib := slices.Index(vault.KnownTypes, a), slices.Index(vault.KnownTypes, b)
			if ia >= 0 || ib >= 0 {
				// Las carpetas conocidas van primero, en el orden de KnownTypes.
				if ia < 0 {
					return 1
				}
				if ib < 0 {
					return -1
				}
				return ia - ib
			}
		}
		return strings.Compare(a, b)
	})
	out := make([]treeNode, 0, len(dirs)+len(notes))
	for _, d := range dirs {
		p := prefix + d
		var kids []treeNode
		if p == "journal" {
			kids = journalGroups(sub[d], current)
		} else {
			kids = buildDir(sub[d], p+"/", current)
		}
		out = append(out, dirNode(d, p, len(sub[d]), kids))
	}
	return append(out, notes...)
}

func dirNode(label, p string, n int, kids []treeNode) treeNode {
	open := slices.ContainsFunc(kids, func(k treeNode) bool { return k.Open || k.Current })
	return treeNode{Dir: true, Label: label, Path: p, N: n, Open: open, Children: kids}
}

var (
	dateRe = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})`)
	meses  = []string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto",
		"septiembre", "octubre", "noviembre", "diciembre"}
)

// datedLabel pone la fecha del nombre del archivo al inicio del nombre visible, para que el orden
// se lea de un vistazo; si el título ya empieza por esa fecha, lo deja igual.
func datedLabel(p, title string) string {
	d := dateRe.FindString(path.Base(p))
	if d == "" || strings.HasPrefix(title, d) {
		return title
	}
	return d + " · " + title
}

// journalGroups agrupa el journal por año y mes (más reciente primero); sin fecha va al final.
func journalGroups(refs []index.NoteRef, current string) []treeNode {
	type dated struct {
		date string // YYYY-MM-DD, o "" si no se pudo deducir
		ref  index.NoteRef
	}
	ds := make([]dated, 0, len(refs))
	for _, r := range refs {
		m := dateRe.FindString(path.Base(r.Path))
		if m == "" {
			m = dateRe.FindString(r.Date)
		}
		if m != "" {
			if mo, _ := strconv.Atoi(m[5:7]); mo < 1 || mo > 12 {
				m = ""
			}
		}
		ds = append(ds, dated{m, r})
	}
	slices.SortStableFunc(ds, func(a, b dated) int {
		return cmp.Or(strings.Compare(b.date, a.date), strings.Compare(b.ref.Path, a.ref.Path))
	})

	var out []treeNode
	var undated []treeNode
	for i := 0; i < len(ds); {
		if ds[i].date == "" {
			for _, d := range ds[i:] {
				undated = append(undated, treeNode{Label: d.ref.Title, Path: d.ref.Path, Current: d.ref.Path == current})
			}
			break
		}
		year := ds[i].date[:4]
		var months []treeNode
		yn := 0
		for i < len(ds) && ds[i].date != "" && ds[i].date[:4] == year {
			month := ds[i].date[:7]
			var notes []treeNode
			for i < len(ds) && ds[i].date != "" && ds[i].date[:7] == month {
				d := ds[i]
				label := d.ref.Title
				if label != d.date {
					label = strings.TrimLeft(d.date[8:], "0") + " · " + label
				}
				notes = append(notes, treeNode{Label: label, Path: d.ref.Path, Current: d.ref.Path == current})
				i++
			}
			mo, _ := strconv.Atoi(month[5:])
			months = append(months, dirNode(meses[mo-1], "", len(notes), notes))
			yn += len(notes)
		}
		out = append(out, dirNode(year, "", yn, months))
	}
	if len(undated) > 0 {
		out = append(out, dirNode("Sin fecha", "", len(undated), undated))
	}
	return out
}
