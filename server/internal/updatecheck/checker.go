// Package updatecheck reports whether a newer release of one component exists.
//
// Each component owns its own tag namespace (desktop-vX.Y.Z, android-vX.Y.Z,
// server-vX.Y.Z) so a fix in one client never implies a release of the others.
// Only the matching namespace is considered here.
package updatecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Default endpoints and limits.
const (
	DefaultAPIBase   = "https://api.github.com"
	DefaultInterval  = 12 * time.Hour
	DefaultTimeout   = 10 * time.Second
	defaultPerPage   = 50
	maxResponseBytes = 1 << 20
)

// Component names used in tags.
const (
	ComponentDesktop   = "desktop"
	ComponentAndroid   = "android"
	ComponentServer    = "server"
	ComponentCollector = "collector"
)

// Version is a parsed MAJOR.MINOR.PATCH triple.
type Version struct {
	Major int
	Minor int
	Patch int
}

// ParseVersion accepts "0.5.1" and "v0.5.1". A leading pre-release or build
// suffix is ignored because clients only act on released versions.
func ParseVersion(raw string) (Version, error) {
	value := strings.TrimSpace(raw)
	value = strings.TrimPrefix(value, "v")
	if index := strings.IndexAny(value, "-+"); index >= 0 {
		value = value[:index]
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("updatecheck: %q is not MAJOR.MINOR.PATCH", raw)
	}
	numbers := make([]int, 3)
	for index, part := range parts {
		parsed, err := strconv.Atoi(part)
		if err != nil || parsed < 0 {
			return Version{}, fmt.Errorf("updatecheck: %q is not MAJOR.MINOR.PATCH", raw)
		}
		numbers[index] = parsed
	}
	return Version{Major: numbers[0], Minor: numbers[1], Patch: numbers[2]}, nil
}

// String renders the canonical form.
func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// Compare returns -1, 0 or 1.
func (v Version) Compare(other Version) int {
	switch {
	case v.Major != other.Major:
		return compareInt(v.Major, other.Major)
	case v.Minor != other.Minor:
		return compareInt(v.Minor, other.Minor)
	default:
		return compareInt(v.Patch, other.Patch)
	}
}

// SameGeneration reports whether both versions share MAJOR.MINOR. It is used to
// warn about protocol-incompatible pairs without refusing to run.
func (v Version) SameGeneration(other Version) bool {
	return v.Major == other.Major && v.Minor == other.Minor
}

func compareInt(left, right int) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

// Release is the newest release found in the component namespace.
type Release struct {
	Component   string    `json:"component"`
	Version     string    `json:"version"`
	Tag         string    `json:"tag"`
	URL         string    `json:"url"`
	Notes       string    `json:"notes,omitempty"`
	PublishedAt time.Time `json:"published_at,omitempty"`
}

// Status is the checker state surfaced in logs and the stats endpoint.
type Status struct {
	Component string `json:"component"`
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
	UpdateURL string `json:"update_url,omitempty"`
	CheckedAt int64  `json:"checked_at,omitempty"`
	UpToDate  bool   `json:"up_to_date"`
	LastError string `json:"last_error,omitempty"`
}

// Options configures a Checker.
type Options struct {
	Repository string
	Component  string
	Current    string
	APIBase    string
	Interval   time.Duration
	Timeout    time.Duration
	HTTPClient *http.Client
	Logger     *slog.Logger
	Now        func() time.Time
	// UserAgent is sent to the GitHub API; GitHub rejects requests without one.
	UserAgent string
}

// Checker caches the newest matching release.
type Checker struct {
	repository string
	component  string
	current    Version
	currentRaw string
	apiBase    string
	interval   time.Duration
	client     *http.Client
	logger     *slog.Logger
	now        func() time.Time
	userAgent  string

	mutex     sync.Mutex
	latest    *Release
	checkedAt time.Time
	lastError string
}

// ErrDisabled reports a checker that cannot run (for example a "dev" build).
var ErrDisabled = errors.New("updatecheck: disabled")

// New validates the options.
func New(options Options) (*Checker, error) {
	if strings.TrimSpace(options.Repository) == "" {
		return nil, errors.New("updatecheck: repository is required")
	}
	component := strings.TrimSpace(options.Component)
	if component == "" {
		return nil, errors.New("updatecheck: component is required")
	}

	current, err := ParseVersion(options.Current)
	if err != nil {
		return nil, err
	}

	now := options.Now
	if now == nil {
		now = time.Now
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	interval := options.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	apiBase := strings.TrimRight(strings.TrimSpace(options.APIBase), "/")
	if apiBase == "" {
		apiBase = DefaultAPIBase
	}
	userAgent := strings.TrimSpace(options.UserAgent)
	if userAgent == "" {
		userAgent = "MoliSync-UpdateCheck/" + current.String()
	}

	return &Checker{
		repository: strings.TrimSpace(options.Repository),
		component:  component,
		current:    current,
		currentRaw: current.String(),
		apiBase:    apiBase,
		interval:   interval,
		client:     client,
		logger:     logger,
		now:        now,
		userAgent:  userAgent,
	}, nil
}

// Current returns the running version.
func (c *Checker) Current() string { return c.currentRaw }

// TagPrefix is the release namespace this checker reads.
func (c *Checker) TagPrefix() string { return c.component + "-v" }

// Latest returns the newest release, using the cache when it is still fresh.
// A nil release with a nil error means no matching release exists yet.
func (c *Checker) Latest(ctx context.Context) (*Release, error) {
	c.mutex.Lock()
	cached := c.latest
	checkedAt := c.checkedAt
	c.mutex.Unlock()

	if !checkedAt.IsZero() && c.now().Sub(checkedAt) < c.interval {
		return cached, nil
	}

	release, err := c.fetch(ctx)
	if err != nil {
		c.mutex.Lock()
		c.lastError = err.Error()
		c.mutex.Unlock()
		return cached, err
	}

	c.mutex.Lock()
	c.latest = release
	c.checkedAt = c.now()
	c.lastError = ""
	c.mutex.Unlock()
	return release, nil
}

// Run refreshes the cached release every interval until the context ends.
func (c *Checker) Run(ctx context.Context) {
	// The first refresh is deferred a little so a restart storm does not hit
	// the API all at once.
	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		if _, err := c.Latest(ctx); err != nil && ctx.Err() == nil {
			c.logger.Warn("update check failed", "component", c.component, "error", err)
		} else if release := c.cachedRelease(); release != nil {
			c.logger.Info("update available",
				"component", c.component,
				"current", c.currentRaw,
				"latest", release.Version,
				"url", release.URL,
			)
		}
		if ctx.Err() != nil {
			return
		}
		timer.Reset(c.interval)
	}
}

// Status returns the cached state without performing a request.
func (c *Checker) Status() Status {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	status := Status{
		Component: c.component,
		Current:   c.currentRaw,
		LastError: c.lastError,
		UpToDate:  true,
	}
	if !c.checkedAt.IsZero() {
		status.CheckedAt = c.checkedAt.UnixMilli()
	}
	if c.latest != nil {
		status.Latest = c.latest.Version
		status.UpdateURL = c.latest.URL
		status.UpToDate = c.current.Compare(mustParse(c.latest.Version)) >= 0
	}
	return status
}

func (c *Checker) cachedRelease() *Release {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.latest
}

type githubRelease struct {
	TagName     string    `json:"tag_name"`
	HTMLURL     string    `json:"html_url"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
}

func (c *Checker) fetch(ctx context.Context) (*Release, error) {
	url := fmt.Sprintf("%s/repos/%s/releases?per_page=%d", c.apiBase, c.repository, defaultPerPage)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("updatecheck: build request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", c.userAgent)

	response, err := c.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("updatecheck: request releases: %w", err)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, fmt.Errorf("updatecheck: repository %s is not publicly readable", c.repository)
	case http.StatusForbidden, http.StatusTooManyRequests:
		return nil, errors.New("updatecheck: GitHub API rate limit reached")
	default:
		return nil, fmt.Errorf("updatecheck: GitHub API returned HTTP %d", response.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("updatecheck: read response: %w", err)
	}

	var releases []githubRelease
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, fmt.Errorf("updatecheck: decode response: %w", err)
	}

	return pickLatest(c.component, releases), nil
}

func pickLatest(component string, releases []githubRelease) *Release {
	prefix := component + "-v"
	type candidate struct {
		release Release
		version Version
	}
	var candidates []candidate

	for _, release := range releases {
		if release.Draft || release.Prerelease {
			continue
		}
		if !strings.HasPrefix(release.TagName, prefix) {
			continue
		}
		version, err := ParseVersion(strings.TrimPrefix(release.TagName, prefix))
		if err != nil {
			continue
		}
		candidates = append(candidates, candidate{
			release: Release{
				Component:   component,
				Version:     version.String(),
				Tag:         release.TagName,
				URL:         release.HTMLURL,
				Notes:       release.Body,
				PublishedAt: release.PublishedAt,
			},
			version: version,
		})
	}

	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(left, right int) bool {
		return candidates[left].version.Compare(candidates[right].version) > 0
	})
	selected := candidates[0].release
	return &selected
}

func mustParse(raw string) Version {
	parsed, err := ParseVersion(raw)
	if err != nil {
		return Version{}
	}
	return parsed
}
