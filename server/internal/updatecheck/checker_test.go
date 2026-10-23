package updatecheck

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseVersion(t *testing.T) {
	accepted := map[string]string{
		"0.5.1":        "0.5.1",
		"v0.5.1":       "0.5.1",
		" 1.2.3 ":      "1.2.3",
		"0.5.1-rc1":    "0.5.1",
		"0.5.1+build7": "0.5.1",
	}
	for input, want := range accepted {
		parsed, err := ParseVersion(input)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", input, err)
		}
		if parsed.String() != want {
			t.Fatalf("ParseVersion(%q) = %q, want %q", input, parsed.String(), want)
		}
	}

	for _, input := range []string{"", "0.5", "0.5.1.2", "a.b.c", "-1.0.0", "dev"} {
		if _, err := ParseVersion(input); err == nil {
			t.Fatalf("ParseVersion(%q) should fail", input)
		}
	}
}

func TestCompareAndGeneration(t *testing.T) {
	low := Version{0, 5, 1}
	high := Version{0, 5, 2}
	next := Version{0, 6, 0}

	if low.Compare(high) != -1 || high.Compare(low) != 1 || low.Compare(low) != 0 {
		t.Fatal("compare is wrong")
	}
	if !low.SameGeneration(high) {
		t.Fatal("same minor must be the same generation")
	}
	if low.SameGeneration(next) {
		t.Fatal("a different minor is a new generation")
	}
}

func newReleaseServer(t *testing.T, payload any, status int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("GitHub requires a User-Agent")
		}
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("accept = %q", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if payload == nil {
			return
		}
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func testChecker(t *testing.T, apiBase, component, current string) *Checker {
	t.Helper()
	checker, err := New(Options{
		Repository: "Molirain/Moli_Sync",
		Component:  component,
		Current:    current,
		APIBase:    apiBase,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:        func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return checker
}

func TestLatestIgnoresOtherComponentTags(t *testing.T) {
	server := newReleaseServer(t, []map[string]any{
		{"tag_name": "android-v0.9.0", "html_url": "https://example/android", "published_at": "2026-01-01T00:00:00Z"},
		{"tag_name": "server-v0.8.0", "html_url": "https://example/server", "published_at": "2026-01-01T00:00:00Z"},
		{"tag_name": "desktop-v0.5.1", "html_url": "https://example/desktop-051", "published_at": "2026-01-02T00:00:00Z"},
		{"tag_name": "desktop-v0.5.2", "html_url": "https://example/desktop-052", "published_at": "2026-01-03T00:00:00Z"},
		{"tag_name": "desktop-v0.5.3", "html_url": "https://example/draft", "draft": true},
		{"tag_name": "desktop-v0.5.4", "html_url": "https://example/pre", "prerelease": true},
		{"tag_name": "v9.9.9", "html_url": "https://example/umbrella"},
	}, http.StatusOK)

	checker := testChecker(t, server.URL, ComponentDesktop, "0.5.1")
	release, err := checker.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if release == nil || release.Version != "0.5.2" {
		t.Fatalf("release = %+v", release)
	}
	if release.URL != "https://example/desktop-052" {
		t.Fatalf("url = %q", release.URL)
	}

	status := checker.Status()
	if status.Component != ComponentDesktop || status.Current != "0.5.1" || status.Latest != "0.5.2" {
		t.Fatalf("status = %+v", status)
	}
	if status.UpToDate {
		t.Fatal("0.5.1 must not be reported as up to date when 0.5.2 exists")
	}
}

func TestLatestReportsUpToDate(t *testing.T) {
	server := newReleaseServer(t, []map[string]any{
		{"tag_name": "server-v0.5.1", "html_url": "https://example/server"},
	}, http.StatusOK)

	checker := testChecker(t, server.URL, ComponentServer, "0.5.1")
	if _, err := checker.Latest(context.Background()); err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if !checker.Status().UpToDate {
		t.Fatalf("status = %+v", checker.Status())
	}
}

func TestLatestCachesWithinTheInterval(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"tag_name":"android-v0.5.1","html_url":"https://example/a"}]`)
	}))
	defer server.Close()

	checker := testChecker(t, server.URL, ComponentAndroid, "0.5.0")
	for i := 0; i < 3; i++ {
		if _, err := checker.Latest(context.Background()); err != nil {
			t.Fatalf("Latest: %v", err)
		}
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want a single request", calls)
	}
}

func TestLatestSurfacesErrors(t *testing.T) {
	cases := map[string]int{
		"not found":   http.StatusNotFound,
		"rate limit":  http.StatusForbidden,
		"server down": http.StatusInternalServerError,
	}
	for name, status := range cases {
		server := newReleaseServer(t, nil, status)
		checker := testChecker(t, server.URL, ComponentDesktop, "0.5.0")
		if _, err := checker.Latest(context.Background()); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
		if checker.Status().LastError == "" {
			t.Fatalf("%s: last error was not recorded", name)
		}
		server.Close()
	}
}

func TestEmptyReleaseListIsNotAnError(t *testing.T) {
	server := newReleaseServer(t, []map[string]any{}, http.StatusOK)
	checker := testChecker(t, server.URL, ComponentDesktop, "0.5.0")

	release, err := checker.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if release != nil {
		t.Fatalf("release = %+v, want nil", release)
	}
	if !checker.Status().UpToDate {
		t.Fatal("an empty namespace must not be reported as an update")
	}
}

func TestNewRejectsUnparseableCurrentVersion(t *testing.T) {
	if _, err := New(Options{Repository: "a/b", Component: ComponentDesktop, Current: "dev"}); err == nil {
		t.Fatal("expected an error for a non-semver current version")
	}
	if _, err := New(Options{Component: ComponentDesktop, Current: "0.5.0"}); err == nil {
		t.Fatal("expected an error for a missing repository")
	}
}
