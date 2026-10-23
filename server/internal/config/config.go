// Package config loads and validates the MoliSync server configuration.
//
// Precedence is: built-in defaults, then the YAML file, then environment
// variables. The process refuses to start when the resulting configuration is
// unsafe (for example a catch-all trusted proxy range).
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Device identity sources accepted by AuthConfig.DeviceIDSource.
const (
	DeviceIDSourceFingerprint = "fingerprint"
	DeviceIDSourceCommonName  = "dn_cn"
)

// Duration is a time.Duration that is written as a human readable string
// (for example "15s") in YAML and in environment variables.
type Duration time.Duration

// Std returns the value as a time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// String implements fmt.Stringer.
func (d Duration) String() string { return time.Duration(d).String() }

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var raw string
	if err := value.Decode(&raw); err != nil {
		return fmt.Errorf("duration must be a string such as \"15s\": %w", err)
	}
	parsed, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", raw, err)
	}
	*d = Duration(parsed)
	return nil
}

// MarshalYAML implements yaml.Marshaler.
func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

// Config is the complete server configuration.
type Config struct {
	Server      ServerConfig      `yaml:"server"`
	Proxy       ProxyConfig       `yaml:"proxy"`
	Auth        AuthConfig        `yaml:"auth"`
	Registry    RegistryConfig    `yaml:"registry"`
	Storage     StorageConfig     `yaml:"storage"`
	WebSocket   WebSocketConfig   `yaml:"websocket"`
	Telemetry   TelemetryConfig   `yaml:"telemetry"`
	UpdateCheck UpdateCheckConfig `yaml:"update_check"`
	Log         LogConfig         `yaml:"log"`
}

// ServerConfig covers the HTTP listener.
type ServerConfig struct {
	Listen            string   `yaml:"listen" env:"MOLISYNC_SERVER_LISTEN"`
	ReadHeaderTimeout Duration `yaml:"read_header_timeout" env:"MOLISYNC_SERVER_READ_HEADER_TIMEOUT"`
	ReadTimeout       Duration `yaml:"read_timeout" env:"MOLISYNC_SERVER_READ_TIMEOUT"`
	WriteTimeout      Duration `yaml:"write_timeout" env:"MOLISYNC_SERVER_WRITE_TIMEOUT"`
	IdleTimeout       Duration `yaml:"idle_timeout" env:"MOLISYNC_SERVER_IDLE_TIMEOUT"`
	ShutdownTimeout   Duration `yaml:"shutdown_timeout" env:"MOLISYNC_SERVER_SHUTDOWN_TIMEOUT"`
	MaxHeaderBytes    int      `yaml:"max_header_bytes" env:"MOLISYNC_SERVER_MAX_HEADER_BYTES"`
}

// ProxyConfig describes the trusted reverse proxy and the headers it injects
// after a successful mTLS handshake.
type ProxyConfig struct {
	TrustedCIDRs        []string `yaml:"trusted_cidrs" env:"MOLISYNC_PROXY_TRUSTED_CIDRS"`
	VerifyHeader        string   `yaml:"verify_header" env:"MOLISYNC_PROXY_VERIFY_HEADER"`
	FingerprintHeader   string   `yaml:"fingerprint_header" env:"MOLISYNC_PROXY_FINGERPRINT_HEADER"`
	DNHeader            string   `yaml:"dn_header" env:"MOLISYNC_PROXY_DN_HEADER"`
	RequiredVerifyValue string   `yaml:"required_verify_value" env:"MOLISYNC_PROXY_REQUIRED_VERIFY_VALUE"`
}

// AuthConfig controls how the certificate identity is turned into a DeviceID.
type AuthConfig struct {
	DeviceIDSource            string `yaml:"device_id_source" env:"MOLISYNC_AUTH_DEVICE_ID_SOURCE"`
	MaxHeaderValueBytes       int    `yaml:"max_header_value_bytes" env:"MOLISYNC_AUTH_MAX_HEADER_VALUE_BYTES"`
	MaxCommonNameLength       int    `yaml:"max_common_name_length" env:"MOLISYNC_AUTH_MAX_COMMON_NAME_LENGTH"`
	MaxDistinguishedNameBytes int    `yaml:"max_dn_bytes" env:"MOLISYNC_AUTH_MAX_DN_BYTES"`
}

// RegistryConfig controls the in-memory device table.
type RegistryConfig struct {
	OfflineAfter          Duration `yaml:"offline_after" env:"MOLISYNC_REGISTRY_OFFLINE_AFTER"`
	SweepInterval         Duration `yaml:"sweep_interval" env:"MOLISYNC_REGISTRY_SWEEP_INTERVAL"`
	MaxDevices            int      `yaml:"max_devices" env:"MOLISYNC_REGISTRY_MAX_DEVICES"`
	MaxEndpointsPerDevice int      `yaml:"max_endpoints_per_device" env:"MOLISYNC_REGISTRY_MAX_ENDPOINTS_PER_DEVICE"`
}

// StorageConfig controls the durable record database and blob directory.
type StorageConfig struct {
	DataDir string `yaml:"data_dir" env:"MOLISYNC_STORAGE_DATA_DIR"`
	// BlobDir defaults to <data_dir>/blobs when empty.
	BlobDir      string `yaml:"blob_dir" env:"MOLISYNC_STORAGE_BLOB_DIR"`
	MaxItemBytes int64  `yaml:"max_item_bytes" env:"MOLISYNC_STORAGE_MAX_ITEM_BYTES"`
}

// WebSocketConfig covers the control plane connection behaviour.
type WebSocketConfig struct {
	ReadLimitBytes       int64    `yaml:"read_limit_bytes" env:"MOLISYNC_WEBSOCKET_READ_LIMIT_BYTES"`
	HandshakeTimeout     Duration `yaml:"handshake_timeout" env:"MOLISYNC_WEBSOCKET_HANDSHAKE_TIMEOUT"`
	HelloTimeout         Duration `yaml:"hello_timeout" env:"MOLISYNC_WEBSOCKET_HELLO_TIMEOUT"`
	PingInterval         Duration `yaml:"ping_interval" env:"MOLISYNC_WEBSOCKET_PING_INTERVAL"`
	PongTimeout          Duration `yaml:"pong_timeout" env:"MOLISYNC_WEBSOCKET_PONG_TIMEOUT"`
	WriteTimeout         Duration `yaml:"write_timeout" env:"MOLISYNC_WEBSOCKET_WRITE_TIMEOUT"`
	SendBuffer           int      `yaml:"send_buffer" env:"MOLISYNC_WEBSOCKET_SEND_BUFFER"`
	HeartbeatInterval    Duration `yaml:"heartbeat_interval" env:"MOLISYNC_WEBSOCKET_HEARTBEAT_INTERVAL"`
	MaxMessagesPerSecond int      `yaml:"max_messages_per_second" env:"MOLISYNC_WEBSOCKET_MAX_MESSAGES_PER_SECOND"`
	AllowedOrigins       []string `yaml:"allowed_origins" env:"MOLISYNC_WEBSOCKET_ALLOWED_ORIGINS"`
}

// LogConfig controls structured logging.
type LogConfig struct {
	Level  string `yaml:"level" env:"MOLISYNC_LOG_LEVEL"`
	Format string `yaml:"format" env:"MOLISYNC_LOG_FORMAT"`
}

// TelemetryConfig controls the one-time anonymous install report. The event
// contains only a deployment-scoped opaque id, the platform and the server
// version; it never includes a device identifier, name, address or content.
type TelemetryConfig struct {
	Enabled  bool     `yaml:"enabled" env:"MOLISYNC_TELEMETRY_ENABLED"`
	Endpoint string   `yaml:"endpoint" env:"MOLISYNC_TELEMETRY_ENDPOINT"`
	Token    string   `yaml:"token" env:"MOLISYNC_TELEMETRY_TOKEN"`
	Timeout  Duration `yaml:"timeout" env:"MOLISYNC_TELEMETRY_TIMEOUT"`
}

// Active reports whether the reporter should run. An enabled reporter without
// an endpoint stays silent, which keeps older configuration files valid.
func (t TelemetryConfig) Active() bool {
	return t.Enabled && strings.TrimSpace(t.Endpoint) != ""
}

// UpdateCheckConfig controls the server's own release lookup. Clients read the
// result through /api/v1/me instead of querying GitHub themselves, so one
// deployment produces one request per interval rather than one per device.
type UpdateCheckConfig struct {
	Enabled    bool     `yaml:"enabled" env:"MOLISYNC_UPDATE_CHECK_ENABLED"`
	Repository string   `yaml:"repository" env:"MOLISYNC_UPDATE_CHECK_REPOSITORY"`
	APIBase    string   `yaml:"api_base" env:"MOLISYNC_UPDATE_CHECK_API_BASE"`
	Interval   Duration `yaml:"interval" env:"MOLISYNC_UPDATE_CHECK_INTERVAL"`
	Timeout    Duration `yaml:"timeout" env:"MOLISYNC_UPDATE_CHECK_TIMEOUT"`
}

// Default returns a configuration that is safe to run on a host where the
// reverse proxy connects from loopback.
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			Listen:            "127.0.0.1:9520",
			ReadHeaderTimeout: Duration(10 * time.Second),
			ReadTimeout:       0,
			WriteTimeout:      0,
			IdleTimeout:       Duration(120 * time.Second),
			ShutdownTimeout:   Duration(15 * time.Second),
			MaxHeaderBytes:    64 << 10,
		},
		Proxy: ProxyConfig{
			TrustedCIDRs:        []string{"127.0.0.1/32", "::1/128"},
			VerifyHeader:        "X-Client-Verify",
			FingerprintHeader:   "X-Client-Fingerprint",
			DNHeader:            "X-Client-DN",
			RequiredVerifyValue: "SUCCESS",
		},
		Auth: AuthConfig{
			DeviceIDSource:            DeviceIDSourceFingerprint,
			MaxHeaderValueBytes:       1024,
			MaxCommonNameLength:       128,
			MaxDistinguishedNameBytes: 1024,
		},
		Registry: RegistryConfig{
			OfflineAfter:          Duration(90 * time.Second),
			SweepInterval:         Duration(15 * time.Second),
			MaxDevices:            4096,
			MaxEndpointsPerDevice: 16,
		},
		Storage: StorageConfig{
			DataDir:      "./data",
			BlobDir:      "",
			MaxItemBytes: 64 << 20,
		},
		WebSocket: WebSocketConfig{
			ReadLimitBytes:       64 << 10,
			HandshakeTimeout:     Duration(10 * time.Second),
			HelloTimeout:         Duration(10 * time.Second),
			PingInterval:         Duration(25 * time.Second),
			PongTimeout:          Duration(60 * time.Second),
			WriteTimeout:         Duration(10 * time.Second),
			SendBuffer:           64,
			HeartbeatInterval:    Duration(25 * time.Second),
			MaxMessagesPerSecond: 20,
			AllowedOrigins:       nil,
		},
		Telemetry: TelemetryConfig{
			Enabled:  true,
			Endpoint: "https://statistics-api.molirain.com/install",
			Timeout:  Duration(10 * time.Second),
		},
		UpdateCheck: UpdateCheckConfig{
			Enabled:    true,
			Repository: "Molirain/Moli_Sync",
			APIBase:    "https://api.github.com",
			Interval:   Duration(12 * time.Hour),
			Timeout:    Duration(10 * time.Second),
		},
		Log: LogConfig{Level: "info", Format: "json"},
	}
}

// Load builds a configuration from defaults, an optional YAML file and the
// MOLISYNC_* environment variables, then validates it. An empty path skips
// the file and is intended for environment-only deployments.
func Load(path string) (*Config, error) {
	cfg := Default()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read config file: %w", err)
		}
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		if err := decoder.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("parse config file %q: %w", path, err)
		}
		var extra yaml.Node
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("config file %q must contain a single YAML document", path)
		}
	}

	if err := applyEnv(cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate reports every invalid setting at once so a deployment can be fixed
// in a single pass.
func (c *Config) Validate() error {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	if err := validateListenAddress(c.Server.Listen); err != nil {
		add("server.listen: %v", err)
	}
	validatePositive(add, "server.read_header_timeout", c.Server.ReadHeaderTimeout, time.Second)
	validateNonNegative(add, "server.read_timeout", c.Server.ReadTimeout)
	validateNonNegative(add, "server.write_timeout", c.Server.WriteTimeout)
	validateNonNegative(add, "server.idle_timeout", c.Server.IdleTimeout)
	validatePositive(add, "server.shutdown_timeout", c.Server.ShutdownTimeout, time.Second)
	if c.Server.MaxHeaderBytes < 4<<10 || c.Server.MaxHeaderBytes > 1<<20 {
		add("server.max_header_bytes: must be between %d and %d", 4<<10, 1<<20)
	}

	if _, err := ParseTrustedCIDRs(c.Proxy.TrustedCIDRs); err != nil {
		add("proxy.trusted_cidrs: %v", err)
	}
	headers := []struct {
		key   string
		value string
	}{
		{"verify_header", c.Proxy.VerifyHeader},
		{"fingerprint_header", c.Proxy.FingerprintHeader},
		{"dn_header", c.Proxy.DNHeader},
	}
	seenHeaders := make(map[string]string, len(headers))
	for _, header := range headers {
		if !ValidHeaderName(header.value) {
			add("proxy.%s: %q is not a valid HTTP header name", header.key, header.value)
			continue
		}
		if IsReservedHeader(header.value) {
			add("proxy.%s: %q must not be used as an identity header", header.key, header.value)
		}
		lower := strings.ToLower(header.value)
		if previous, ok := seenHeaders[lower]; ok {
			add("proxy.%s: duplicates proxy.%s (%q)", header.key, previous, header.value)
		}
		seenHeaders[lower] = header.key
	}
	verifyValue := strings.TrimSpace(c.Proxy.RequiredVerifyValue)
	if verifyValue == "" || len(verifyValue) > 64 || strings.ContainsFunc(verifyValue, isControl) {
		add("proxy.required_verify_value: must be a non-empty printable value of at most 64 bytes")
	}

	if c.Auth.DeviceIDSource != DeviceIDSourceFingerprint && c.Auth.DeviceIDSource != DeviceIDSourceCommonName {
		add("auth.device_id_source: must be %q or %q", DeviceIDSourceFingerprint, DeviceIDSourceCommonName)
	}
	if c.Auth.MaxHeaderValueBytes < 64 || c.Auth.MaxHeaderValueBytes > 16<<10 {
		add("auth.max_header_value_bytes: must be between %d and %d", 64, 16<<10)
	}
	if c.Auth.MaxCommonNameLength < 1 || c.Auth.MaxCommonNameLength > 512 {
		add("auth.max_common_name_length: must be between 1 and 512")
	}
	if c.Auth.MaxDistinguishedNameBytes < 64 || c.Auth.MaxDistinguishedNameBytes > 16<<10 {
		add("auth.max_dn_bytes: must be between %d and %d", 64, 16<<10)
	}

	offlineAfter := c.Registry.OfflineAfter.Std()
	if offlineAfter < 10*time.Second || offlineAfter > time.Hour {
		add("registry.offline_after: must be between 10s and 1h")
	}
	sweepInterval := c.Registry.SweepInterval.Std()
	if sweepInterval < time.Second || sweepInterval > offlineAfter {
		add("registry.sweep_interval: must be between 1s and registry.offline_after")
	}
	if c.Registry.MaxDevices < 1 || c.Registry.MaxDevices > 1_000_000 {
		add("registry.max_devices: must be between 1 and 1000000")
	}
	if c.Registry.MaxEndpointsPerDevice < 1 || c.Registry.MaxEndpointsPerDevice > 64 {
		add("registry.max_endpoints_per_device: must be between 1 and 64")
	}

	if strings.TrimSpace(c.Storage.DataDir) == "" {
		add("storage.data_dir: must not be empty")
	}
	if c.Storage.MaxItemBytes < 1<<10 || c.Storage.MaxItemBytes > 1<<30 {
		add("storage.max_item_bytes: must be between %d and %d", 1<<10, 1<<30)
	}

	if c.WebSocket.ReadLimitBytes < 1024 || c.WebSocket.ReadLimitBytes > 1<<20 {
		add("websocket.read_limit_bytes: must be between %d and %d", 1024, 1<<20)
	}
	validateRange(add, "websocket.handshake_timeout", c.WebSocket.HandshakeTimeout, time.Second, 30*time.Second)
	validateRange(add, "websocket.hello_timeout", c.WebSocket.HelloTimeout, time.Second, 60*time.Second)
	validateRange(add, "websocket.ping_interval", c.WebSocket.PingInterval, 5*time.Second, 5*time.Minute)
	validateRange(add, "websocket.pong_timeout", c.WebSocket.PongTimeout, 5*time.Second, 10*time.Minute)
	validateRange(add, "websocket.write_timeout", c.WebSocket.WriteTimeout, time.Second, time.Minute)
	validateRange(add, "websocket.heartbeat_interval", c.WebSocket.HeartbeatInterval, time.Second, 5*time.Minute)
	if c.WebSocket.PongTimeout.Std() <= c.WebSocket.PingInterval.Std() {
		add("websocket.pong_timeout: must be greater than websocket.ping_interval")
	}
	if c.WebSocket.HeartbeatInterval.Std() >= c.WebSocket.PongTimeout.Std() {
		add("websocket.heartbeat_interval: must be smaller than websocket.pong_timeout")
	}
	if c.WebSocket.HeartbeatInterval.Std()*2 > offlineAfter {
		add("websocket.heartbeat_interval: must be at most half of registry.offline_after")
	}
	if c.WebSocket.SendBuffer < 8 || c.WebSocket.SendBuffer > 4096 {
		add("websocket.send_buffer: must be between 8 and 4096")
	}
	if c.WebSocket.MaxMessagesPerSecond < 1 || c.WebSocket.MaxMessagesPerSecond > 1000 {
		add("websocket.max_messages_per_second: must be between 1 and 1000")
	}
	for _, origin := range c.WebSocket.AllowedOrigins {
		if _, err := NormalizeOrigin(origin); err != nil {
			add("websocket.allowed_origins: %v", err)
		}
	}

	validateRange(add, "telemetry.timeout", c.Telemetry.Timeout, time.Second, time.Minute)
	if c.Telemetry.Active() {
		parsed, err := url.Parse(strings.TrimSpace(c.Telemetry.Endpoint))
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			add("telemetry.endpoint: must be an absolute https URL")
		}
		if parsed != nil && (parsed.User != nil || parsed.Fragment != "") {
			add("telemetry.endpoint: must not contain credentials or a fragment")
		}
	}

	validateRange(add, "update_check.interval", c.UpdateCheck.Interval, time.Hour, 7*24*time.Hour)
	validateRange(add, "update_check.timeout", c.UpdateCheck.Timeout, time.Second, time.Minute)
	if c.UpdateCheck.Enabled {
		if !validRepository(c.UpdateCheck.Repository) {
			add("update_check.repository: must look like \"owner/name\"")
		}
		parsed, err := url.Parse(strings.TrimSpace(c.UpdateCheck.APIBase))
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			add("update_check.api_base: must be an absolute https URL")
		}
	}

	switch strings.ToLower(c.Log.Level) {
	case "debug", "info", "warn", "error":
	default:
		add("log.level: must be one of debug, info, warn, error")
	}
	switch strings.ToLower(c.Log.Format) {
	case "json", "text":
	default:
		add("log.format: must be one of json, text")
	}

	if len(problems) > 0 {
		return fmt.Errorf("invalid configuration: %s", strings.Join(problems, "; "))
	}
	return nil
}

// ParseTrustedCIDRs parses the trusted proxy list and rejects prefixes that
// cover the whole address space.
func ParseTrustedCIDRs(values []string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", raw, err)
		}
		prefix = prefix.Masked()
		if prefix.Bits() == 0 {
			return nil, fmt.Errorf("CIDR %q covers the whole address space and cannot be trusted", raw)
		}
		// An IPv4-mapped prefix such as ::ffff:0:0/96 normalizes to 0.0.0.0/0
		// and would trust every IPv4 peer.
		if prefix.Addr().Is4In6() && prefix.Bits() <= 96 {
			return nil, fmt.Errorf("CIDR %q covers the whole IPv4 address space and cannot be trusted", raw)
		}
		prefixes = append(prefixes, prefix)
	}
	if len(prefixes) == 0 {
		return nil, errors.New("at least one trusted proxy CIDR is required")
	}
	return prefixes, nil
}

// NormalizeOrigin validates an Origin header allow-list entry and returns its
// canonical form (lowercase scheme and host, no trailing slash).
func NormalizeOrigin(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("origin must not be empty")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("invalid origin %q: %w", raw, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("origin %q must use http or https", raw)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("origin %q has no host", raw)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("origin %q must not contain credentials, a query or a fragment", raw)
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("origin %q must not contain a path", raw)
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host), nil
}

// ValidHeaderName reports whether value is an RFC 7230 token.
func ValidHeaderName(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c)):
		default:
			return false
		}
	}
	return true
}

// IsReservedHeader reports whether a header is managed by the HTTP stack or
// carries credentials that must never be trusted as a device identity.
func IsReservedHeader(value string) bool {
	switch strings.ToLower(value) {
	case "host", "content-length", "transfer-encoding", "connection", "upgrade",
		"te", "trailer", "proxy-authorization", "proxy-authenticate",
		"authorization", "cookie", "set-cookie",
		"sec-websocket-key", "sec-websocket-version", "sec-websocket-extensions",
		"x-forwarded-for", "x-forwarded-proto", "x-real-ip":
		return true
	default:
		return false
	}
}

func validateListenAddress(value string) error {
	host, port, err := net.SplitHostPort(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("invalid listen address %q: %w", value, err)
	}
	if host != "" {
		if _, err := netip.ParseAddr(host); err != nil {
			return fmt.Errorf("invalid listen host %q", host)
		}
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return fmt.Errorf("invalid listen port %q", port)
	}
	return nil
}

func validatePositive(add func(string, ...any), name string, value Duration, minimum time.Duration) {
	if value.Std() < minimum {
		add("%s: must be at least %s", name, minimum)
	}
}

func validateNonNegative(add func(string, ...any), name string, value Duration) {
	if value.Std() < 0 {
		add("%s: must not be negative", name)
	}
}

func validateRange(add func(string, ...any), name string, value Duration, minimum, maximum time.Duration) {
	if value.Std() < minimum || value.Std() > maximum {
		add("%s: must be between %s and %s", name, minimum, maximum)
	}
}

// validRepository accepts the "owner/name" form used by the GitHub API.
func validRepository(repository string) bool {
	parts := strings.Split(strings.TrimSpace(repository), "/")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		if part == "" || len(part) > 100 {
			return false
		}
		for _, character := range part {
			switch {
			case character >= 'a' && character <= 'z':
			case character >= 'A' && character <= 'Z':
			case character >= '0' && character <= '9':
			case character == '-', character == '_', character == '.':
			default:
				return false
			}
		}
	}
	return true
}

func isControl(r rune) bool {
	return r < 0x20 || r == 0x7f
}
