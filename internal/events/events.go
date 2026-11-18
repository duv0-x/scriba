// Package events es el bus en memoria de cambios del vault y el tracker de
// presencia de agentes que se alimenta de él.
package events

import (
	"sort"
	"sync"
	"time"
)

// Kind es el tipo de cambio.
type Kind string

const (
	Changed Kind = "changed"
	Deleted Kind = "deleted"
)

// Event es un cambio en una nota. Agent vacío = cambio en disco; "web" = editor web.
type Event struct {
	Kind  Kind
	Path  string
	Agent string
	At    time.Time
}

// Bus reparte eventos a los suscriptores sin bloquear nunca al publicador.
type Bus struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

func NewBus() *Bus { return &Bus{subs: map[chan Event]struct{}{}} }

// Publish entrega e a cada suscriptor; si el canal de uno está lleno, lo descarta para ese suscriptor.
func (b *Bus) Publish(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

// Subscribe devuelve un canal con buffer buf y la función que lo cancela
// (idempotente; cierra el canal).
func (b *Bus) Subscribe(buf int) (<-chan Event, func()) {
	ch := make(chan Event, buf)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, ch)
			b.mu.Unlock()
			// Publish envía con b.mu tomado, así que tras el delete nadie más escribe en ch.
			close(ch)
		})
	}
}

// Active es un agente con actividad reciente.
type Active struct {
	Agent, Path string
	At          time.Time
}

// Presence recuerda la última nota que escribió cada agente.
type Presence struct {
	mu     sync.Mutex
	window time.Duration
	now    func() time.Time
	last   map[string]Active
}

// NewPresence crea un tracker; now permite un reloj falso en tests (nil = time.Now).
func NewPresence(window time.Duration, now func() time.Time) *Presence {
	if now == nil {
		now = time.Now
	}
	return &Presence{window: window, now: now, last: map[string]Active{}}
}

// Observe registra e si viene de un agente (ignora cambios en disco y de la web).
func (p *Presence) Observe(e Event) {
	if e.Agent == "" || e.Agent == "web" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.last[e.Agent] = Active{Agent: e.Agent, Path: e.Path, At: e.At}
}

// Active devuelve los agentes con actividad dentro de la ventana, más reciente primero.
func (p *Presence) Active() []Active {
	p.mu.Lock()
	defer p.mu.Unlock()
	cutoff := p.now().Add(-p.window)
	var out []Active
	for k, a := range p.last {
		if a.At.Before(cutoff) {
			delete(p.last, k) // fuera de la ventana: no vuelve a contar
			continue
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}

// Writing indica qué agente escribió path dentro de la ventana (el más reciente).
func (p *Presence) Writing(path string) (string, bool) {
	for _, a := range p.Active() {
		if a.Path == path {
			return a.Agent, true
		}
	}
	return "", false
}
