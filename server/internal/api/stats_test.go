package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/molirain/molisync/server/internal/store"
)

func TestStatsEndpointReportsLocalCounters(t *testing.T) {
	deps := testDeps(t)
	records, err := store.Open(store.Options{DataDir: filepath.Join(t.TempDir(), "data")})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = records.Close() })

	ctx := context.Background()
	if _, err := records.SaveText(ctx, "device-a", "Desk", "hello"); err != nil {
		t.Fatalf("SaveText: %v", err)
	}
	if _, err := records.SaveBlob(
		ctx, store.KindFile, "device-a", "Desk", "a.bin", "application/octet-stream", "", 4,
		bytes.NewReader([]byte("data")),
	); err != nil {
		t.Fatalf("SaveBlob: %v", err)
	}
	if _, _, err := records.TouchDevice(ctx, "device-a", "windows"); err != nil {
		t.Fatalf("TouchDevice: %v", err)
	}
	if _, _, err := records.TouchDevice(ctx, "device-b", "android"); err != nil {
		t.Fatalf("TouchDevice: %v", err)
	}

	deps.Store = records
	deps.Version = "0.5.0"
	router := NewRouter(deps)

	fingerprint := strings.Repeat("ab", 32)
	recorder := performRequest(t, router, http.MethodGet, "/api/v1/stats", "127.0.0.1:9000", map[string]string{
		"X-Client-Verify":      "SUCCESS",
		"X-Client-Fingerprint": fingerprint,
		"X-Client-DN":          "CN=desk,O=Molirain",
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var body statsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.ServerVersion != "0.5.0" || body.ProtocolVersion == 0 {
		t.Fatalf("unexpected version fields: %+v", body)
	}
	if body.Devices.Total != 2 || body.Devices.Active7d != 2 {
		t.Fatalf("unexpected devices: %+v", body.Devices)
	}
	if body.Platforms.Windows != 1 || body.Platforms.Android != 1 {
		t.Fatalf("unexpected platforms: %+v", body.Platforms)
	}
	if body.Records.Text != 1 || body.Records.File != 1 || body.Records.Bytes != 4 {
		t.Fatalf("unexpected records: %+v", body.Records)
	}
	if len(body.DeploymentID) != 32 {
		t.Fatalf("deployment id = %q", body.DeploymentID)
	}
	if body.FirstSeen == 0 {
		t.Fatal("first_seen was not reported")
	}
}

func TestStatsEndpointRequiresAuthentication(t *testing.T) {
	router := newTestRouter(t)
	recorder := performRequest(t, router, http.MethodGet, "/api/v1/stats", "127.0.0.1:9000", nil)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestStatsEndpointWithoutStoreIsUnavailable(t *testing.T) {
	router := newTestRouter(t)
	fingerprint := strings.Repeat("cd", 32)
	recorder := performRequest(t, router, http.MethodGet, "/api/v1/stats", "127.0.0.1:9000", map[string]string{
		"X-Client-Verify":      "SUCCESS",
		"X-Client-Fingerprint": fingerprint,
	})
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}
