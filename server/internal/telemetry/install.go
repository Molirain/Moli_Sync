// Package telemetry reports one anonymous event the first time a device ever
// connects, so the project can count installations without tracking anyone.
//
// The event carries a deployment-scoped opaque id derived from the salted
// device key, the platform and the server version. It contains no device
// identifier, name, address or content, and it is sent at most once per device
// for the lifetime of the database.
package telemetry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Schema is the event schema version understood by the endpoint.
const Schema = 1

const (
	maxResponseBytes = 4 << 10
	queueCapacity    = 256
	maxAttempts      = 3
)

// Event is one first-install notification.
type Event struct {
	Schema       int    `json:"schema"`
	EventID      string `json:"event_id"`
	DeploymentID string `json:"deployment_id"`
	Platform     string `json:"platform"`
	Version      string `json:"version"`
	InstalledAt  int64  `json:"installed_at"`
}

// Status is the reporter state exposed by /api/v1/stats.
type Status struct {
	Enabled     bool   `json:"enabled"`
	Endpoint    string `json:"endpoint,omitempty"`
	Reported    int64  `json:"reported"`
	Failed      int64  `json:"failed"`
	Queued      int64  `json:"queued"`
	LastSuccess int64  `json:"last_success,omitempty"`
	LastError   string `json:"last_error,omitempty"`
}

// Options configures a Reporter.
type Options struct {
	Endpoint     string
	Token        string
	DeploymentID string
	Version      string
	Timeout      time.Duration
	HTTPClient   *http.Client
	Logger       *slog.Logger
	Now          func() time.Time
}

// Reporter serialises install events onto a bounded queue and posts them in the
// background, so a slow or unreachable endpoint never delays a connection.
type Reporter struct {
	endpoint     string
	token        string
	deploymentID string
	version      string
	client       *http.Client
	logger       *slog.Logger
	now          func() time.Time
	queue        chan Event

	stateMutex  sync.RWMutex
	reported    int64
	failed      int64
	lastSuccess time.Time
	lastError   string
}

// New validates the options. An empty endpoint disables reporting.
func New(options Options) (*Reporter, error) {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}

	return &Reporter{
		endpoint:     strings.TrimSpace(options.Endpoint),
		token:        strings.TrimSpace(options.Token),
		deploymentID: strings.TrimSpace(options.DeploymentID),
		version:      strings.TrimSpace(options.Version),
		client:       client,
		logger:       logger,
		now:          now,
		queue:        make(chan Event, queueCapacity),
	}, nil
}

// Enabled reports whether an endpoint is configured.
func (r *Reporter) Enabled() bool { return r.endpoint != "" }

// EventID derives the idempotency key of an installation. It is scoped to the
// deployment so the same device is not linkable across installations.
func EventID(deploymentID, deviceKey string) string {
	sum := sha256.Sum256([]byte("install\x00" + deploymentID + "\x00" + deviceKey))
	return hex.EncodeToString(sum[:16])
}

// Report queues a first-install event. It never blocks: when the queue is full
// the event is dropped and counted as a failure.
func (r *Reporter) Report(deviceKey, platform string) {
	if !r.Enabled() {
		return
	}
	event := Event{
		Schema:       Schema,
		EventID:      EventID(r.deploymentID, deviceKey),
		DeploymentID: r.deploymentID,
		Platform:     platform,
		Version:      r.version,
		InstalledAt:  r.now().UTC().Unix(),
	}
	select {
	case r.queue <- event:
	default:
		r.fail(errors.New("telemetry: install queue is full"))
	}
}

// Run drains the queue until the context is cancelled.
func (r *Reporter) Run(ctx context.Context) {
	if !r.Enabled() {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-r.queue:
			if err := r.send(ctx, event); err != nil && ctx.Err() == nil {
				r.logger.Warn("install report failed", "error", err)
			}
		}
	}
}

// SendNow posts one event synchronously. It exists for tests and for the
// explicit "report this installation" path.
func (r *Reporter) SendNow(ctx context.Context, event Event) error {
	return r.send(ctx, event)
}

func (r *Reporter) send(ctx context.Context, event Event) error {
	body, err := json.Marshal(event)
	if err != nil {
		r.fail(err)
		return fmt.Errorf("telemetry: encode event: %w", err)
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = r.post(ctx, body)
		if lastErr == nil {
			r.succeed()
			return nil
		}
		if attempt < maxAttempts {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
			}
		}
	}

	r.fail(lastErr)
	return lastErr
}

func (r *Reporter) post(ctx context.Context, body []byte) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(body))
	if err != nil {
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
		return fmt.Errorf("telemetry: post install: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))

	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("telemetry: endpoint returned HTTP %d", response.StatusCode)
	}
	return nil
}

// Status returns the current counters.
func (r *Reporter) Status() Status {
	r.stateMutex.RLock()
	defer r.stateMutex.RUnlock()
	status := Status{
		Enabled:   r.Enabled(),
		Endpoint:  safeEndpoint(r.endpoint),
		Reported:  r.reported,
		Failed:    r.failed,
		Queued:    int64(len(r.queue)),
		LastError: r.lastError,
	}
	if !r.lastSuccess.IsZero() {
		status.LastSuccess = r.lastSuccess.UnixMilli()
	}
	return status
}

func (r *Reporter) succeed() {
	r.stateMutex.Lock()
	defer r.stateMutex.Unlock()
	r.reported++
	r.lastSuccess = r.now()
	r.lastError = ""
}

func (r *Reporter) fail(err error) {
	r.stateMutex.Lock()
	defer r.stateMutex.Unlock()
	r.failed++
	if err != nil {
		r.lastError = err.Error()
	}
}

func userAgent(version string) string {
	if version == "" {
		return "MoliSync-Server/dev"
	}
	return "MoliSync-Server/" + version
}

// safeEndpoint strips any query string so a token in the URL never reaches the
// stats endpoint.
func safeEndpoint(endpoint string) string {
	if endpoint == "" {
		return ""
	}
	if index := strings.IndexAny(endpoint, "?#"); index >= 0 {
		return endpoint[:index]
	}
	return endpoint
}
