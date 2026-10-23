package ws

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/molirain/molisync/server/internal/store"
)

func TestNewDeviceHandlerFiresOncePerDevice(t *testing.T) {
	server, hub := newTestHub(t, nil)
	records, err := store.Open(store.Options{DataDir: filepath.Join(t.TempDir(), "data")})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = records.Close() })
	hub.SetStore(records)

	var mutex sync.Mutex
	seen := make(map[string]int)
	hub.SetNewDeviceHandler(func(deviceKey, platform string) {
		mutex.Lock()
		defer mutex.Unlock()
		seen[platform]++
		if deviceKey == "" {
			t.Error("device key must not be empty")
		}
	})

	first := dialTestClient(t, server, phoneDeviceID, nil)
	registerDevice(t, first, "Phone A")

	// A second connection from the same certificate is a reconnect, not a new
	// installation.
	second := dialTestClient(t, server, phoneDeviceID, nil)
	registerDevice(t, second, "Phone A")

	mutex.Lock()
	defer mutex.Unlock()
	if seen["android"] != 1 {
		t.Fatalf("installations = %d, want exactly 1", seen["android"])
	}
}
