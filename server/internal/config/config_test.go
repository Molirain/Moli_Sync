package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestDefaultConfigIsValid(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Listen != "127.0.0.1:9520" {
		t.Fatalf("unexpected default listen address %q", cfg.Server.Listen)
	}
	if cfg.Auth.DeviceIDSource != DeviceIDSourceFingerprint {
		t.Fatalf("unexpected device id source %q", cfg.Auth.DeviceIDSource)
	}
	if len(cfg.Proxy.TrustedCIDRs) != 2 {
		t.Fatalf("expected loopback defaults, got %v", cfg.Proxy.TrustedCIDRs)
	}
}

func TestYAMLOverridesDefaults(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: "0.0.0.0:9600"
  shutdown_timeout: 5s
proxy:
  trusted_cidrs:
    - "10.0.0.0/8"
websocket:
  allowed_origins:
    - "https://app.example.com/"
log:
  level: "debug"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Listen != "0.0.0.0:9600" {
		t.Fatalf("listen override not applied: %q", cfg.Server.Listen)
	}
	if cfg.Server.ShutdownTimeout.Std() != 5*time.Second {
		t.Fatalf("duration override not applied: %s", cfg.Server.ShutdownTimeout)
	}
	if len(cfg.Proxy.TrustedCIDRs) != 1 || cfg.Proxy.TrustedCIDRs[0] != "10.0.0.0/8" {
		t.Fatalf("trusted cidrs not replaced: %v", cfg.Proxy.TrustedCIDRs)
	}
	if cfg.Proxy.VerifyHeader != "X-Client-Verify" {
		t.Fatalf("unspecified header lost its default: %q", cfg.Proxy.VerifyHeader)
	}
	if cfg.Log.Level != "debug" {
		t.Fatalf("log level override not applied: %q", cfg.Log.Level)
	}
}

func TestEnvironmentOverridesYAML(t *testing.T) {
	path := writeConfig(t, "server:\n  listen: \"127.0.0.1:9000\"\n")
	t.Setenv("MOLISYNC_SERVER_LISTEN", "127.0.0.1:9100")
	t.Setenv("MOLISYNC_PROXY_TRUSTED_CIDRS", "127.0.0.1/32, 192.168.0.0/16")
	t.Setenv("MOLISYNC_AUTH_DEVICE_ID_SOURCE", "dn_cn")
	t.Setenv("MOLISYNC_WEBSOCKET_PONG_TIMEOUT", "90s")
	t.Setenv("MOLISYNC_LOG_FORMAT", "text")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Listen != "127.0.0.1:9100" {
		t.Fatalf("environment did not win: %q", cfg.Server.Listen)
	}
	if len(cfg.Proxy.TrustedCIDRs) != 2 || cfg.Proxy.TrustedCIDRs[1] != "192.168.0.0/16" {
		t.Fatalf("comma separated environment slice not applied: %v", cfg.Proxy.TrustedCIDRs)
	}
	if cfg.Auth.DeviceIDSource != DeviceIDSourceCommonName {
		t.Fatalf("device id source override not applied: %q", cfg.Auth.DeviceIDSource)
	}
	if cfg.WebSocket.PongTimeout.Std() != 90*time.Second {
		t.Fatalf("duration environment override not applied: %s", cfg.WebSocket.PongTimeout)
	}
	if cfg.Log.Format != "text" {
		t.Fatalf("log format override not applied: %q", cfg.Log.Format)
	}
}

func TestUnknownYAMLFieldRejected(t *testing.T) {
	path := writeConfig(t, "server:\n  listne: \"127.0.0.1:9520\"\n")
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "listne") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestMultipleYAMLDocumentsRejected(t *testing.T) {
	path := writeConfig(t, "server:\n  listen: \"127.0.0.1:9520\"\n---\nlog:\n  level: debug\n")
	if _, err := Load(path); err == nil {
		t.Fatal("expected multiple document error")
	}
}

func TestInvalidDurationRejected(t *testing.T) {
	path := writeConfig(t, "server:\n  read_header_timeout: ten\n")
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "duration") {
		t.Fatalf("expected duration error, got %v", err)
	}
}

func TestCatchAllTrustedCIDRRejected(t *testing.T) {
	for _, prefix := range []string{"0.0.0.0/0", "::/0", "::ffff:0:0/96"} {
		path := writeConfig(t, "proxy:\n  trusted_cidrs: [\""+prefix+"\"]\n")
		if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "address space") {
			t.Fatalf("expected catch-all rejection for %s, got %v", prefix, err)
		}
	}
}

func TestEmptyTrustedCIDRsRejected(t *testing.T) {
	path := writeConfig(t, "proxy:\n  trusted_cidrs: []\n")
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "at least one trusted proxy CIDR") {
		t.Fatalf("expected empty trusted proxy rejection, got %v", err)
	}
}

func TestInvalidSettingsAreReportedTogether(t *testing.T) {
	path := writeConfig(t, `
auth:
  device_id_source: "cn"
log:
  level: "verbose"
websocket:
  allowed_origins: ["ftp://example.com"]
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error")
	}
	message := err.Error()
	for _, fragment := range []string{"auth.device_id_source", "log.level", "websocket.allowed_origins"} {
		if !strings.Contains(message, fragment) {
			t.Fatalf("error does not mention %q: %v", fragment, err)
		}
	}
}

func TestDuplicateIdentityHeadersRejected(t *testing.T) {
	path := writeConfig(t, `
proxy:
  verify_header: "X-Client-Verify"
  fingerprint_header: "x-client-verify"
`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "duplicates") {
		t.Fatalf("expected duplicate header error, got %v", err)
	}
}

func TestNormalizeOrigin(t *testing.T) {
	valid := map[string]string{
		"https://app.example.com":  "https://app.example.com",
		"HTTPS://APP.EXAMPLE.COM/": "https://app.example.com",
		"http://localhost:3000":    "http://localhost:3000",
	}
	for input, expected := range valid {
		actual, err := NormalizeOrigin(input)
		if err != nil {
			t.Fatalf("NormalizeOrigin(%q): %v", input, err)
		}
		if actual != expected {
			t.Fatalf("NormalizeOrigin(%q) = %q, want %q", input, actual, expected)
		}
	}
	for _, input := range []string{"", "ftp://example.com", "https://example.com/path", "https://example.com?x=1", "https://user@example.com"} {
		if _, err := NormalizeOrigin(input); err == nil {
			t.Fatalf("expected %q to be rejected", input)
		}
	}
}
