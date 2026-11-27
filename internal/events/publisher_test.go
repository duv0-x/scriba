package events

import (
	"testing"
	"time"
)

const win = 50 * time.Millisecond

func newPub(t *testing.T) (*Publisher, <-chan Event, *Presence) {
	t.Helper()
	bus := NewBus()
	ch, cancel := bus.Subscribe(16)
	pres := NewPresence(time.Minute, nil)
	p := NewPublisher(bus, pres, win, nil)
	t.Cleanup(func() { p.Close(); cancel() })
	return p, ch, pres
}

// collect devuelve lo publicado hasta pasado de sobra la ventana.
func collect(ch <-chan Event) []Event {
	var out []Event
	deadline := time.After(3 * win)
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		case <-deadline:
			return out
		}
	}
}

func TestPublisherAgenteLuegoDisco(t *testing.T) {
	p, ch, pres := newPub(t)
	p.Agent("a.md", "claude", "h1")
	p.Disk("a.md", false)
	got := collect(ch)
	if len(got) != 1 || got[0].Agent != "claude" || got[0].Kind != Changed {
		t.Fatalf("eventos = %+v", got)
	}
	if got[0].Hash != "h1" {
		t.Fatalf("hash = %q", got[0].Hash)
	}
	if a, ok := pres.Writing("a.md"); !ok || a != "claude" {
		t.Fatalf("presencia = %q %v", a, ok)
	}
}

func TestPublisherDiscoLuegoAgente(t *testing.T) {
	p, ch, _ := newPub(t)
	p.Disk("a.md", false)
	p.Disk("a.md", false) // el watcher puede avisar dos veces
	time.Sleep(win / 5)
	p.Agent("a.md", "claude", "h1")
	got := collect(ch)
	if len(got) != 1 || got[0].Agent != "claude" {
		t.Fatalf("eventos = %+v", got)
	}
}

func TestPublisherSoloDisco(t *testing.T) {
	p, ch, _ := newPub(t)
	start := time.Now()
	p.Disk("a.md", false)
	got := collect(ch)
	if len(got) != 1 || got[0].Agent != "" || got[0].Kind != Changed || got[0].Path != "a.md" {
		t.Fatalf("eventos = %+v", got)
	}
	if d := got[0].At.Sub(start); d < win {
		t.Fatalf("publicado antes de la ventana: %v", d)
	}
}

func TestPublisherBorrado(t *testing.T) {
	p, ch, _ := newPub(t)
	p.Disk("a.md", false)
	p.Disk("a.md", true) // cancela el cambio retenido
	select {
	case e := <-ch:
		if e.Kind != Deleted || e.Path != "a.md" {
			t.Fatalf("evento = %+v", e)
		}
	case <-time.After(win / 2):
		t.Fatal("el borrado no salió de inmediato")
	}
	if got := collect(ch); len(got) != 0 {
		t.Fatalf("eventos de más: %+v", got)
	}
}

func TestPublisherVentanaVencida(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	now := t0
	bus := NewBus()
	ch, cancel := bus.Subscribe(4)
	defer cancel()
	p := NewPublisher(bus, nil, win, func() time.Time { return now })
	defer p.Close()
	p.Agent("a.md", "claude", "h1")
	<-ch
	now = t0.Add(win) // un cambio en disco tras la ventana es de otra persona
	p.Disk("a.md", false)
	if got := collect(ch); len(got) != 1 || got[0].Agent != "" {
		t.Fatalf("eventos = %+v", got)
	}
}

func TestPublisherClose(t *testing.T) {
	p, ch, _ := newPub(t)
	p.Disk("a.md", false)
	p.Close()
	if got := collect(ch); len(got) != 0 {
		t.Fatalf("eventos tras Close: %+v", got)
	}
}
