package auth

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testOptions() Options {
	return Options{
		VerifyHeader:              "X-Client-Verify",
		FingerprintHeader:         "X-Client-Fingerprint",
		DNHeader:                  "X-Client-DN",
		RequiredVerifyValue:       "SUCCESS",
		DeviceIDSource:            "fingerprint",
		MaxHeaderValueBytes:       1024,
		MaxCommonNameLength:       128,
		MaxDistinguishedNameBytes: 1024,
	}
}

func newTestMiddleware(t *testing.T, options Options, prefixes ...string) *Middleware {
	t.Helper()
	trusted := make([]netip.Prefix, 0, len(prefixes))
	for _, value := range prefixes {
		trusted = append(trusted, netip.MustParsePrefix(value))
	}
	middleware, err := NewMiddleware(options, trusted, discardLogger())
	if err != nil {
		t.Fatalf("NewMiddleware: %v", err)
	}
	return middleware
}

func TestNormalizeFingerprint(t *testing.T) {
	plain := strings.Repeat("ab", 32)
	upper := strings.ToUpper(plain)

	var builder strings.Builder
	for i := 0; i < len(upper); i += 2 {
		builder.WriteString(upper[i : i+2])
		builder.WriteByte(':')
	}
	withColons := strings.TrimSuffix(builder.String(), ":")

	for _, input := range []string{plain, upper, withColons} {
		normalized, err := NormalizeFingerprint(input)
		if err != nil {
			t.Fatalf("NormalizeFingerprint(%q): %v", input, err)
		}
		if normalized != plain {
			t.Fatalf("NormalizeFingerprint(%q) = %q, want %q", input, normalized, plain)
		}
	}

	for _, input := range []string{"", "abc", strings.Repeat("zz", 32), strings.Repeat("a", 63) + " ", plain + "0"} {
		if _, err := NormalizeFingerprint(input); err == nil {
			t.Fatalf("expected %q to be rejected", input)
		}
	}
}

func TestParseCommonName(t *testing.T) {
	tests := []struct {
		name  string
		dn    string
		want  string
		error bool
	}{
		{name: "plain", dn: "CN=phone-a", want: "phone-a"},
		{name: "full", dn: "CN=phone-a,O=Molirain,C=CN", want: "phone-a"},
		{name: "cn at end", dn: "O=Molirain,C=CN,CN=phone-a", want: "phone-a"},
		{name: "escaped comma", dn: `CN=Phone\, A,O=Molirain`, want: "Phone, A"},
		{name: "hex escape", dn: `CN=Phone\20A`, want: "Phone A"},
		{name: "quoted", dn: `CN="Phone A",O=Molirain`, want: "Phone A"},
		{name: "multi valued rdn", dn: "O=Molirain+CN=phone-a", want: "phone-a"},
		{name: "slash legacy", dn: "/C=CN/O=Molirain/CN=phone-a", want: "phone-a"},
		{name: "first cn wins", dn: "CN=first,CN=second", want: "first"},
		{name: "utf8", dn: "CN=手机 A,O=Molirain", want: "手机 A"},
		{name: "missing cn", dn: "O=Molirain,C=CN", error: true},
		{name: "empty", dn: "", error: true},
		{name: "attribute without equals", dn: "CNphone-a", error: true},
		{name: "empty cn", dn: "CN=,O=Molirain", error: true},
		{name: "trailing escape", dn: `CN=phone\`, error: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseCommonName(test.dn, 128)
			if test.error {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCommonName: %v", err)
			}
			if got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestParseCommonNameLengthLimit(t *testing.T) {
	if _, err := ParseCommonName("CN="+strings.Repeat("a", 129), 128); err == nil {
		t.Fatal("expected length error")
	}
}

func TestRequireDeviceInjectsIdentity(t *testing.T) {
	middleware := newTestMiddleware(t, testOptions(), "127.0.0.0/8")
	fingerprint := strings.Repeat("cd", 32)

	var captured Identity
	handler := middleware.RequireDevice(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := FromContext(r.Context())
		if !ok {
			t.Fatal("identity missing from context")
		}
		captured = identity
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
	request.RemoteAddr = "127.0.0.1:54321"
	request.Header.Set("X-Client-Verify", "SUCCESS")
	request.Header.Set("X-Client-Fingerprint", fingerprint)
	request.Header.Set("X-Client-DN", "CN=phone-a,O=Molirain")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if captured.DeviceID != fingerprint {
		t.Fatalf("device id = %q, want %q", captured.DeviceID, fingerprint)
	}
	if captured.CommonName != "phone-a" {
		t.Fatalf("common name = %q", captured.CommonName)
	}
	if captured.ProxyIP.String() != "127.0.0.1" {
		t.Fatalf("proxy ip = %q", captured.ProxyIP)
	}
}

func TestRequireDeviceRejections(t *testing.T) {
	fingerprint := strings.Repeat("ab", 32)
	failing := map[string]struct {
		setup     func(*http.Request)
		remote    string
		options   Options
		status    int
		errorCode string
	}{
		"untrusted peer": {
			setup:     func(r *http.Request) {},
			remote:    "203.0.113.7:4444",
			status:    http.StatusForbidden,
			errorCode: "untrusted_proxy",
		},
		"missing verify": {
			setup: func(r *http.Request) {
				r.Header.Set("X-Client-Fingerprint", fingerprint)
			},
			status:    http.StatusUnauthorized,
			errorCode: "client_certificate_unverified",
		},
		"verify failed": {
			setup: func(r *http.Request) {
				r.Header.Set("X-Client-Verify", "FAILED")
				r.Header.Set("X-Client-Fingerprint", fingerprint)
			},
			status:    http.StatusUnauthorized,
			errorCode: "client_certificate_unverified",
		},
		"missing fingerprint": {
			setup: func(r *http.Request) {
				r.Header.Set("X-Client-Verify", "SUCCESS")
			},
			status:    http.StatusUnauthorized,
			errorCode: "missing_client_fingerprint",
		},
		"invalid fingerprint": {
			setup: func(r *http.Request) {
				r.Header.Set("X-Client-Verify", "SUCCESS")
				r.Header.Set("X-Client-Fingerprint", "not-a-fingerprint")
			},
			status:    http.StatusUnauthorized,
			errorCode: "invalid_client_fingerprint",
		},
		"invalid dn": {
			setup: func(r *http.Request) {
				r.Header.Set("X-Client-Verify", "SUCCESS")
				r.Header.Set("X-Client-Fingerprint", fingerprint)
				r.Header.Set("X-Client-DN", "O=Molirain")
			},
			status:    http.StatusUnauthorized,
			errorCode: "invalid_client_dn",
		},
		"repeated header": {
			setup: func(r *http.Request) {
				r.Header.Set("X-Client-Verify", "SUCCESS")
				r.Header.Add("X-Client-Fingerprint", fingerprint)
				r.Header.Add("X-Client-Fingerprint", fingerprint)
			},
			status:    http.StatusBadRequest,
			errorCode: "malformed_authentication_headers",
		},
		"dn source without dn": {
			setup: func(r *http.Request) {
				r.Header.Set("X-Client-Verify", "SUCCESS")
				r.Header.Set("X-Client-Fingerprint", fingerprint)
			},
			options: func() Options {
				options := testOptions()
				options.DeviceIDSource = "dn_cn"
				return options
			}(),
			status:    http.StatusUnauthorized,
			errorCode: "missing_client_dn",
		},
	}

	for name, test := range failing {
		t.Run(name, func(t *testing.T) {
			options := test.options
			if options.VerifyHeader == "" {
				options = testOptions()
			}
			middleware := newTestMiddleware(t, options, "127.0.0.0/8")
			called := false
			handler := middleware.RequireDevice(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				called = true
			}))

			request := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
			request.RemoteAddr = "127.0.0.1:1000"
			if test.remote != "" {
				request.RemoteAddr = test.remote
			}
			test.setup(request)

			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			if called {
				t.Fatal("protected handler was called")
			}
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d (body %s)", recorder.Code, test.status, recorder.Body.String())
			}
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.Error.Code != test.errorCode {
				t.Fatalf("error code = %q, want %q", body.Error.Code, test.errorCode)
			}
		})
	}
}

func TestRequireDeviceWithCommonNameSource(t *testing.T) {
	options := testOptions()
	options.DeviceIDSource = "dn_cn"
	middleware := newTestMiddleware(t, options, "127.0.0.0/8")

	var captured Identity
	handler := middleware.RequireDevice(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = FromContext(r.Context())
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "127.0.0.1:1000"
	request.Header.Set("X-Client-Verify", "SUCCESS")
	request.Header.Set("X-Client-DN", "CN=pc-c,O=Molirain")
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if captured.DeviceID != "cn:pc-c" {
		t.Fatalf("device id = %q", captured.DeviceID)
	}
}

func TestIPv4MappedTrustedProxy(t *testing.T) {
	middleware := newTestMiddleware(t, testOptions(), "127.0.0.0/8")
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "[::ffff:127.0.0.1]:1234"
	request.Header.Set("X-Client-Verify", "SUCCESS")
	request.Header.Set("X-Client-Fingerprint", strings.Repeat("ab", 32))
	if _, err := middleware.Authenticate(request); err != nil {
		t.Fatalf("mapped loopback should be trusted: %v", err)
	}
}

func TestCatchAllTrustedPrefixRejected(t *testing.T) {
	for _, value := range []string{"0.0.0.0/0", "::/0", "::ffff:0:0/96"} {
		prefix := netip.MustParsePrefix(value)
		if _, err := NewMiddleware(testOptions(), []netip.Prefix{prefix}, discardLogger()); err == nil {
			t.Fatalf("trusted prefix %s was accepted", value)
		}
	}
}
