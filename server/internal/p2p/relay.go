package p2p

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"
)

// Relay pairs the two sides of a session on a raw TCP connection and copies
// bytes in both directions. Nothing is buffered to disk: the payload is already
// end-to-end encrypted by the clients.
type Relay struct {
	mu      sync.Mutex
	waiting map[string]chan net.Conn
	timeout time.Duration
	logger  *slog.Logger
}

// NewRelay creates a relay manager.
func NewRelay(timeout time.Duration, logger *slog.Logger) *Relay {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Relay{
		waiting: make(map[string]chan net.Conn),
		timeout: timeout,
		logger:  logger,
	}
}

// Bridge hands the connection to the session partner. The first caller blocks
// until the second side arrives (or the timeout fires) and owns the copy loop;
// the second caller returns immediately after publishing its connection.
func (r *Relay) Bridge(id string, conn net.Conn) error {
	r.mu.Lock()
	ch, waiting := r.waiting[id]
	if !waiting {
		ch = make(chan net.Conn, 1)
		r.waiting[id] = ch
		r.mu.Unlock()

		defer func() {
			r.mu.Lock()
			if current, ok := r.waiting[id]; ok && current == ch {
				delete(r.waiting, id)
			}
			r.mu.Unlock()
		}()

		select {
		case peer := <-ch:
			bridge(conn, peer)
			return nil
		case <-time.After(r.timeout):
			return errors.New("relay: peer did not connect in time")
		}
	}

	delete(r.waiting, id)
	r.mu.Unlock()
	ch <- conn
	return nil
}

func bridge(left, right net.Conn) {
	defer left.Close()
	defer right.Close()

	done := make(chan struct{}, 2)
	copyOne := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		if closer, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = closer.CloseWrite()
		}
		done <- struct{}{}
	}

	go copyOne(left, right)
	go copyOne(right, left)
	<-done
	<-done
}
