// Package auth authenticates requests forwarded by the trusted reverse proxy.
//
// The proxy terminates mTLS, verifies the client certificate chain and its CRL,
// then forwards the certificate subject and SHA-256 fingerprint. This package
// verifies that the TCP peer is the trusted proxy and that the forwarded
// certificate metadata is well formed before deriving the immutable DeviceID.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// DeviceIDPrefixCommonName prefixes DeviceIDs derived from a certificate CN.
// Fingerprint based IDs never contain a colon, so the two namespaces cannot
// collide.
const DeviceIDPrefixCommonName = "cn:"

// fingerprintHexLength is the length of a SHA-256 digest in lowercase hex.
const fingerprintHexLength = 64

// maxCertificateHeaderBytes bounds a PEM certificate forwarded in a header.
// A typical leaf certificate is 1-2 KiB; the URL-encoded form is up to 3x that.
const maxCertificateHeaderBytes = 16 * 1024

var (
	// ErrMissingFingerprint is returned when no certificate fingerprint was forwarded.
	ErrMissingFingerprint = errors.New("missing client certificate fingerprint")
	// ErrInvalidFingerprint is returned when the forwarded fingerprint is not a SHA-256 digest.
	ErrInvalidFingerprint = errors.New("invalid client certificate fingerprint")
	// ErrMissingCommonName is returned when the subject contains no CN attribute.
	ErrMissingCommonName = errors.New("missing common name in client certificate subject")
	// ErrInvalidCommonName is returned when the CN is empty, too long or contains control characters.
	ErrInvalidCommonName = errors.New("invalid common name in client certificate subject")
	// ErrMalformedDistinguishedName is returned when the subject cannot be parsed as a DN.
	ErrMalformedDistinguishedName = errors.New("malformed client certificate subject")
)

// Identity is the authenticated device identity derived from the client
// certificate. It is immutable for the lifetime of a request.
type Identity struct {
	DeviceID        string     `json:"device_id"`
	CommonName      string     `json:"common_name,omitempty"`
	Fingerprint     string     `json:"fingerprint,omitempty"`
	ProxyIP         netip.Addr `json:"proxy_ip"`
	AuthenticatedAt time.Time  `json:"authenticated_at"`
}

type identityContextKey struct{}

// WithIdentity stores an identity in the context.
func WithIdentity(ctx context.Context, identity Identity) context.Context {
	return context.WithValue(ctx, identityContextKey{}, identity)
}

// FromContext returns the identity stored by the middleware.
func FromContext(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityContextKey{}).(Identity)
	return identity, ok
}

// NormalizeFingerprint validates and canonicalizes a SHA-256 certificate
// fingerprint. Separators are accepted so administrators can paste the format
// printed by OpenSSL, but the canonical DeviceID is lowercase hex.
func NormalizeFingerprint(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", ErrMissingFingerprint
	}
	if len(value) > fingerprintHexLength*3 {
		return "", ErrInvalidFingerprint
	}

	normalized := make([]byte, 0, fingerprintHexLength)
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == ':' {
			continue
		}
		switch {
		case c >= '0' && c <= '9':
			normalized = append(normalized, c)
		case c >= 'a' && c <= 'f':
			normalized = append(normalized, c)
		case c >= 'A' && c <= 'F':
			normalized = append(normalized, c+('a'-'A'))
		default:
			return "", ErrInvalidFingerprint
		}
	}
	if len(normalized) != fingerprintHexLength {
		return "", ErrInvalidFingerprint
	}
	return string(normalized), nil
}

// ParseCommonName extracts the CN attribute from a certificate subject in
// RFC 2253/4514 form (CN=phone-a,O=Molirain) or in the legacy OpenSSL slash
// form (/C=CN/O=Molirain/CN=phone-a). The most specific RDN is preferred,
// which matches the ordering mandated by RFC 4514.
func ParseCommonName(dn string, maxLength int) (string, error) {
	value := strings.TrimSpace(dn)
	if value == "" {
		return "", ErrMissingCommonName
	}

	var rdns []string
	if strings.HasPrefix(value, "/") {
		rdns = splitUnescaped(value[1:], '/')
	} else {
		rdns = splitUnescaped(value, ',')
	}

	for _, rdn := range rdns {
		for _, attribute := range splitUnescaped(rdn, '+') {
			index := indexUnescaped(attribute, '=')
			if index < 0 {
				return "", fmt.Errorf("%w: attribute without '='", ErrMalformedDistinguishedName)
			}
			if strings.TrimSpace(attribute[:index]) == "" {
				return "", fmt.Errorf("%w: empty attribute type", ErrMalformedDistinguishedName)
			}
			if !strings.EqualFold(strings.TrimSpace(attribute[:index]), "CN") {
				continue
			}
			commonName, err := decodeAttributeValue(attribute[index+1:])
			if err != nil {
				return "", err
			}
			commonName = strings.TrimSpace(commonName)
			if err := validateCommonName(commonName, maxLength); err != nil {
				return "", err
			}
			return commonName, nil
		}
	}
	return "", ErrMissingCommonName
}

func splitUnescaped(value string, separator rune) []string {
	parts := make([]string, 0, 4)
	var current strings.Builder
	escaped := false
	quoted := false
	for _, r := range value {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\':
			current.WriteRune(r)
			escaped = true
		case r == '"':
			quoted = !quoted
			current.WriteRune(r)
		case r == separator && !quoted:
			parts = append(parts, current.String())
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	parts = append(parts, current.String())
	return parts
}

func indexUnescaped(value string, target rune) int {
	escaped := false
	quoted := false
	for index, r := range value {
		switch {
		case escaped:
			escaped = false
		case r == '\\':
			escaped = true
		case r == '"':
			quoted = !quoted
		case r == target && !quoted:
			return index
		}
	}
	return -1
}

func decodeAttributeValue(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		value = value[1 : len(value)-1]
	}

	var decoded strings.Builder
	decoded.Grow(len(value))
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c != '\\' {
			decoded.WriteByte(c)
			continue
		}
		i++
		if i >= len(value) {
			return "", fmt.Errorf("%w: trailing escape", ErrMalformedDistinguishedName)
		}
		if i+1 < len(value) && isHexDigit(value[i]) && isHexDigit(value[i+1]) {
			number, err := strconv.ParseUint(value[i:i+2], 16, 8)
			if err != nil {
				return "", fmt.Errorf("%w: invalid hex escape", ErrMalformedDistinguishedName)
			}
			decoded.WriteByte(byte(number))
			i++
			continue
		}
		decoded.WriteByte(value[i])
	}

	result := decoded.String()
	if !utf8.ValidString(result) {
		return "", fmt.Errorf("%w: attribute value is not valid UTF-8", ErrMalformedDistinguishedName)
	}
	return result, nil
}

func validateCommonName(value string, maxLength int) error {
	if value == "" {
		return ErrInvalidCommonName
	}
	if utf8.RuneCountInString(value) > maxLength {
		return fmt.Errorf("%w: longer than %d characters", ErrInvalidCommonName, maxLength)
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return fmt.Errorf("%w: contains control or replacement characters", ErrInvalidCommonName)
		}
	}
	return nil
}

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
