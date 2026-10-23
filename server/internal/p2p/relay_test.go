package p2p

import (
	"io"
	"net"
	"testing"
	"time"
)

func TestRelayPairsConnectionsAndCopiesBothWays(t *testing.T) {
	relay := NewRelay(2*time.Second, nil)

	leftLocal, leftPeer := net.Pipe()
	rightLocal, rightPeer := net.Pipe()
	deadline := time.Now().Add(5 * time.Second)
	for _, conn := range []net.Conn{leftPeer, rightPeer} {
		_ = conn.SetDeadline(deadline)
	}

	done := make(chan error, 2)
	go func() { done <- relay.Bridge("session-1", leftLocal) }()
	go func() { done <- relay.Bridge("session-1", rightLocal) }()

	payload := []byte("hello relay")
	go func() {
		_, _ = leftPeer.Write(payload)
	}()

	got := make([]byte, len(payload))
	if _, err := io.ReadFull(rightPeer, got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("payload = %q, want %q", got, payload)
	}

	go func() {
		_, _ = rightPeer.Write(payload)
	}()
	if _, err := io.ReadFull(leftPeer, got); err != nil {
		t.Fatalf("reverse read: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("reverse payload = %q, want %q", got, payload)
	}

	_ = leftPeer.Close()
	_ = rightPeer.Close()

	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("relay bridge did not terminate")
		}
	}
}

func TestRelayTimesOutWaitingForPeer(t *testing.T) {
	relay := NewRelay(50*time.Millisecond, nil)
	local, peer := net.Pipe()
	defer peer.Close()

	start := time.Now()
	err := relay.Bridge("lonely", local)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout took too long: %s", elapsed)
	}
}
