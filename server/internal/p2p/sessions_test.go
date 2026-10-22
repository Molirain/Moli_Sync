package p2p

import (
	"errors"
	"testing"
	"time"
)

func newTestStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	store, err := NewStore(2 * time.Minute)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clock := now
	store.SetClock(func() time.Time { return clock })
	return store, &clock
}

func TestOpenIssuesUniqueTicketsAndVerifiesParticipants(t *testing.T) {
	store, _ := newTestStore(t)

	first, err := store.Open("device-a", "device-b")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	second, err := store.Open("device-a", "device-b")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if len(first.Ticket) != TicketSize {
		t.Fatalf("ticket length = %d, want %d", len(first.Ticket), TicketSize)
	}
	if first.ID == second.ID {
		t.Fatal("session ids must be unique")
	}
	if string(first.Ticket) == string(second.Ticket) {
		t.Fatal("tickets must be unique")
	}

	encoded := EncodeTicket(first.Ticket)
	verified, err := store.Verify(first.ID, "device-b", encoded)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if verified.Peer("device-b") != "device-a" {
		t.Fatalf("peer = %q", verified.Peer("device-b"))
	}

	if _, err := store.Verify(first.ID, "device-c", encoded); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}

	wrong := EncodeTicket(make([]byte, TicketSize))
	if _, err := store.Verify(first.ID, "device-a", wrong); !errors.Is(err, ErrBadTicket) {
		t.Fatalf("expected ErrBadTicket, got %v", err)
	}
}

func TestExpiredSessionsAreRejected(t *testing.T) {
	store, clock := newTestStore(t)
	session, err := store.Open("device-a", "device-b")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	*clock = clock.Add(3 * time.Minute)

	if _, err := store.Lookup(session.ID, "device-a"); !errors.Is(err, ErrExpired) {
		t.Fatalf("expected ErrExpired, got %v", err)
	}
	if store.Len() != 0 {
		t.Fatalf("expired sessions must be pruned, len = %d", store.Len())
	}
}

func TestOpenRejectsSelfSessions(t *testing.T) {
	store, _ := newTestStore(t)
	if _, err := store.Open("device-a", "device-a"); err == nil {
		t.Fatal("a session with itself must be rejected")
	}
}

func TestDecodeTicketRejectsWrongLength(t *testing.T) {
	if _, err := DecodeTicket(EncodeTicket([]byte("short"))); !errors.Is(err, ErrBadTicket) {
		t.Fatalf("expected ErrBadTicket, got %v", err)
	}
}
