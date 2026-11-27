package events

import (
	"testing"
	"time"
)

func TestBusVariosSuscriptores(t *testing.T) {
	b := NewBus()
	a, ca := b.Subscribe(4)
	c, cc := b.Subscribe(4)
	defer ca()
	defer cc()
	b.Publish(Event{Kind: Changed, Path: "x.md", Agent: "claude"})
	for _, ch := range []<-chan Event{a, c} {
		if e := <-ch; e.Path != "x.md" || e.Kind != Changed {
			t.Fatalf("evento = %+v", e)
		}
	}
}

func TestBusLentoNoBloquea(t *testing.T) {
	b := NewBus()
	_, cancel := b.Subscribe(1) // nadie lee
	defer cancel()
	done := make(chan struct{})
	go func() {
		for range 1000 {
			b.Publish(Event{Kind: Changed, Path: "x.md"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish se bloqueó con un suscriptor lento")
	}
}

func TestBusCancel(t *testing.T) {
	b := NewBus()
	ch, cancel := b.Subscribe(1)
	cancel()
	cancel() // idempotente
	if _, ok := <-ch; ok {
		t.Fatal("el canal sigue abierto")
	}
	b.Publish(Event{Kind: Deleted, Path: "x.md"}) // sin panic
}

func TestBusCancelConcurrente(t *testing.T) {
	b := NewBus()
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				b.Publish(Event{Kind: Changed})
			}
		}
	}()
	for range 100 {
		_, cancel := b.Subscribe(1)
		cancel()
	}
	close(stop)
}

func TestPresence(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	now := t0
	p := NewPresence(60*time.Second, func() time.Time { return now })
	p.Observe(Event{Kind: Changed, Path: "a.md", Agent: "claude", At: t0})
	p.Observe(Event{Kind: Changed, Path: "b.md", Agent: "codex", At: t0.Add(10 * time.Second)})
	p.Observe(Event{Kind: Changed, Path: "c.md", Agent: "web", At: t0})
	p.Observe(Event{Kind: Changed, Path: "d.md", At: t0})

	now = t0.Add(30 * time.Second)
	act := p.Active()
	if len(act) != 2 || act[0].Agent != "codex" || act[1].Agent != "claude" {
		t.Fatalf("Active = %+v", act)
	}
	if a, ok := p.Writing("a.md"); !ok || a != "claude" {
		t.Fatalf("Writing(a.md) = %q %v", a, ok)
	}
	for _, path := range []string{"c.md", "d.md"} {
		if a, ok := p.Writing(path); ok {
			t.Fatalf("Writing(%s) = %q, web y vacío se ignoran", path, a)
		}
	}

	now = t0.Add(65 * time.Second) // claude sale de la ventana, codex sigue
	if act := p.Active(); len(act) != 1 || act[0].Agent != "codex" {
		t.Fatalf("Active a los 65 s = %+v", act)
	}
	if _, ok := p.Writing("a.md"); ok {
		t.Fatal("claude debería haber salido de la ventana")
	}
	now = t0.Add(2 * time.Minute)
	if act := p.Active(); len(act) != 0 {
		t.Fatalf("Active a los 2 min = %+v", act)
	}
}

func TestPresenceWindow(t *testing.T) {
	if w := NewPresence(42*time.Second, nil).Window(); w != 42*time.Second {
		t.Fatalf("Window = %v", w)
	}
}
