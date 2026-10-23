package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/molirain/molisync/server/internal/store"
)

type fakeSource struct {
	snapshot store.Snapshot
	err      error
	calls    int
}

func (f *fakeSource) Snapshot(context.Context) (store.Snapshot, error) {
	f.calls++
	return f.snapshot, f.err
}

func testOptions(endpoint string) Options {
	return Options{
		Endpoint:        endpoint,
		Interval:        time.Hour,
		Timeout:         time.Second,
		ServerVersion:   "0.5.0",
		ProtocolVersion: 1,
		OnlineDevices:   func() int { return 2 },
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:             func() time.Time { return time.Unix(1_700_000_000, 0) },
		Rand:            rand.New(rand.NewSource(1)),
	}
}

func sampleSnapshot() store.Snapshot {
	return store.Snapshot{
		Devices: store.DeviceStats{
			Total: 7, Active7d: 3, Active30d: 5, Windows: 2, Android: 4, Other: 1,
		},
		Records:      store.RecordStats{Text: 40, Image: 5, File: 2, Bytes: 2048},
		FirstSeen:    time.Unix(1_600_000_000, 0).UTC(),
		DeploymentID: "0123456789abcdef0123456789abcdef",
	}
}

func TestReportOncePostsAggregatesOnly(t *testing.T) {
	var captured map[string]any
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		authorization = r.Header.Get("Authorization")
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content type = %q", r.Header.Get("Content-Type"))
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	source := &fakeSource{snapshot: sampleSnapshot()}
	options := testOptions(server.URL)
	options.Token = "secret-token"
	reporter, err := New(source, options)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := reporter.ReportOnce(context.Background()); err != nil {
		t.Fatalf("ReportOnce: %v", err)
	}
	if authorization != "Bearer secret-token" {
		t.Fatalf("authorization = %q", authorization)
	}
	if captured["schema"] != float64(Schema) {
		t.Fatalf("schema = %v", captured["schema"])
	}

	devices := captured["devices"].(map[string]any)
	if devices["total"] != float64(7) || devices["active_7d"] != float64(3) || devices["online"] != float64(2) {
		t.Fatalf("device counters = %v", devices)
	}
	platforms := captured["platforms"].(map[string]any)
	if platforms["windows"] != float64(2) || platforms["android"] != float64(4) || platforms["other"] != float64(1) {
		t.Fatalf("platform counters = %v", platforms)
	}
	records := captured["records"].(map[string]any)
	if records["bytes"] != float64(2048) {
		t.Fatalf("record bytes = %v", records["bytes"])
	}

	// The payload must not contain anything that could identify a device.
	for _, forbidden := range []string{"device_id", "name", "ip", "text_content", "file_name"} {
		if _, exists := captured[forbidden]; exists {
			t.Fatalf("report leaks %q", forbidden)
		}
	}

	status := reporter.Status()
	if !status.ReportedOnce || status.LastError != "" {
		t.Fatalf("unexpected status: %+v", status)
	}
	if status.LastSuccess == 0 {
		t.Fatal("last success was not recorded")
	}
}

func TestReportOnceSurfacesHTTPFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	reporter, err := New(&fakeSource{snapshot: sampleSnapshot()}, testOptions(server.URL))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := reporter.ReportOnce(context.Background()); err == nil {
		t.Fatal("expected an error for HTTP 429")
	}
	status := reporter.Status()
	if status.LastError == "" || status.ReportedOnce {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestReportOnceStopsWhenTheSnapshotFails(t *testing.T) {
	source := &fakeSource{err: context.DeadlineExceeded}
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer server.Close()

	reporter, err := New(source, testOptions(server.URL))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := reporter.ReportOnce(context.Background()); err == nil {
		t.Fatal("expected the snapshot error")
	}
	if called {
		t.Fatal("no request must be sent when the snapshot fails")
	}
}

func TestDisabledReporterIsInert(t *testing.T) {
	source := &fakeSource{snapshot: sampleSnapshot()}
	reporter, err := New(source, testOptions("   "))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if reporter.Enabled() {
		t.Fatal("an empty endpoint must disable reporting")
	}
	if err := reporter.ReportOnce(context.Background()); err == nil {
		t.Fatal("expected an error when reporting is disabled")
	}

	done := make(chan struct{})
	go func() {
		reporter.Run(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run must return immediately when disabled")
	}
	if source.calls != 0 {
		t.Fatalf("snapshot called %d times", source.calls)
	}
}

func TestStatusHidesTheQueryString(t *testing.T) {
	reporter, err := New(&fakeSource{}, testOptions("https://collector.example/v1/report?token=leak"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := reporter.Status().Endpoint; got != "https://collector.example/v1/report" {
		t.Fatalf("endpoint = %q", got)
	}
}

func TestRunReportsRepeatedlyUntilCancelled(t *testing.T) {
	var count int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		count++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	options := testOptions(server.URL)
	options.Interval = 10 * time.Millisecond
	options.InitialDelay = 5 * time.Millisecond
	reporter, err := New(&fakeSource{snapshot: sampleSnapshot()}, options)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Override the start-up delay by driving a context with a deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	reporter.Run(ctx)

	if count < 2 {
		t.Fatalf("reports = %d, want at least 2", count)
	}
}
