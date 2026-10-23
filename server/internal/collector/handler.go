package collector

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Validation limits for a single report.
const (
	defaultMaxBodyBytes = 8 << 10
	maxCounterValue     = 1_000_000_000
	maxByteValue        = int64(1) << 50
	maxVersionLength    = 64
	reportClockSkew     = time.Hour
	reportMaxAge        = 7 * 24 * time.Hour
)

// HandlerOptions configures the collector HTTP surface.
type HandlerOptions struct {
	// ReportToken, when set, must be sent as "Authorization: Bearer <token>".
	ReportToken string
	// AdminToken protects GET /v1/stats. Without it the endpoint is disabled,
	// so a collector never exposes global counts by accident.
	AdminToken string
	// RatePerMinute limits reports per client address. Zero uses 12.
	RatePerMinute int
	MaxBodyBytes  int64
	Logger        *slog.Logger
	Now           func() time.Time
}

// NewHandler builds the collector HTTP handler.
func NewHandler(store *Store, options HandlerOptions) http.Handler {
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	limit := options.RatePerMinute
	if limit <= 0 {
		limit = 12
	}
	maxBody := options.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = defaultMaxBodyBytes
	}

	limiter := newIPLimiter(limit, now)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/report", func(w http.ResponseWriter, r *http.Request) {
		handleReport(store, options, now, maxBody, limiter, logger, w, r)
	})
	mux.HandleFunc("GET /v1/stats", func(w http.ResponseWriter, r *http.Request) {
		handleStats(store, options.AdminToken, now, logger, w, r)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})

	return securityHeaders(mux)
}

func handleReport(
	store *Store,
	options HandlerOptions,
	now func() time.Time,
	maxBody int64,
	limiter *ipLimiter,
	logger *slog.Logger,
	w http.ResponseWriter,
	r *http.Request,
) {
	if options.ReportToken != "" && !tokenMatches(r, options.ReportToken) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !limiter.allow(clientHost(r)) {
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}

	body := http.MaxBytesReader(w, r.Body, maxBody)
	defer body.Close()

	decoder := json.NewDecoder(body)
	var report Report
	if err := decoder.Decode(&report); err != nil {
		var maxBytes *http.MaxBytesError
		if errors.As(err, &maxBytes) {
			http.Error(w, "report is too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid report", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(new(struct{})); !errors.Is(err, io.EOF) {
		http.Error(w, "invalid report", http.StatusBadRequest)
		return
	}

	if err := Validate(report, now()); err != nil {
		logger.Warn("rejected telemetry report", "reason", err)
		http.Error(w, "invalid report", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := store.Record(ctx, report); err != nil {
		logger.Error("store telemetry report", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	logger.Info("telemetry report stored",
		"deployment_id", report.DeploymentID[:8],
		"version", report.ServerVersion,
		"devices", report.Devices.Total,
	)
	w.WriteHeader(http.StatusNoContent)
}

func handleStats(
	store *Store,
	adminToken string,
	now func() time.Time,
	logger *slog.Logger,
	w http.ResponseWriter,
	r *http.Request,
) {
	if adminToken == "" {
		http.Error(w, "statistics endpoint is disabled", http.StatusServiceUnavailable)
		return
	}
	if !tokenMatches(r, adminToken) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	totals, err := store.Totals(ctx)
	if err != nil {
		logger.Error("compute totals", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	totals.GeneratedAt = now().UTC().Unix()

	if strings.EqualFold(r.URL.Query().Get("format"), "text") {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, RenderText(totals))
		return
	}

	payload, err := json.Marshal(totals)
	if err != nil {
		logger.Error("encode totals", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

// RenderText formats the totals for a terminal. The first line answers "how
// many people use this?" using deployments, because a self-hosted, accountless
// system can only approximates people; the second line reports devices, which
// over-counts anyone with more than one client.
func RenderText(totals Totals) string {
	var builder strings.Builder
	builder.WriteString("MoliSync usage\n")
	fmt.Fprintf(&builder, "  users         : %d   (self-hosted deployments; one owner each)\n",
		totals.Deployments)
	fmt.Fprintf(&builder, "  installations : %d   (devices; 7d +%d, 30d +%d)\n",
		totals.Installations, totals.NewLast7Days, totals.NewLast30Days)
	fmt.Fprintf(&builder, "  devices       : %d total, %d online, %d active 7d, %d active 30d\n",
		totals.Devices.Total, totals.Devices.Online, totals.Devices.Active7d, totals.Devices.Active30d)
	fmt.Fprintf(&builder, "  platforms     : windows %d, android %d, other %d\n",
		totals.Platforms.Windows, totals.Platforms.Android, totals.Platforms.Other)
	fmt.Fprintf(&builder, "  records       : %d (text %d, image %d, file %d), %s\n",
		totals.Records.Text+totals.Records.Image+totals.Records.File,
		totals.Records.Text, totals.Records.Image, totals.Records.File,
		formatBytes(totals.Records.Bytes))
	for _, version := range totals.Versions {
		label := version.Version
		if label == "" {
			label = "(unknown)"
		}
		fmt.Fprintf(&builder, "  version %-8s: %d deployment(s), %d device(s)\n",
			label, version.Deployments, version.Devices)
	}
	return builder.String()
}

func formatBytes(value int64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	size := float64(value)
	for _, name := range units {
		size /= unit
		if size < unit {
			return fmt.Sprintf("%.1f %s", size, name)
		}
	}
	return fmt.Sprintf("%.1f EiB", size)
}

// Validate rejects reports that cannot come from a healthy deployment.
func Validate(report Report, now time.Time) error {
	if report.Schema < 1 || report.Schema > Schema {
		return fmt.Errorf("unsupported schema %d", report.Schema)
	}
	if !isDeploymentID(report.DeploymentID) {
		return errors.New("deployment_id must be 32 lowercase hex characters")
	}
	if len(report.ServerVersion) > maxVersionLength || hasControl(report.ServerVersion) {
		return errors.New("server_version is invalid")
	}

	reportedAt := time.Unix(report.ReportedAt, 0)
	switch {
	case report.ReportedAt <= 0:
		return errors.New("reported_at is required")
	case reportedAt.After(now.Add(reportClockSkew)):
		return errors.New("reported_at is in the future")
	case reportedAt.Before(now.Add(-reportMaxAge)):
		return errors.New("reported_at is too old")
	}
	if report.FirstSeen < 0 || (report.FirstSeen > 0 && report.FirstSeen > report.ReportedAt) {
		return errors.New("first_seen is invalid")
	}

	counters := map[string]int{
		"devices.total":      report.Devices.Total,
		"devices.active_7d":  report.Devices.Active7d,
		"devices.active_30d": report.Devices.Active30d,
		"devices.online":     report.Devices.Online,
		"platforms.windows":  report.Platforms.Windows,
		"platforms.android":  report.Platforms.Android,
		"platforms.other":    report.Platforms.Other,
		"records.text":       report.Records.Text,
		"records.image":      report.Records.Image,
		"records.file":       report.Records.File,
	}
	for name, value := range counters {
		if value < 0 || value > maxCounterValue {
			return fmt.Errorf("%s is out of range", name)
		}
	}
	if report.Records.Bytes < 0 || report.Records.Bytes > maxByteValue {
		return errors.New("records.bytes is out of range")
	}

	if report.Devices.Active7d > report.Devices.Active30d {
		return errors.New("active_7d cannot exceed active_30d")
	}
	if report.Devices.Active30d > report.Devices.Total {
		return errors.New("active_30d cannot exceed total")
	}
	if report.Devices.Online > report.Devices.Total {
		return errors.New("online cannot exceed total")
	}
	platformSum := report.Platforms.Windows + report.Platforms.Android + report.Platforms.Other
	if platformSum > report.Devices.Total {
		return errors.New("platform buckets cannot exceed total")
	}
	return nil
}

func isDeploymentID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func hasControl(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

func tokenMatches(r *http.Request, expected string) bool {
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	presented := strings.TrimSpace(header[len(prefix):])
	if len(presented) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(presented), []byte(expected)) == 1
}

func clientHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// ipLimiter is a small fixed-window limiter keyed by client address.
type ipLimiter struct {
	mutex   sync.Mutex
	limit   int
	window  time.Duration
	now     func() time.Time
	entries map[string]*limiterEntry
}

type limiterEntry struct {
	windowStart time.Time
	count       int
}

func newIPLimiter(limitPerMinute int, now func() time.Time) *ipLimiter {
	return &ipLimiter{
		limit:   limitPerMinute,
		window:  time.Minute,
		now:     now,
		entries: make(map[string]*limiterEntry),
	}
}

func (l *ipLimiter) allow(key string) bool {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	now := l.now()
	if len(l.entries) > 4096 {
		for existing, entry := range l.entries {
			if now.Sub(entry.windowStart) > l.window {
				delete(l.entries, existing)
			}
		}
	}

	entry, ok := l.entries[key]
	if !ok || now.Sub(entry.windowStart) >= l.window {
		l.entries[key] = &limiterEntry{windowStart: now, count: 1}
		return true
	}
	if entry.count >= l.limit {
		return false
	}
	entry.count++
	return true
}
