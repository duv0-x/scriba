package events

import (
	"sync"
	"time"
)

// Publisher une los avisos del writer (con agente) y del watcher (disco) de una misma
// escritura: cada cambio de un agente lo ve también el watcher, y ese segundo aviso haría
// que el lector pidiera de nuevo la nota y borrara el resaltado. Gana siempre el del
// agente, que trae la presencia, llegue antes o después que el del disco.
type Publisher struct {
	bus    *Bus
	pres   *Presence
	window time.Duration
	now    func() time.Time

	mu      sync.Mutex
	agentAt map[string]time.Time // última escritura de un agente por ruta
	held    map[string]*hold     // avisos de disco retenidos esperando al del agente
	closed  bool
}

type hold struct{ t *time.Timer }

// NewPublisher crea un Publisher; now permite un reloj falso en tests (nil = time.Now).
func NewPublisher(bus *Bus, pres *Presence, window time.Duration, now func() time.Time) *Publisher {
	if now == nil {
		now = time.Now
	}
	return &Publisher{bus: bus, pres: pres, window: window, now: now, agentAt: map[string]time.Time{}, held: map[string]*hold{}}
}

// Agent publica el cambio de un agente (siempre) y descarta el aviso de disco retenido de la misma
// ruta. hash es el SHA-256 hex del contenido escrito; los eventos de disco y de release van sin hash.
func (p *Publisher) Agent(path, agent, hash string) {
	now := p.now()
	p.mu.Lock()
	if h := p.held[path]; h != nil {
		h.t.Stop()
		delete(p.held, path)
	}
	p.agentAt[path] = now
	for k, t := range p.agentAt {
		if now.Sub(t) >= p.window {
			delete(p.agentAt, k)
		}
	}
	p.mu.Unlock()
	e := Event{Kind: Changed, Path: path, Agent: agent, At: now, Hash: hash}
	if p.pres != nil {
		p.pres.Observe(e)
	}
	p.bus.Publish(e)
}

// Disk publica un cambio visto en disco. Un borrado sale de inmediato; un cambio se descarta
// si un agente escribió la ruta hace menos de window y, si no, se retiene window por si llega
// el aviso del agente.
func (p *Publisher) Disk(path string, deleted bool) {
	now := p.now()
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	if deleted {
		if h := p.held[path]; h != nil {
			h.t.Stop()
			delete(p.held, path)
		}
		p.mu.Unlock()
		p.bus.Publish(Event{Kind: Deleted, Path: path, At: now})
		return
	}
	if t, ok := p.agentAt[path]; ok && now.Sub(t) < p.window {
		p.mu.Unlock()
		return
	}
	if p.held[path] == nil {
		h := &hold{}
		h.t = time.AfterFunc(p.window, func() { p.release(path, h) }) // release espera a p.mu, así h.t ya está asignado
		p.held[path] = h
	}
	p.mu.Unlock()
}

func (p *Publisher) release(path string, h *hold) {
	p.mu.Lock()
	if p.closed || p.held[path] != h {
		p.mu.Unlock()
		return
	}
	delete(p.held, path)
	p.mu.Unlock()
	p.bus.Publish(Event{Kind: Changed, Path: path, At: p.now()})
}

// Close cancela los avisos retenidos; después Disk no publica nada.
func (p *Publisher) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	for k, h := range p.held {
		h.t.Stop()
		delete(p.held, k)
	}
}
