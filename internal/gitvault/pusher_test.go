package gitvault

import (
	"sync"
	"testing"
	"time"
)

// BeforePull corre con Lock tomado y antes del pull: lo que commitea se empuja en el mismo ciclo.
func TestPusherBeforePull(t *testing.T) {
	bare := setup(t)
	dir := clone(t, bare, Cred{})
	var mu sync.Mutex
	results := make(chan error, 1)
	p := NewPusher(dir, func() (Cred, bool) { return Cred{}, true }, 50*time.Millisecond,
		func(hash string, err error) { results <- err })
	p.Lock = &mu
	var hash string
	held := false
	p.BeforePull = func() error {
		held = !mu.TryLock()
		write(t, dir, "web.md", "pendiente\n")
		var err error
		hash, _, err = CommitPaths(dir, "web", "web: actualiza web.md", "web.md")
		return err
	}
	p.Trigger()
	select {
	case err := <-results:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("el Pusher no empujó")
	}
	p.Close()
	if !held {
		t.Fatal("BeforePull corrió sin Lock")
	}
	if got := git(t, dir, "--git-dir="+bare, "log", "-1", "--format=%H", "main"); got != hash {
		t.Fatalf("bare main=%s, esperado %s", got, hash)
	}
}
