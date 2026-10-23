package auth

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

// Options configures how forwarded certificate metadata is interpreted. The
// header names must match the ones configured in the reverse proxy.
type Options struct {
	VerifyHeader              string
	FingerprintHeader         string
	DNHeader                  string
	RequiredVerifyValue       string
	DeviceIDSource            string
	MaxHeaderValueBytes       int
	MaxCommonNameLength       int
	MaxDistinguishedNameBytes int
	// CertHeader names a header that carries the whole client certificate in
	// PEM form. It is only consulted when the fingerprint header is empty, and
	// exists because nginx cannot compute a SHA-256 digest on its own
	// ($ssl_client_fingerprint is SHA-1). Empty disables the feature.
	CertHeader string
	// ClientCAPool, when non-nil, must sign a certificate taken from CertHeader.
	ClientCAPool *x509.CertPool
	// Now overrides the clock for certificate validity checks in tests.
	Now func() time.Time
}

// Error is an authentication failure that maps to a specific HTTP status and
// machine readable code. Its message is safe to return to the client.
type Error struct {
	Status  int
	Code    string
	Message string
}

// Error implements error.
func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Middleware authenticates requests that arrive from the trusted reverse proxy.
type Middleware struct {
	options  Options
	prefixes []netip.Prefix
	logger   *slog.Logger
	now      func() time.Time
}

// NewMiddleware validates the options and compiles the trusted proxy ranges.
func NewMiddleware(options Options, trusted []netip.Prefix, logger *slog.Logger) (*Middleware, error) {
	if len(trusted) == 0 {
		return nil, errors.New("auth: at least one trusted proxy prefix is required")
	}
	if !isToken(options.VerifyHeader) || !isToken(options.FingerprintHeader) ||
		!isToken(options.DNHeader) {
		return nil, errors.New("auth: identity header names must be valid HTTP tokens")
	}
	if options.DeviceIDSource != "fingerprint" && options.DeviceIDSource != "dn_cn" {
		return nil, fmt.Errorf("auth: unsupported device id source %q", options.DeviceIDSource)
	}
	if options.MaxHeaderValueBytes <= 0 || options.MaxCommonNameLength <= 0 || options.MaxDistinguishedNameBytes <= 0 {
		return nil, errors.New("auth: header and CN limits must be positive")
	}
	if logger == nil {
		logger = slog.Default()
	}

	prefixes := make([]netip.Prefix, 0, len(trusted))
	for _, prefix := range trusted {
		if !prefix.IsValid() {
			return nil, errors.New("auth: trusted proxy prefix is invalid")
		}
		normalized := normalizePrefix(prefix)
		if normalized.Bits() == 0 {
			return nil, errors.New("auth: a trusted proxy range must not cover the whole address space")
		}
		prefixes = append(prefixes, normalized)
	}
	return &Middleware{
		options:  options,
		prefixes: prefixes,
		logger:   logger,
		now:      time.Now,
	}, nil
}

// RequireDevice authenticates the request and rejects it unless a verified
// client certificate identity can be derived. On success the Identity is made
// available through FromContext.
func (m *Middleware) RequireDevice(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, err := m.Authenticate(r)
		if err != nil {
			authErr := &Error{Status: http.StatusUnauthorized, Code: "unauthorized", Message: "authentication failed"}
			if !errors.As(err, &authErr) {
				authErr = &Error{Status: http.StatusInternalServerError, Code: "internal_error", Message: "authentication failed"}
			}
			m.logger.Warn("request rejected",
				"code", authErr.Code,
				"status", authErr.Status,
				"remote", r.RemoteAddr,
				"path", r.URL.Path,
			)
			writeError(w, authErr)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), identity)))
	})
}

// Authenticate verifies that the request originates from the trusted proxy and
// derives the device identity from the forwarded certificate metadata.
func (m *Middleware) Authenticate(r *http.Request) (Identity, error) {
	proxyIP, err := remoteIP(r.RemoteAddr)
	if err != nil {
		return Identity{}, &Error{
			Status:  http.StatusInternalServerError,
			Code:    "internal_error",
			Message: "cannot determine the peer address",
		}
	}
	if !m.isTrusted(proxyIP) {
		return Identity{}, &Error{
			Status:  http.StatusForbidden,
			Code:    "untrusted_proxy",
			Message: "request did not originate from the trusted reverse proxy",
		}
	}

	verify, err := m.readHeader(r, m.options.VerifyHeader, m.options.MaxHeaderValueBytes)
	if err != nil {
		return Identity{}, err
	}
	if verify == "" || !strings.EqualFold(verify, m.options.RequiredVerifyValue) {
		return Identity{}, &Error{
			Status:  http.StatusUnauthorized,
			Code:    "client_certificate_unverified",
			Message: "client certificate was not verified by the reverse proxy",
		}
	}

	rawFingerprint, err := m.readHeader(r, m.options.FingerprintHeader, m.options.MaxHeaderValueBytes)
	if err != nil {
		return Identity{}, err
	}
	var fingerprint string
	if rawFingerprint != "" {
		fingerprint, err = NormalizeFingerprint(rawFingerprint)
		if err != nil {
			return Identity{}, &Error{
				Status:  http.StatusUnauthorized,
				Code:    "invalid_client_fingerprint",
				Message: "client certificate fingerprint is not a SHA-256 digest",
			}
		}
	}
	if fingerprint == "" {
		fingerprint, err = m.fingerprintFromCertHeader(r)
		if err != nil {
			return Identity{}, err
		}
	}

	rawDN, err := m.readHeader(r, m.options.DNHeader, m.options.MaxDistinguishedNameBytes)
	if err != nil {
		return Identity{}, err
	}
	var commonName string
	if rawDN != "" {
		commonName, err = ParseCommonName(rawDN, m.options.MaxCommonNameLength)
		if err != nil {
			return Identity{}, &Error{
				Status:  http.StatusUnauthorized,
				Code:    "invalid_client_dn",
				Message: "client certificate subject is missing a valid common name",
			}
		}
	}

	var deviceID string
	switch m.options.DeviceIDSource {
	case "dn_cn":
		if commonName == "" {
			return Identity{}, &Error{
				Status:  http.StatusUnauthorized,
				Code:    "missing_client_dn",
				Message: "client certificate subject does not contain a common name",
			}
		}
		deviceID = DeviceIDPrefixCommonName + commonName
	default:
		if fingerprint == "" {
			return Identity{}, &Error{
				Status:  http.StatusUnauthorized,
				Code:    "missing_client_fingerprint",
				Message: "client certificate fingerprint was not forwarded",
			}
		}
		deviceID = fingerprint
	}

	return Identity{
		DeviceID:        deviceID,
		CommonName:      commonName,
		Fingerprint:     fingerprint,
		ProxyIP:         proxyIP,
		AuthenticatedAt: m.now().UTC(),
	}, nil
}

// fingerprintFromCertHeader derives the SHA-256 digest from a forwarded
// certificate. It returns an empty string when the feature is disabled or the
// header is absent, so callers can keep the usual "missing fingerprint" error.
func (m *Middleware) fingerprintFromCertHeader(r *http.Request) (string, error) {
	if m.options.CertHeader == "" {
		return "", nil
	}
	raw, err := m.readHeader(r, m.options.CertHeader, maxCertificateHeaderBytes)
	if err != nil {
		return "", err
	}
	if raw == "" {
		return "", nil
	}

	certificate, parseErr := ParseClientCertificate(raw)
	if parseErr != nil {
		return "", &Error{
			Status:  http.StatusUnauthorized,
			Code:    "invalid_client_certificate",
			Message: "forwarded client certificate could not be parsed",
		}
	}
	clock := m.now
	if m.options.Now != nil {
		clock = m.options.Now
	}
	if verifyErr := VerifyClientCertificate(certificate, m.options.ClientCAPool, clock().UTC()); verifyErr != nil {
		m.logger.Warn("forwarded client certificate rejected", "error", verifyErr)
		return "", &Error{
			Status:  http.StatusUnauthorized,
			Code:    "untrusted_client_certificate",
			Message: "forwarded client certificate is not trusted",
		}
	}
	return FingerprintOf(certificate), nil
}

func (m *Middleware) readHeader(r *http.Request, name string, limit int) (string, error) {
	values := r.Header.Values(name)
	switch len(values) {
	case 0:
		return "", nil
	case 1:
	default:
		return "", &Error{
			Status:  http.StatusBadRequest,
			Code:    "malformed_authentication_headers",
			Message: "authentication headers must not be repeated",
		}
	}
	value := strings.TrimSpace(values[0])
	if len(value) > limit {
		return "", &Error{
			Status:  http.StatusBadRequest,
			Code:    "authentication_header_too_large",
			Message: "authentication header exceeds the configured limit",
		}
	}
	return value, nil
}

func (m *Middleware) isTrusted(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, prefix := range m.prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func remoteIP(remoteAddr string) (netip.Addr, error) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	host = strings.Trim(host, "[]")
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, err
	}
	if addr.Zone() != "" {
		return netip.Addr{}, errors.New("scoped addresses are not supported")
	}
	return addr.Unmap(), nil
}

func normalizePrefix(prefix netip.Prefix) netip.Prefix {
	addr := prefix.Addr()
	if addr.Is4In6() && prefix.Bits() >= 96 {
		return netip.PrefixFrom(addr.Unmap(), prefix.Bits()-96).Masked()
	}
	return prefix.Masked()
}

func isToken(value string) bool {
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

func writeError(w http.ResponseWriter, authErr *Error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(authErr.Status)
	body := struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{}
	body.Error.Code = authErr.Code
	body.Error.Message = authErr.Message
	_ = json.NewEncoder(w).Encode(body)
}
