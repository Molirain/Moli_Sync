package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type capture struct {
	mutex  sync.Mutex
	events []Event
	token  string
	status int
}

func (c *capture) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c.mutex.Lock()
		defer c.mutex.Unlock()
		if r.Method != http.MethodPost {
			t := r.Method
			_ = t
			c.status = http.StatusMethodNotAllowed
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		c.token = r.Header.Get("Authorization")
		var event Event
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			c.status = http.StatusBadRequest
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		c.events = append(c.events, event)
		if c.status != 0 {
			w.WriteHeader(c.status)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (c *capture) recorded() []Event {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return append([]Event(nil), c.events...)
}

func newReporter(t *testing.T, endpoint string, options ...func(*Options)) *Reporter {
	t.Helper()
	opts := Options{
		Endpoint:     endpoint,
		DeploymentID: "0123456789abcdef0123456789abcdef",
		Version:      "0.5.0",
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:          func() time.Time { return time.Unix(1_700_000_000, 0) },
	}
	for _, mutate := range options {
		mutate(&opts)
	}
	reporter, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return reporter
}

func TestEventIDSeparatesDeploymentsAndDevices(t *testing.T) {
	first := EventID("deploy-a", "device-key-1")
	if first != EventID("deploy-a", "device-key-1") {
		t.Fatal("the event id must be stable for the same device and deployment")
	}
	if first == EventID("deploy-b", "device-key-1") {
		t.Fatal("the same device must not be linkable across deployments")
	}
	if first == EventID("deploy-a", "device-key-2") {
		t.Fatal("different devices must produce different ids")
	}
	if len(first) != 32 {
		t.Fatalf("event id = %q, want 32 hex characters", first)
	}
}

func TestReportSendsOneAnonymisedEvent(t *testing.T) {
	sink := &capture{}
	server := httptest.NewServer(sink.handler())
	defer server.Close()

	reporter := newReporter(t, server.URL, func(options *Options) {
		options.Token = "shared-token"
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go reporter.Run(ctx)

	reporter.Report("device-key-1", "windows")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(sink.recorded()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}

	events := sink.recorded()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	event := events[0]
	if event.Schema != Schema || event.DeploymentID != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("unexpected event: %+v", event)
	}
	if event.Platform != "windows" || event.Version != "0.5.0" {
		t.Fatalf("unexpected metadata: %+v", event)
	}
	if event.EventID != EventID(event.DeploymentID, "device-key-1") {
		t.Fatalf("event id = %q", event.EventID)
	}
	if sink.token != "Bearer shared-token" {
		t.Fatalf("authorization = %q", sink.token)
	}

	// The payload must not contain anything identifying.
	raw, _ := json.Marshal(event)
	for _, forbidden := range []string{"device_key", "device_id", "name", "ip"} {
		if string(raw) != "" && contains(string(raw), forbidden) {
			t.Fatalf("event leaks %q: %s", forbidden, raw)
		}
	}

	status := reporter.Status()
	if status.Reported != 1 || status.Failed != 0 || status.LastError != "" {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestDisabledReporterDropsEvents(t *testing.T) {
	reporter := newReporter(t, "   ")
	if reporter.Enabled() {
		t.Fatal("an empty endpoint must disable reporting")
	}
	reporter.Report("device-key", "android")

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
	if status := reporter.Status(); status.Reported != 0 || status.Queued != 0 {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestSendRetriesThenReportsFailure(t *testing.T) {
	var attempts int
	var mutex sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mutex.Lock()
		attempts++
		mutex.Unlock()
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	reporter := newReporter(t, server.URL)
	err := reporter.SendNow(context.Background(), Event{
		Schema:       Schema,
		EventID:      "abc",
		DeploymentID: "deployment",
		Platform:     "windows",
		InstalledAt:  1,
	})
	if err == nil {
		t.Fatal("expected an error for HTTP 503")
	}

	mutex.Lock()
	count := attempts
	mutex.Unlock()
	if count != maxAttempts {
		t.Fatalf("attempts = %d, want %d", count, maxAttempts)
	}
	status := reporter.Status()
	if status.Failed != 1 || status.LastError == "" {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestSendNowSucceedsAgainstAHealthyEndpoint(t *testing.T) {
	sink := &capture{}
	server := httptest.NewServer(sink.handler())
	defer server.Close()

	reporter := newReporter(t, server.URL)
	event := Event{
		Schema:       Schema,
		EventID:      EventID("deployment", "device"),
		DeploymentID: "deployment",
		Platform:     "android",
		Version:      "0.5.0",
		InstalledAt:  1_700_000_000,
	}
	if err := reporter.SendNow(context.Background(), event); err != nil {
		t.Fatalf("SendNow: %v", err)
	}
	if len(sink.recorded()) != 1 {
		t.Fatalf("events = %d", len(sink.recorded()))
	}
}

func TestStatusHidesTheQueryString(t *testing.T) {
	reporter := newReporter(t, "https://stats.example.com/install?token=leak")
	if got := reporter.Status().Endpoint; got != "https://stats.example.com/install" {
		t.Fatalf("endpoint = %q", got)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		if haystack[index:index+len(needle)] == needle {
			return index
		}
	}
	return -1
}
