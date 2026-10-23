package collector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "collector"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func fixedNow() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

func validReport() Report {
	return Report{
		Schema:        Schema,
		DeploymentID:  "0123456789abcdef0123456789abcdef",
		ServerVersion: "0.5.0",
		ReportedAt:    fixedNow().Unix(),
		FirstSeen:     fixedNow().Add(-24 * time.Hour).Unix(),
		Devices:       DeviceCounts{Total: 4, Active7d: 3, Active30d: 4, Online: 2},
		Platforms:     PlatformCounts{Windows: 2, Android: 2},
		Records:       RecordCounts{Text: 10, Image: 2, File: 1, Bytes: 4096},
	}
}

func TestValidateAcceptsHealthyReports(t *testing.T) {
	if err := Validate(validReport(), fixedNow()); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateRejectsBadReports(t *testing.T) {
	cases := map[string]func(*Report){
		"unsupported schema":   func(r *Report) { r.Schema = Schema + 1 },
		"uppercase id":         func(r *Report) { r.DeploymentID = strings.ToUpper(r.DeploymentID) },
		"short id":             func(r *Report) { r.DeploymentID = "abc" },
		"future timestamp":     func(r *Report) { r.ReportedAt = fixedNow().Add(2 * time.Hour).Unix() },
		"stale timestamp":      func(r *Report) { r.ReportedAt = fixedNow().Add(-8 * 24 * time.Hour).Unix() },
		"first seen after now": func(r *Report) { r.FirstSeen = fixedNow().Add(time.Hour).Unix() },
		"negative counter":     func(r *Report) { r.Devices.Online = -1 },
		"huge counter":         func(r *Report) { r.Records.Text = maxCounterValue + 1 },
		"7d above 30d":         func(r *Report) { r.Devices.Active7d = 5; r.Devices.Active30d = 3 },
		"30d above total":      func(r *Report) { r.Devices.Active30d = 9 },
		"platform sum too big": func(r *Report) { r.Platforms.Other = 99 },
		"control character":    func(r *Report) { r.ServerVersion = "0.5\x00" },
		"long version":         func(r *Report) { r.ServerVersion = strings.Repeat("9", maxVersionLength+1) },
	}

	for name, mutate := range cases {
		report := validReport()
		mutate(&report)
		if err := Validate(report, fixedNow()); err == nil {
			t.Fatalf("%s should be rejected", name)
		}
	}
}

func TestRecordKeepsTheHighestDeviceTotal(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	report := validReport()
	report.Devices = DeviceCounts{Total: 5, Active7d: 4, Active30d: 5}
	if err := store.Record(ctx, report); err != nil {
		t.Fatalf("Record: %v", err)
	}

	// A later report with one device removed must not lower the count.
	report.Devices = DeviceCounts{Total: 3, Active7d: 2, Active30d: 3}
	report.Records.Text = 20
	if err := store.Record(ctx, report); err != nil {
		t.Fatalf("Record: %v", err)
	}

	totals, err := store.Totals(ctx)
	if err != nil {
		t.Fatalf("Totals: %v", err)
	}
	if totals.Deployments != 1 {
		t.Fatalf("deployments = %d, want 1", totals.Deployments)
	}
	if totals.Devices.Total != 5 {
		t.Fatalf("devices = %d, want 5", totals.Devices.Total)
	}
	if totals.Records.Text != 20 {
		t.Fatalf("text records = %d, want the latest value 20", totals.Records.Text)
	}
}

func TestTotalsSumAcrossDeploymentsAndGroupVersions(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	first := validReport()
	second := validReport()
	second.DeploymentID = "fedcba9876543210fedcba9876543210"
	second.ServerVersion = "0.4.0"
	second.Devices = DeviceCounts{Total: 2, Active7d: 1, Active30d: 2, Online: 1}
	second.Platforms = PlatformCounts{Android: 2}
	second.Records = RecordCounts{Text: 1}

	for _, report := range []Report{first, second} {
		if err := store.Record(ctx, report); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}

	totals, err := store.Totals(ctx)
	if err != nil {
		t.Fatalf("Totals: %v", err)
	}
	if totals.Deployments != 2 {
		t.Fatalf("deployments = %d, want 2", totals.Deployments)
	}
	if totals.Devices.Total != 6 {
		t.Fatalf("devices = %d, want 6", totals.Devices.Total)
	}
	if totals.Platforms.Windows != 2 || totals.Platforms.Android != 4 {
		t.Fatalf("platforms = %+v", totals.Platforms)
	}
	if totals.Records.Text != 11 || totals.Records.Bytes != 4096 {
		t.Fatalf("records = %+v", totals.Records)
	}
	if len(totals.Versions) != 2 {
		t.Fatalf("versions = %+v", totals.Versions)
	}
	if totals.Versions[0].Version != "0.5.0" && totals.Versions[0].Version != "0.4.0" {
		t.Fatalf("unexpected version entry: %+v", totals.Versions[0])
	}
}

func TestHandlerAcceptsReportAndServesStats(t *testing.T) {
	store := openTestStore(t)
	handler := NewHandler(store, HandlerOptions{
		AdminToken: "admin-token-1234567890",
		Now:        fixedNow,
	})

	body, err := json.Marshal(validReport())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/report", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/stats", nil)
	request.Header.Set("Authorization", "Bearer admin-token-1234567890")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}

	var totals Totals
	if err := json.Unmarshal(recorder.Body.Bytes(), &totals); err != nil {
		t.Fatalf("decode totals: %v", err)
	}
	if totals.Deployments != 1 || totals.Devices.Total != 4 {
		t.Fatalf("unexpected totals: %+v", totals)
	}
}

func TestHandlerProtectsStatsAndReports(t *testing.T) {
	store := openTestStore(t)
	handler := NewHandler(store, HandlerOptions{
		AdminToken:  "admin-token-1234567890",
		ReportToken: "report-token-1234567890",
		Now:         fixedNow,
	})

	// Stats without a token.
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/stats", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("stats status = %d, want 401", recorder.Code)
	}

	// Report without the shared token.
	body, _ := json.Marshal(validReport())
	recorder = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/report", strings.NewReader(string(body)))
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("report status = %d, want 401", recorder.Code)
	}

	// Stats are disabled when no admin token is configured.
	openHandler := NewHandler(store, HandlerOptions{Now: fixedNow})
	recorder = httptest.NewRecorder()
	openHandler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/stats", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("open stats status = %d, want 503", recorder.Code)
	}
}

func TestHandlerRejectsOversizedAndInvalidBodies(t *testing.T) {
	store := openTestStore(t)
	handler := NewHandler(store, HandlerOptions{
		MaxBodyBytes: 64,
		Now:          fixedNow,
	})

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/report",
		strings.NewReader(`{"schema":1,"deployment_id":"`+strings.Repeat("a", 200)+`"}`)))
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/report", strings.NewReader("not json")))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/report",
		strings.NewReader(`{"schema":1,"deployment_id":"zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"}`)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestHandlerRateLimitsPerAddress(t *testing.T) {
	store := openTestStore(t)
	handler := NewHandler(store, HandlerOptions{
		RatePerMinute: 2,
		Now:           fixedNow,
	})

	body, _ := json.Marshal(validReport())
	send := func() int {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/v1/report", strings.NewReader(string(body)))
		request.RemoteAddr = "203.0.113.7:4242"
		handler.ServeHTTP(recorder, request)
		return recorder.Code
	}

	if code := send(); code != http.StatusNoContent {
		t.Fatalf("first status = %d", code)
	}
	if code := send(); code != http.StatusNoContent {
		t.Fatalf("second status = %d", code)
	}
	if code := send(); code != http.StatusTooManyRequests {
		t.Fatalf("third status = %d, want 429", code)
	}
}
