package auth

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

// ErrInvalidCertificate reports a forwarded certificate that cannot be parsed.
var ErrInvalidCertificate = errors.New("auth: forwarded client certificate is not a valid PEM certificate")

// ErrUntrustedCertificate reports a forwarded certificate that does not chain
// to the configured client CA.
var ErrUntrustedCertificate = errors.New("auth: forwarded client certificate is not issued by the client CA")

// LoadCertPool reads a PEM bundle of trusted client CA certificates.
func LoadCertPool(path string) (*x509.CertPool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("auth: read client CA file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, errors.New("auth: client CA file contains no PEM certificate")
	}
	return pool, nil
}

// ParseClientCertificate decodes the certificate a proxy forwards in a header.
//
// Both nginx variables are accepted: $ssl_client_escaped_cert (URL-encoded PEM,
// the only form that survives header folding) and $ssl_client_cert (PEM with a
// leading tab on every continuation line).
func ParseClientCertificate(raw string) (*x509.Certificate, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, ErrInvalidCertificate
	}

	candidates := []string{value}
	if decoded, err := url.QueryUnescape(value); err == nil && decoded != value {
		candidates = append([]string{decoded}, candidates...)
	}

	for _, candidate := range candidates {
		// $ssl_client_cert indents every line after the first with a tab.
		pemText := strings.ReplaceAll(candidate, "\t", "")
		block, _ := pem.Decode([]byte(pemText))
		if block == nil || block.Type != "CERTIFICATE" {
			continue
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		return certificate, nil
	}
	return nil, ErrInvalidCertificate
}

// FingerprintOf returns the lowercase SHA-256 digest of a certificate, which is
// exactly the value the server uses as a DeviceID and clients pin P2P peers to.
func FingerprintOf(certificate *x509.Certificate) string {
	sum := sha256.Sum256(certificate.Raw)
	return hex.EncodeToString(sum[:])
}

// VerifyClientCertificate checks that the certificate chains to roots and is
// allowed to authenticate clients.
func VerifyClientCertificate(certificate *x509.Certificate, roots *x509.CertPool, now time.Time) error {
	if roots == nil {
		return nil
	}
	intermediates := x509.NewCertPool()
	_, err := certificate.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUntrustedCertificate, err)
	}
	return nil
}
