// Package registry keeps the authoritative in-memory table of online devices.
//
// Every exported method is safe for concurrent use. Devices are reference
// counted by connection so a client can hold several control-plane sockets
// (for example a desktop app and a background service) without being marked
// offline until the last one closes.
package registry

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// ErrTooManyDevices is returned when accepting a new device would exceed the
// configured device limit.
var ErrTooManyDevices = errors.New("device limit reached")

const (
	maxDeviceNameLength = 64
	maxPlatformLength   = 16
	maxCommonNameLength = 512
	maxDeviceIDLength   = 512
)

var platformPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,15}$`)

// Device is an immutable snapshot of a registered device.
type Device struct {
	ID          string
	Fingerprint string
	CommonName  string
	Name        string
	Platform    string
	Endpoints   []Endpoint
	ConnectedAt time.Time
	LastSeen    time.Time
	Connections int
}

// Registry is the concurrent device table.
type Registry struct {
	mu           sync.RWMutex
	devices      map[string]*Device
	maxDevices   int
	maxEndpoints int
	now          func() time.Time
}

// New creates a registry. maxDevices bounds the table, maxEndpoints bounds the
// candidate pool of a single device.
func New(maxDevices, maxEndpoints int, now func() time.Time) *Registry {
	if now == nil {
		now = time.Now
	}
	return &Registry{
		devices:      make(map[string]*Device),
		maxDevices:   maxDevices,
		maxEndpoints: maxEndpoints,
		now:          now,
	}
}

// NormalizeDeviceMetadata validates the client supplied alias and platform and
// returns their canonical form.
func NormalizeDeviceMetadata(name, platform string) (string, string, error) {
	name = strings.TrimSpace(name)
	platform = strings.ToLower(strings.TrimSpace(platform))

	if name == "" {
		return "", "", errors.New("device name must not be empty")
	}
	if !utf8.ValidString(name) {
		return "", "", errors.New("device name must be valid UTF-8")
	}
	if utf8.RuneCountInString(name) > maxDeviceNameLength {
		return "", "", fmt.Errorf("device name must be at most %d characters", maxDeviceNameLength)
	}
	for _, r := range name {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return "", "", errors.New("device name must not contain control characters")
		}
	}
	if !platformPattern.MatchString(platform) {
		return "", "", fmt.Errorf("platform %q must match %s", platform, platformPattern)
	}
	return name, platform, nil
}

// ValidateIdentity checks the identity values derived from the client
// certificate before they enter the registry.
func ValidateIdentity(deviceID, commonName string) error {
	if deviceID == "" || len(deviceID) > maxDeviceIDLength {
		return errors.New("device id is empty or too long")
	}
	for _, r := range deviceID {
		if unicode.IsControl(r) {
			return errors.New("device id must not contain control characters")
		}
	}
	if len(commonName) > maxCommonNameLength {
		return fmt.Errorf("common name must be at most %d bytes", maxCommonNameLength)
	}
	return nil
}

// Connect registers a connection for a device and returns the updated snapshot.
// The returned bool reports whether this is a newly seen device. fingerprint is
// the certificate digest, which stays available even when DeviceID is derived
// from the common name; peers pin P2P connections to it.
func (r *Registry) Connect(deviceID, fingerprint, commonName, name, platform string, endpoints []Endpoint) (Device, bool, error) {
	if err := ValidateIdentity(deviceID, commonName); err != nil {
		return Device{}, false, err
	}
	name, platform, err := NormalizeDeviceMetadata(name, platform)
	if err != nil {
		return Device{}, false, err
	}
	normalizedEndpoints, err := NormalizeEndpoints(endpoints, r.maxEndpoints)
	if err != nil {
		return Device{}, false, err
	}

	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()

	device, exists := r.devices[deviceID]
	if !exists {
		if len(r.devices) >= r.maxDevices {
			return Device{}, false, ErrTooManyDevices
		}
		device = &Device{ID: deviceID, ConnectedAt: now}
		r.devices[deviceID] = device
	}
	device.CommonName = commonName
	device.Fingerprint = fingerprint
	device.Name = name
	device.Platform = platform
	device.Endpoints = normalizedEndpoints
	device.LastSeen = now
	device.Connections++
	return cloneDevice(device), !exists, nil
}

// Heartbeat refreshes liveness and optionally replaces the candidate pool.
// A nil endpoints pointer leaves the pool untouched; a non-nil pointer is
// authoritative, including an empty slice. The returned booleans report
// whether the device is still registered and whether its endpoints changed.
func (r *Registry) Heartbeat(deviceID string, endpoints *[]Endpoint) (Device, bool, bool, error) {
	var normalized []Endpoint
	if endpoints != nil {
		var err error
		normalized, err = NormalizeEndpoints(*endpoints, r.maxEndpoints)
		if err != nil {
			return Device{}, false, false, err
		}
	}

	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()

	device, exists := r.devices[deviceID]
	if !exists {
		return Device{}, false, false, nil
	}
	device.LastSeen = now
	changed := false
	if endpoints != nil && !slices.Equal(device.Endpoints, normalized) {
		device.Endpoints = normalized
		changed = true
	}
	return cloneDevice(device), true, changed, nil
}

// Touch refreshes liveness without changing any other field.
func (r *Registry) Touch(deviceID string) (Device, bool) {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()

	device, exists := r.devices[deviceID]
	if !exists {
		return Device{}, false
	}
	device.LastSeen = now
	return cloneDevice(device), true
}

// Disconnect releases one connection of a device. The returned bool reports
// whether the device became offline.
func (r *Registry) Disconnect(deviceID string) (Device, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	device, exists := r.devices[deviceID]
	if !exists {
		return Device{}, false
	}
	device.Connections--
	if device.Connections > 0 {
		return cloneDevice(device), false
	}
	delete(r.devices, deviceID)
	return cloneDevice(device), true
}

// Get returns a snapshot of a single device.
func (r *Registry) Get(deviceID string) (Device, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	device, exists := r.devices[deviceID]
	if !exists {
		return Device{}, false
	}
	return cloneDevice(device), true
}

// Snapshot returns every registered device ordered by connection time and ID.
func (r *Registry) Snapshot() []Device {
	r.mu.RLock()
	defer r.mu.RUnlock()

	devices := make([]Device, 0, len(r.devices))
	for _, device := range r.devices {
		devices = append(devices, cloneDevice(device))
	}
	sort.Slice(devices, func(i, j int) bool {
		if !devices[i].ConnectedAt.Equal(devices[j].ConnectedAt) {
			return devices[i].ConnectedAt.Before(devices[j].ConnectedAt)
		}
		return devices[i].ID < devices[j].ID
	})
	return devices
}

// Len returns the number of registered devices.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.devices)
}

// Sweep removes devices whose last heartbeat is older than cutoff and returns
// their final snapshots.
func (r *Registry) Sweep(cutoff time.Time) []Device {
	r.mu.Lock()
	defer r.mu.Unlock()

	removed := make([]Device, 0)
	for deviceID, device := range r.devices {
		if device.LastSeen.Before(cutoff) {
			removed = append(removed, cloneDevice(device))
			delete(r.devices, deviceID)
		}
	}
	sort.Slice(removed, func(i, j int) bool { return removed[i].ID < removed[j].ID })
	return removed
}

func cloneDevice(device *Device) Device {
	clone := *device
	clone.Endpoints = append([]Endpoint(nil), device.Endpoints...)
	return clone
}
