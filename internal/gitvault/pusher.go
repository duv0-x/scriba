package gitvault

import (
	"context"
	"sync"
	"time"
)

// pushTimeout acota cada ciclo pull+push para no colgar Close.
const pushTimeout = 2 * time.Minute

// Pusher agrupa varios Trigger en un solo pull --rebase + push.
type Pusher struct {
	dir      string
	cred     func() Cred
	every    time.Duration
	onResult func(hash string, err error)

	mu     sync.Mutex
	timer  *time.Timer
	closed bool
	op     sync.Mutex // serializa los ciclos si uno dura más que every
	wg     sync.WaitGroup
}

// NewPusher crea un Pusher; cred se consulta en cada ciclo para usar el token vigente.
func NewPusher(dir string, cred func() Cred, every time.Duration, onResult func(hash string, err error)) *Pusher {
	return &Pusher{dir: dir, cred: cred, every: every, onResult: onResult}
}

// Trigger programa un ciclo dentro de every; los Trigger siguientes se agrupan en él.
func (p *Pusher) Trigger() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.timer != nil {
		return
	}
	p.wg.Add(1)
	p.timer = time.AfterFunc(p.every, p.run)
}

func (p *Pusher) run() {
	defer p.wg.Done()
	p.mu.Lock()
	p.timer = nil // un Trigger durante el ciclo programa otro
	p.mu.Unlock()

	p.op.Lock()
	defer p.op.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), pushTimeout)
	defer cancel()
	cred := p.cred()
	err := Pull(ctx, p.dir, cred)
	if err == nil {
		err = Push(ctx, p.dir, cred)
	}
	var hash string
	if err == nil {
		hash, err = Head(p.dir)
	}
	if p.onResult != nil {
		p.onResult(hash, err)
	}
}

// Close descarta el ciclo pendiente y espera el que esté en curso.
func (p *Pusher) Close() {
	p.mu.Lock()
	p.closed = true
	if p.timer != nil && p.timer.Stop() {
		p.wg.Done()
		p.timer = nil
	}
	p.mu.Unlock()
	p.wg.Wait()
}
