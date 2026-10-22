package ws

import (
	"math"
	"sync"
	"time"
)

// tokenBucket is a small, dependency free message rate limiter. It allows a
// short burst twice the sustained rate so heartbeats and endpoint updates that
// happen to coincide are never penalized.
type tokenBucket struct {
	mu     sync.Mutex
	tokens float64
	burst  float64
	rate   float64
	last   time.Time
	now    func() time.Time
}

func newTokenBucket(messagesPerSecond int, now func() time.Time) *tokenBucket {
	if now == nil {
		now = time.Now
	}
	rate := float64(messagesPerSecond)
	burst := math.Max(4, rate*2)
	return &tokenBucket{
		tokens: burst,
		burst:  burst,
		rate:   rate,
		last:   now(),
		now:    now,
	}
}

// Allow consumes one token and reports whether the message may be processed.
func (b *tokenBucket) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = math.Min(b.burst, b.tokens+elapsed*b.rate)
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
