// Package telemetry sends a coarse, anonymous aggregate report to a collector
// so the project can count installs and devices without identifying anyone.
//
// A report contains only: a random per-installation id, version strings,
// timestamps and integer counters. Device identifiers, names, IP addresses and
// record contents never leave the deployment.
package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/molirain/molisync/server/internal/store"
)

// Schema is the report schema version understood by the collector.
const Schema = 1

// maxResponseBytes bounds how much of a collector response is read.
const maxResponseBytes = 4 << 10

// Snapshotter supplies the local counters for one report.
type Snapshotter interface {
	Snapshot(ctx context.Context) (store.Snapshot, error)
}

// Options configures a Reporter.
type Options struct {
	Endpoint string
	Token    string
	Interval time.Duration
	Timeout  time.Duration
	// InitialDelay overrides the randomised start-up delay. Zero keeps the
	// default 20-80 second spread.
	InitialDelay    time.Duration
	ServerVersion   string
	ProtocolVersion int
	OnlineDevices   func() int
	HTTPClient      *http.Client
	Logger          *slog.Logger
	Now             func() time.Time
	// Rand is used for start-up and interval jitter. Tests set a deterministic
	// source.
	Rand *rand.Rand
}

// Status is the read-only view exposed to the stats endpoint.
type Status struct {
	Enabled      bool   `json:"enabled"`
	Endpoint     string `json:"endpoint,omitempty"`
	LastAttempt  int64  `json:"last_attempt,omitempty"`
	LastSuccess  int64  `json:"last_success,omitempty"`
	LastError    string `json:"last_error,omitempty"`
	ReportedOnce bool   `json:"reported_once"`
}

// Reporter periodically posts the aggregate snapshot.
type Reporter struct {
	source    Snapshotter
	logger    *slog.Logger
	now       func() time.Time
	rand      *rand.Rand
	client    *http.Client
	endpoint  string
	token     string
	interval  time.Duration
	firstWait time.Duration
	version   string
	protocol  int
	online    func() int
	randMutex sync.Mutex

	stateMutex  sync.RWMutex
	lastAttempt time.Time
	lastSuccess time.Time
	lastError   string
}

// New validates the options and returns a reporter. An empty endpoint yields a
// reporter whose Run returns immediately, so callers can wire it
// unconditionally.
func New(source Snapshotter, options Options) (*Reporter, error) {
	if source == nil {
		return nil, errors.New("telemetry: snapshot source is required")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	random := options.Rand
	if random == nil {
		random = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	interval := options.Interval
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	online := options.OnlineDevices
	if online == nil {
		online = func() int { return 0 }
	}

	return &Reporter{
		source:    source,
		logger:    logger,
		now:       now,
		rand:      random,
		client:    client,
		endpoint:  strings.TrimSpace(options.Endpoint),
		token:     strings.TrimSpace(options.Token),
		interval:  interval,
		firstWait: options.InitialDelay,
		version:   strings.TrimSpace(options.ServerVersion),
		protocol:  options.ProtocolVersion,
		online:    online,
	}, nil
}

// Enabled reports whether reporting is configured.
func (r *Reporter) Enabled() bool { return r.endpoint != "" }

// Status returns the current reporting state.
func (r *Reporter) Status() Status {
	r.stateMutex.RLock()
	defer r.stateMutex.RUnlock()
	return Status{
		Enabled:      r.Enabled(),
		Endpoint:     safeEndpoint(r.endpoint),
		LastAttempt:  unixMilli(r.lastAttempt),
		LastSuccess:  unixMilli(r.lastSuccess),
		LastError:    r.lastError,
		ReportedOnce: !r.lastSuccess.IsZero(),
	}
}

// Run posts a report until the context is cancelled. The first report is
// delayed by a random offset so restarting a fleet does not create a spike.
func (r *Reporter) Run(ctx context.Context) {
	if !r.Enabled() {
		return
	}

	timer := time.NewTimer(r.initialDelay())
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		if err := r.ReportOnce(ctx); err != nil && ctx.Err() == nil {
			r.logger.Warn("telemetry report failed", "error", err)
		}
		if ctx.Err() != nil {
			return
		}
		timer.Reset(r.nextDelay())
	}
}

// ReportOnce sends one report immediately.
func (r *Reporter) ReportOnce(ctx context.Context) error {
	if !r.Enabled() {
		return errors.New("telemetry: no endpoint configured")
	}

	reportedAt := r.now().UTC()
	r.setState(func() { r.lastAttempt = reportedAt })

	snapshot, err := r.source.Snapshot(ctx)
	if err != nil {
		r.fail(err)
		return err
	}

	payload := report{
		Schema:          Schema,
		DeploymentID:    snapshot.DeploymentID,
		ServerVersion:   r.version,
		ProtocolVersion: r.protocol,
		ReportedAt:      reportedAt.Unix(),
		FirstSeen:       unixOrZero(snapshot.FirstSeen),
		Devices: deviceCounts{
			Total:     snapshot.Devices.Total,
			Active7d:  snapshot.Devices.Active7d,
			Active30d: snapshot.Devices.Active30d,
			Online:    r.online(),
		},
		Platforms: platformCounts{
			Windows: snapshot.Devices.Windows,
			Android: snapshot.Devices.Android,
			Other:   snapshot.Devices.Other,
		},
		Records: recordCounts{
			Text:  snapshot.Records.Text,
			Image: snapshot.Records.Image,
			File:  snapshot.Records.File,
			Bytes: snapshot.Records.Bytes,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		r.fail(err)
		return fmt.Errorf("telemetry: encode report: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(body))
	if err != nil {
		r.fail(err)
		return fmt.Errorf("telemetry: build request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", userAgent(r.version))
	if r.token != "" {
		request.Header.Set("Authorization", "Bearer "+r.token)
	}

	response, err := r.client.Do(request)
	if err != nil {
		r.fail(err)
		return fmt.Errorf("telemetry: post report: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))

	if response.StatusCode < 200 || response.StatusCode > 299 {
		statusErr := fmt.Errorf("telemetry: collector returned HTTP %d", response.StatusCode)
		r.fail(statusErr)
		return statusErr
	}

	r.setState(func() {
		r.lastSuccess = reportedAt
		r.lastError = ""
	})
	r.logger.Debug("telemetry report sent", "deployment_id", snapshot.DeploymentID)
	return nil
}

type report struct {
	Schema          int            `json:"schema"`
	DeploymentID    string         `json:"deployment_id"`
	ServerVersion   string         `json:"server_version"`
	ProtocolVersion int            `json:"protocol_version"`
	ReportedAt      int64          `json:"reported_at"`
	FirstSeen       int64          `json:"first_seen"`
	Devices         deviceCounts   `json:"devices"`
	Platforms       platformCounts `json:"platforms"`
	Records         recordCounts   `json:"records"`
}

type deviceCounts struct {
	Total     int `json:"total"`
	Active7d  int `json:"active_7d"`
	Active30d int `json:"active_30d"`
	Online    int `json:"online"`
}

type platformCounts struct {
	Windows int `json:"windows"`
	Android int `json:"android"`
	Other   int `json:"other"`
}

type recordCounts struct {
	Text  int   `json:"text"`
	Image int   `json:"image"`
	File  int   `json:"file"`
	Bytes int64 `json:"bytes"`
}

func (r *Reporter) initialDelay() time.Duration {
	if r.firstWait > 0 {
		return r.firstWait
	}
	// 20-80 seconds keeps the first report prompt without stampeding.
	return 20*time.Second + r.jitter(60*time.Second)
}

func (r *Reporter) nextDelay() time.Duration {
	// Up to 10% jitter smooths a synchronised fleet.
	return r.interval + r.jitter(r.interval/10)
}

func (r *Reporter) jitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	r.randMutex.Lock()
	defer r.randMutex.Unlock()
	return time.Duration(r.rand.Int63n(int64(max)))
}

func (r *Reporter) setState(update func()) {
	r.stateMutex.Lock()
	defer r.stateMutex.Unlock()
	update()
}

func (r *Reporter) fail(err error) {
	r.setState(func() { r.lastError = err.Error() })
}

func unixMilli(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UnixMilli()
}

func unixOrZero(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.Unix()
}

func userAgent(version string) string {
	if version == "" {
		return "MoliSync-Server/dev"
	}
	return "MoliSync-Server/" + version
}

// safeEndpoint strips the query string so a token passed in the URL never
// reaches the stats endpoint.
func safeEndpoint(endpoint string) string {
	if endpoint == "" {
		return ""
	}
	if index := strings.IndexAny(endpoint, "?#"); index >= 0 {
		return endpoint[:index]
	}
	return endpoint
}
