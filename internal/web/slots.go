package web

import (
	"context"
	"sync"

	"github.com/krilinxito/studyhomie/internal/provider"
)

// slots bounds how many flows talk to the AI at once (max_parallel), so a chat and a few
// practices do not start as many Claude CLI processes together. The limit is read on every
// acquire, so a change in Ajustes applies to the next call.
type slots struct {
	mu      sync.Mutex
	used    int
	waiters []chan struct{}
}

// acquire takes a slot, calling onWait first if it has to queue for one.
func (sl *slots) acquire(ctx context.Context, limit int, onWait func()) error {
	limit = max(1, limit)
	sl.mu.Lock()
	if sl.used < limit && len(sl.waiters) == 0 {
		sl.used++
		sl.mu.Unlock()
		return nil
	}
	ch := make(chan struct{})
	sl.waiters = append(sl.waiters, ch)
	sl.mu.Unlock()
	if onWait != nil {
		onWait()
	}
	select {
	case <-ch:
		return nil // the slot was handed over by release
	case <-ctx.Done():
		sl.mu.Lock()
		for i, w := range sl.waiters {
			if w == ch {
				sl.waiters = append(sl.waiters[:i], sl.waiters[i+1:]...)
				sl.mu.Unlock()
				return ctx.Err()
			}
		}
		sl.mu.Unlock()
		sl.release(limit) // handed over just as the wait ended: give it back
		return ctx.Err()
	}
}

// release frees a slot, handing it to the first in line while the limit allows.
func (sl *slots) release(limit int) {
	limit = max(1, limit)
	sl.mu.Lock()
	defer sl.mu.Unlock()
	if len(sl.waiters) > 0 && sl.used <= limit {
		ch := sl.waiters[0]
		sl.waiters = sl.waiters[1:]
		close(ch)
		return
	}
	sl.used--
}

// limited is the provider one flow uses: while any of its calls is in flight it holds one slot,
// so a guide that writes several concept notes at the same time still counts as one flow.
type limited struct {
	provider.Provider
	sl     *slots
	limit  func() int
	onWait func() // the flow has to queue
	onGo   func() // its turn came after queuing

	mu sync.Mutex
	n  int // calls in flight
}

func (l *limited) enter(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.n == 0 {
		waited := false
		err := l.sl.acquire(ctx, l.limit(), func() {
			waited = true
			if l.onWait != nil {
				l.onWait()
			}
		})
		if err != nil {
			return err
		}
		if waited && l.onGo != nil {
			l.onGo()
		}
	}
	l.n++
	return nil
}

func (l *limited) leave() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.n--
	if l.n == 0 {
		l.sl.release(l.limit())
	}
}

func (l *limited) Complete(ctx context.Context, req provider.Request, onText func(string)) (provider.Response, error) {
	if err := l.enter(ctx); err != nil {
		return provider.Response{}, err
	}
	defer l.leave()
	return l.Provider.Complete(ctx, req, onText)
}

// limit wraps a flow's provider so it waits for a free slot; onWait and onGo may be nil.
func (s *Server) limit(p provider.Provider, onWait, onGo func()) provider.Provider {
	return &limited{Provider: p, sl: &s.slots, onWait: onWait, onGo: onGo, limit: func() int {
		_, cfg, _ := s.backend()
		return maxParallel(cfg)
	}}
}
