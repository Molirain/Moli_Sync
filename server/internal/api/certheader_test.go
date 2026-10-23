package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/netip"
	"net/url"
	"testing"
	"time"

	"github.com/molirain/molisync/server/internal/auth"
)

// selfSignedClientCertificate returns a PEM certificate and its SHA-256 digest.
func selfSignedClientCertificate(t *testing.T, commonName string) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(11),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	sum := sha256.Sum256(der)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), hex.EncodeToString(sum[:])
}

// TestMeEndpointUsesForwardedCertificate covers the nginx deployment path: the
// proxy cannot produce a SHA-256 digest, so it forwards the certificate and the
// server derives the fingerprint itself.
func TestMeEndpointUsesForwardedCertificate(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	middleware, err := auth.NewMiddleware(auth.Options{
		VerifyHeader:              "X-Client-Verify",
		FingerprintHeader:         "X-Client-Fingerprint",
		DNHeader:                  "X-Client-DN",
		RequiredVerifyValue:       "SUCCESS",
		DeviceIDSource:            "dn_cn",
		MaxHeaderValueBytes:       1024,
		MaxCommonNameLength:       128,
		MaxDistinguishedNameBytes: 1024,
		CertHeader:                "X-Client-Cert",
	}, []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, logger)
	if err != nil {
		t.Fatalf("auth.NewMiddleware: %v", err)
	}

	certPEM, fingerprint := selfSignedClientCertificate(t, "phone-a")
	deps := testDeps(t)
	deps.Auth = middleware
	router := NewRouter(deps)

	recorder := performRequest(t, router, http.MethodGet, "/api/v1/me", "127.0.0.1:9000", map[string]string{
		"X-Client-Verify": "SUCCESS",
		"X-Client-DN":     "CN=phone-a,O=Molirain",
		"X-Client-Cert":   url.QueryEscape(certPEM),
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var body meResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Fingerprint != fingerprint {
		t.Fatalf("fingerprint = %q, want %q", body.Fingerprint, fingerprint)
	}
	// dn_cn keeps the DeviceID readable while the fingerprint stays available
	// for P2P pinning.
	if body.DeviceID != "cn:phone-a" {
		t.Fatalf("device id = %q", body.DeviceID)
	}
}
