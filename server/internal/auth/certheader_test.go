package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// testCA mints a CA and client certificates for the header tests.
type testCA struct {
	certificate *x509.Certificate
	key         *ecdsa.PrivateKey
	pem         string
}

func newTestCA(t *testing.T, name string) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA: %v", err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA: %v", err)
	}
	return &testCA{
		certificate: parsed,
		key:         key,
		pem:         string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
	}
}

// issue returns the PEM encoding of a client certificate signed by the CA.
func (ca *testCA) issue(t *testing.T, commonName string, serial int64) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.certificate, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("create client certificate: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func (ca *testCA) pool(t *testing.T) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(ca.pem)) {
		t.Fatal("append CA")
	}
	return pool
}

func clientCertificateRequest(certPEM string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
	request.RemoteAddr = "127.0.0.1:54321"
	request.Header.Set("X-Client-Verify", "SUCCESS")
	request.Header.Set("X-Client-DN", "CN=phone-a,O=Molirain")
	if certPEM != "" {
		request.Header.Set("X-Client-Cert", url.QueryEscape(certPEM))
	}
	return request
}

func TestAuthenticateDerivesFingerprintFromEscapedCertificate(t *testing.T) {
	ca := newTestCA(t, "MoliSync Test CA")
	certPEM := ca.issue(t, "phone-a", 2)

	options := testOptions()
	options.CertHeader = "X-Client-Cert"
	middleware := newTestMiddleware(t, options, "127.0.0.0/8")

	identity, err := middleware.Authenticate(clientCertificateRequest(certPEM))
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	want := FingerprintOf(mustParsePEM(t, certPEM))
	if identity.Fingerprint != want {
		t.Fatalf("fingerprint = %q, want %q", identity.Fingerprint, want)
	}
	if identity.DeviceID != want {
		t.Fatalf("device id = %q, want the fingerprint %q", identity.DeviceID, want)
	}
	if identity.CommonName != "phone-a" {
		t.Fatalf("common name = %q", identity.CommonName)
	}
}

func TestAuthenticateAcceptsTabbedClientCertificate(t *testing.T) {
	ca := newTestCA(t, "MoliSync Test CA")
	certPEM := ca.issue(t, "phone-b", 3)
	// $ssl_client_cert indents every continuation line with a tab.
	tabbed := "\t" + strings.ReplaceAll(strings.TrimSpace(certPEM), "\n", "\n\t")

	options := testOptions()
	options.CertHeader = "X-Client-Cert"
	middleware := newTestMiddleware(t, options, "127.0.0.0/8")

	identity, err := middleware.Authenticate(clientCertificateRequest(tabbed))
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if identity.Fingerprint == "" {
		t.Fatal("expected a certificate fingerprint")
	}
}

func TestAuthenticatePrefersTheFingerprintHeader(t *testing.T) {
	ca := newTestCA(t, "MoliSync Test CA")
	fingerprint := strings.Repeat("ab", 32)

	options := testOptions()
	options.CertHeader = "X-Client-Cert"
	middleware := newTestMiddleware(t, options, "127.0.0.0/8")

	request := clientCertificateRequest(ca.issue(t, "phone-a", 4))
	request.Header.Set("X-Client-Fingerprint", fingerprint)

	identity, err := middleware.Authenticate(request)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if identity.Fingerprint != fingerprint {
		t.Fatalf("fingerprint = %q, want the forwarded digest", identity.Fingerprint)
	}
}

func TestAuthenticateRejectsMalformedCertificateHeader(t *testing.T) {
	options := testOptions()
	options.CertHeader = "X-Client-Cert"
	middleware := newTestMiddleware(t, options, "127.0.0.0/8")

	request := clientCertificateRequest("")
	request.Header.Set("X-Client-Cert", url.QueryEscape("-----BEGIN CERTIFICATE-----\nnope\n-----END CERTIFICATE-----\n"))

	_, err := middleware.Authenticate(request)
	var authErr *Error
	if !asError(err, &authErr) || authErr.Code != "invalid_client_certificate" {
		t.Fatalf("expected invalid_client_certificate, got %v", err)
	}
}

func TestAuthenticateRequiresFingerprintWhenCertHeaderDisabled(t *testing.T) {
	ca := newTestCA(t, "MoliSync Test CA")
	middleware := newTestMiddleware(t, testOptions(), "127.0.0.0/8")

	_, err := middleware.Authenticate(clientCertificateRequest(ca.issue(t, "phone-a", 5)))
	var authErr *Error
	if !asError(err, &authErr) || authErr.Code != "missing_client_fingerprint" {
		t.Fatalf("expected missing_client_fingerprint, got %v", err)
	}
}

func TestAuthenticateVerifiesTheClientCA(t *testing.T) {
	trusted := newTestCA(t, "MoliSync Test CA")
	rogue := newTestCA(t, "Rogue CA")

	options := testOptions()
	options.CertHeader = "X-Client-Cert"
	options.ClientCAPool = trusted.pool(t)
	middleware := newTestMiddleware(t, options, "127.0.0.0/8")

	// A certificate from the trusted CA is accepted.
	if _, err := middleware.Authenticate(clientCertificateRequest(trusted.issue(t, "phone-a", 6))); err != nil {
		t.Fatalf("expected the trusted certificate to authenticate: %v", err)
	}

	// A certificate from another CA must be rejected even though it parses.
	_, err := middleware.Authenticate(clientCertificateRequest(rogue.issue(t, "phone-evil", 7)))
	var authErr *Error
	if !asError(err, &authErr) || authErr.Code != "untrusted_client_certificate" {
		t.Fatalf("expected untrusted_client_certificate, got %v", err)
	}
}

func TestAuthenticateWorksWithoutClientCA(t *testing.T) {
	// Without a configured CA the proxy is the only verifier, which matches
	// the trust model of the fingerprint header.
	ca := newTestCA(t, "MoliSync Test CA")
	options := testOptions()
	options.CertHeader = "X-Client-Cert"
	middleware := newTestMiddleware(t, options, "127.0.0.0/8")

	if _, err := middleware.Authenticate(clientCertificateRequest(ca.issue(t, "phone-a", 8))); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
}

func mustParsePEM(t *testing.T, value string) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode([]byte(value))
	if block == nil {
		t.Fatal("decode PEM")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return certificate
}

func asError(err error, target **Error) bool {
	if err == nil {
		return false
	}
	authErr, ok := err.(*Error)
	if ok {
		*target = authErr
	}
	return ok
}
