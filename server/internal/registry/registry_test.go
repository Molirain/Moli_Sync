package registry

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNormalizeEndpoints(t *testing.T) {
	input := []Endpoint{
		{Network: "LAN", IP: "192.168.1.20", Port: 9527},
		{Network: "vpn", IP: "100.64.0.8", Port: 9527},
		{Network: "lan", IP: "192.168.1.20", Port: 9527},
		{Network: "lan", IP: "10.0.0.4", Port: 9528, Priority: 200},
		{Network: "vpn", IP: "fd7a:115c:a1e0::1", Port: 9529, Priority: 300},
	}
	normalized, err := NormalizeEndpoints(input, 8)
	if err != nil {
		t.Fatalf("NormalizeEndpoints: %v", err)
	}
	if len(normalized) != 4 {
		t.Fatalf("expected duplicate removal, got %d entries: %+v", len(normalized), normalized)
	}
	if normalized[0].Priority != 300 || normalized[1].Priority != 200 {
		t.Fatalf("priority ordering is wrong: %+v", normalized)
	}
	if normalized[2].Priority != 100 || normalized[2].Network != NetworkLAN {
		t.Fatalf("LAN default priority missing: %+v", normalized[2])
	}
	if normalized[3].Priority != 50 || normalized[3].Network != NetworkVPN {
		t.Fatalf("VPN default priority missing: %+v", normalized[3])
	}
}

func TestNormalizeEndpointsRejections(t *testing.T) {
	tests := map[string]struct {
		endpoints []Endpoint
		max       int
	}{
		"bad network":    {endpoints: []Endpoint{{Network: "wan", IP: "192.168.1.20", Port: 1}}, max: 8},
		"loopback":       {endpoints: []Endpoint{{Network: "lan", IP: "127.0.0.1", Port: 1}}, max: 8},
		"unspecified":    {endpoints: []Endpoint{{Network: "lan", IP: "0.0.0.0", Port: 1}}, max: 8},
		"multicast":      {endpoints: []Endpoint{{Network: "lan", IP: "224.0.0.1", Port: 1}}, max: 8},
		"link local":     {endpoints: []Endpoint{{Network: "lan", IP: "169.254.10.1", Port: 1}}, max: 8},
		"missing port":   {endpoints: []Endpoint{{Network: "lan", IP: "192.168.1.20"}}, max: 8},
		"bad ip":         {endpoints: []Endpoint{{Network: "lan", IP: "not-an-ip", Port: 1}}, max: 8},
		"huge priority":  {endpoints: []Endpoint{{Network: "lan", IP: "192.168.1.20", Port: 1, Priority: 1001}}, max: 8},
		"too many":       {endpoints: []Endpoint{{Network: "lan", IP: "192.168.1.20", Port: 1}, {Network: "lan", IP: "192.168.1.21", Port: 1}}, max: 1},
		"zero max limit": {endpoints: nil, max: 0},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := NormalizeEndpoints(test.endpoints, test.max); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestRegistryConnectionLifecycle(t *testing.T) {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	registry := New(4, 4, now)

	endpoints := []Endpoint{{Network: NetworkLAN, IP: "192.168.1.20", Port: 9527}}
	device, created, err := registry.Connect("aaaaaaaa", "phone-a", "Phone A", "android", endpoints)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if !created || device.Connections != 1 {
		t.Fatalf("unexpected initial state: %+v", device)
	}

	device, created, err = registry.Connect("aaaaaaaa", "phone-a", "Phone A", "android", endpoints)
	if err != nil {
		t.Fatalf("second Connect: %v", err)
	}
	if created || device.Connections != 2 {
		t.Fatalf("second connection not reference counted: %+v", device)
	}

	device, offline := registry.Disconnect("aaaaaaaa")
	if offline || device.Connections != 1 {
		t.Fatalf("first disconnect must keep the device online: %+v offline=%v", device, offline)
	}
	device, offline = registry.Disconnect("aaaaaaaa")
	if !offline || device.Connections != 0 {
		t.Fatalf("last disconnect must take the device offline: %+v offline=%v", device, offline)
	}
	if registry.Len() != 0 {
		t.Fatalf("registry should be empty, has %d devices", registry.Len())
	}
}

func TestRegistryHeartbeatEndpointChanges(t *testing.T) {
	registry := New(4, 4, nil)
	initial := []Endpoint{{Network: NetworkLAN, IP: "192.168.1.20", Port: 9527}}
	if _, _, err := registry.Connect("aaaaaaaa", "phone-a", "Phone A", "android", initial); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	same := []Endpoint{{Network: NetworkLAN, IP: "192.168.1.20", Port: 9527, Priority: 100}}
	device, known, changed, err := registry.Heartbeat("aaaaaaaa", &same)
	if err != nil || !known || changed {
		t.Fatalf("equivalent endpoints reported a change: %+v known=%v changed=%v err=%v", device, known, changed, err)
	}

	updated := []Endpoint{{Network: NetworkVPN, IP: "100.64.0.8", Port: 9527}}
	device, known, changed, err = registry.Heartbeat("aaaaaaaa", &updated)
	if err != nil || !known || !changed {
		t.Fatalf("endpoint change not detected: known=%v changed=%v err=%v", known, changed, err)
	}
	if len(device.Endpoints) != 1 || device.Endpoints[0].Network != NetworkVPN {
		t.Fatalf("endpoints not replaced: %+v", device.Endpoints)
	}

	device, known, changed, err = registry.Heartbeat("aaaaaaaa", nil)
	if err != nil || !known || changed {
		t.Fatalf("heartbeat without endpoints must keep the pool: %+v known=%v changed=%v err=%v", device, known, changed, err)
	}
	if len(device.Endpoints) != 1 || device.Endpoints[0].Network != NetworkVPN {
		t.Fatalf("heartbeat cleared the endpoints: %+v", device.Endpoints)
	}

	invalid := []Endpoint{{Network: NetworkLAN, IP: "127.0.0.1", Port: 9527}}
	if _, _, _, err := registry.Heartbeat("aaaaaaaa", &invalid); err == nil {
		t.Fatal("invalid endpoint update was accepted")
	}
}

func TestRegistryDeviceLimit(t *testing.T) {
	registry := New(1, 4, nil)
	if _, _, err := registry.Connect("aaaaaaaa", "", "A", "linux", nil); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if _, _, err := registry.Connect("bbbbbbbb", "", "B", "linux", nil); !errors.Is(err, ErrTooManyDevices) {
		t.Fatalf("expected ErrTooManyDevices, got %v", err)
	}
}

func TestRegistrySweep(t *testing.T) {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	registry := New(4, 4, func() time.Time { return clock })
	if _, _, err := registry.Connect("aaaaaaaa", "", "A", "linux", nil); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if removed := registry.Sweep(clock.Add(-time.Minute)); len(removed) != 0 {
		t.Fatalf("device swept too early: %+v", removed)
	}

	clock = clock.Add(2 * time.Minute)
	removed := registry.Sweep(clock.Add(-time.Minute))
	if len(removed) != 1 || removed[0].ID != "aaaaaaaa" {
		t.Fatalf("stale device not swept: %+v", removed)
	}
	if registry.Len() != 0 {
		t.Fatalf("registry still has %d devices", registry.Len())
	}
}

func TestRegistrySnapshotIsCopy(t *testing.T) {
	registry := New(4, 4, nil)
	endpoints := []Endpoint{{Network: NetworkLAN, IP: "192.168.1.20", Port: 9527}}
	if _, _, err := registry.Connect("aaaaaaaa", "", "A", "linux", endpoints); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	snapshot := registry.Snapshot()
	snapshot[0].Endpoints[0].IP = "10.0.0.1"
	snapshot[0].Name = "mutated"

	fresh, ok := registry.Get("aaaaaaaa")
	if !ok {
		t.Fatal("device missing")
	}
	if fresh.Name != "A" || fresh.Endpoints[0].IP != "192.168.1.20" {
		t.Fatalf("snapshot aliased registry state: %+v", fresh)
	}
}

func TestRegistryConcurrentAccess(t *testing.T) {
	registry := New(128, 4, nil)
	var waitGroup sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		waitGroup.Add(1)
		go func(worker int) {
			defer waitGroup.Done()
			deviceID := "device-" + strings.Repeat("x", worker)
			for iteration := 0; iteration < 50; iteration++ {
				if _, _, err := registry.Connect(deviceID, "", "Device", "linux", nil); err != nil {
					t.Errorf("Connect: %v", err)
					return
				}
				registry.Touch(deviceID)
				registry.Snapshot()
				registry.Get(deviceID)
				registry.Disconnect(deviceID)
			}
		}(worker)
	}
	waitGroup.Wait()
	if registry.Len() != 0 {
		t.Fatalf("expected an empty registry, got %d devices", registry.Len())
	}
}

func TestNormalizeDeviceMetadata(t *testing.T) {
	name, platform, err := NormalizeDeviceMetadata("  Phone A  ", " Android ")
	if err != nil {
		t.Fatalf("NormalizeDeviceMetadata: %v", err)
	}
	if name != "Phone A" || platform != "android" {
		t.Fatalf("unexpected normalization: %q %q", name, platform)
	}

	invalid := []struct {
		name     string
		platform string
	}{
		{"", "android"},
		{strings.Repeat("a", 65), "android"},
		{"bad\x00name", "android"},
		{"Phone", "android two"},
		{"Phone", "android/../../"},
		{"Phone", ""},
	}
	for _, test := range invalid {
		if _, _, err := NormalizeDeviceMetadata(test.name, test.platform); err == nil {
			t.Fatalf("expected rejection for %q / %q", test.name, test.platform)
		}
	}
}

func TestValidateIdentity(t *testing.T) {
	if err := ValidateIdentity("", ""); err == nil {
		t.Fatal("empty device id accepted")
	}
	if err := ValidateIdentity("bad\nid", ""); err == nil {
		t.Fatal("control character in device id accepted")
	}
	if err := ValidateIdentity(strings.Repeat("a", 513), ""); err == nil {
		t.Fatal("oversized device id accepted")
	}
}
