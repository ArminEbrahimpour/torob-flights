package alibaba

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type testCert struct {
	cert *x509.Certificate
	der  []byte
	key  *ecdsa.PrivateKey
}

// issue makes a certificate signed by parent (self-signed when parent is nil).
func issue(t *testing.T, parent *testCert, cn string, dns []string, ips []net.IP, notBefore, notAfter time.Time, isCA bool) *testCert {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  isCA,
		DNSNames:              dns,
		IPAddresses:           ips,
	}
	signer, signerKey := tmpl, key
	if parent != nil {
		signer, signerKey = parent.cert, parent.key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signer, &key.PublicKey, signerKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testCert{cert: cert, der: der, key: key}
}

func state(certs ...*testCert) tls.ConnectionState {
	var peers []*x509.Certificate
	for _, c := range certs {
		peers = append(peers, c.cert)
	}
	return tls.ConnectionState{PeerCertificates: peers}
}

func poolOf(certs ...*testCert) *x509.CertPool {
	p := x509.NewCertPool()
	for _, c := range certs {
		p.AddCert(c.cert)
	}
	return p
}

var (
	now      = time.Date(2026, 10, 19, 6, 0, 0, 0, time.UTC)
	longAgo  = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	farAway  = time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)
	issued   = time.Date(2024, 12, 31, 12, 32, 2, 0, time.UTC)
	expiry   = time.Date(2025, 12, 31, 12, 32, 1, 0, time.UTC) // the leaf the collector received
	hostName = "ws.alibaba.ir"
)

func TestVerifyServerCert(t *testing.T) {
	root := issue(t, nil, "Test Root", nil, nil, longAgo, farAway, true)
	other := issue(t, nil, "Unknown Root", nil, nil, longAgo, farAway, true)

	tests := []struct {
		name          string
		leaf          *testCert
		chain         []*testCert
		roots         *x509.CertPool
		acceptExpired bool
		wantErr       string
		wantAccepted  bool
	}{
		{
			name:  "valid certificate passes with the check off",
			leaf:  issue(t, root, "leaf", []string{hostName}, nil, issued, farAway, false),
			roots: poolOf(root),
		},
		{
			name:    "expired certificate is refused by default",
			leaf:    issue(t, root, "leaf", []string{hostName}, nil, issued, expiry, false),
			roots:   poolOf(root),
			wantErr: "expired",
		},
		{
			name:          "expired certificate is accepted when allowed",
			leaf:          issue(t, root, "leaf", []string{hostName}, nil, issued, expiry, false),
			roots:         poolOf(root),
			acceptExpired: true,
			wantAccepted:  true,
		},
		{
			name:          "a wrong host name is still refused",
			leaf:          issue(t, root, "leaf", []string{"www.example.com"}, nil, issued, expiry, false),
			roots:         poolOf(root),
			acceptExpired: true,
			wantErr:       "not www.example.com|valid for",
		},
		{
			name:          "an unknown issuer is still refused",
			leaf:          issue(t, other, "leaf", []string{hostName}, nil, issued, expiry, false),
			roots:         poolOf(root),
			acceptExpired: true,
			wantErr:       "unknown authority",
		},
		{
			name:          "a certificate that is not valid yet is still refused",
			leaf:          issue(t, root, "leaf", []string{hostName}, nil, now.Add(24*time.Hour), farAway, false),
			roots:         poolOf(root),
			acceptExpired: true,
			wantErr:       "expired or is not yet valid",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			chain := append([]*testCert{tc.leaf}, tc.chain...)
			var accepted time.Duration
			err := verifyServerCert(state(chain...), hostName, tc.roots, now, tc.acceptExpired, func(d time.Duration) { accepted = d })
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			} else {
				if err == nil {
					t.Fatalf("expected an error containing %q, got none", tc.wantErr)
				}
				matched := false
				for _, want := range strings.Split(tc.wantErr, "|") {
					if strings.Contains(err.Error(), want) {
						matched = true
					}
				}
				if !matched {
					t.Fatalf("error %q does not contain %q", err, tc.wantErr)
				}
			}
			if (accepted > 0) != tc.wantAccepted {
				t.Fatalf("accepted callback = %v, want called: %v", accepted, tc.wantAccepted)
			}
			if tc.wantAccepted {
				if want := now.Sub(expiry); accepted != want {
					t.Fatalf("expired for %s, want %s", accepted, want)
				}
			}
		})
	}
}

func TestExpiredIntermediateIsNotForgiven(t *testing.T) {
	root := issue(t, nil, "Test Root", nil, nil, longAgo, farAway, true)
	// The intermediate outlived the leaf but has expired since. Only the leaf may be forgiven.
	inter := issue(t, root, "Test Intermediate", nil, nil, longAgo, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), true)
	leaf := issue(t, inter, "leaf", []string{hostName}, nil, issued, expiry, false)
	err := verifyServerCert(state(leaf, inter), hostName, poolOf(root), now, true, func(time.Duration) {})
	if err == nil {
		t.Fatal("an expired intermediate must be refused")
	}
}

func TestExpiredLeafUnderValidIntermediateIsForgiven(t *testing.T) {
	root := issue(t, nil, "Test Root", nil, nil, longAgo, farAway, true)
	inter := issue(t, root, "Test Intermediate", nil, nil, longAgo, farAway, true)
	leaf := issue(t, inter, "leaf", []string{hostName}, nil, issued, expiry, false)
	called := false
	err := verifyServerCert(state(leaf, inter), hostName, poolOf(root), now, true, func(time.Duration) { called = true })
	if err != nil || !called {
		t.Fatalf("err=%v called=%v, want accepted", err, called)
	}
}

func TestNoCertificateIsRefused(t *testing.T) {
	if err := verifyServerCert(tls.ConnectionState{}, hostName, nil, now, true, func(time.Duration) {}); err == nil {
		t.Fatal("expected an error")
	}
}

// startExpiredServer serves JSON over HTTPS with a certificate that expired yesterday.
func startExpiredServer(t *testing.T) (*httptest.Server, *x509.CertPool) {
	t.Helper()
	real := time.Now()
	root := issue(t, nil, "Test Root", nil, nil, real.Add(-48*time.Hour), real.Add(48*time.Hour), true)
	leaf := issue(t, root, "leaf", nil, []net.IP{net.ParseIP("127.0.0.1")}, real.Add(-47*time.Hour), real.Add(-24*time.Hour), false)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{leaf.der}, PrivateKey: leaf.key}}}
	srv.Config.ErrorLog = nil
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, poolOf(root)
}

func TestClientTalksToAServerWithAnExpiredCertificateOnlyWhenAllowed(t *testing.T) {
	srv, roots := startExpiredServer(t)
	base := Options{BaseURL: srv.URL, RootCAs: roots, CircuitBreakerThreshold: 3, CircuitBreakerOpen: time.Minute, RPS: 100, Burst: 10}

	strict := New(base)
	var out struct{ OK bool }
	err := strict.doJSON(context.Background(), http.MethodGet, "/ping", nil, &out)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("strict client: want an expired-certificate error, got %v", err)
	}

	base.AcceptExpiredCert = true
	tolerant := New(base)
	if err := tolerant.doJSON(context.Background(), http.MethodGet, "/ping", nil, &out); err != nil {
		t.Fatalf("tolerant client: %v", err)
	}
	if !out.OK {
		t.Fatal("tolerant client: the body was not decoded")
	}
}

func TestTolerantClientStillRefusesAnUntrustedServer(t *testing.T) {
	srv, _ := startExpiredServer(t)
	stranger := poolOf(issue(t, nil, "Someone Else", nil, nil, longAgo, farAway, true))
	c := New(Options{BaseURL: srv.URL, RootCAs: stranger, AcceptExpiredCert: true, CircuitBreakerThreshold: 3, CircuitBreakerOpen: time.Minute, RPS: 100, Burst: 10})
	var out struct{ OK bool }
	if err := c.doJSON(context.Background(), http.MethodGet, "/ping", nil, &out); err == nil {
		t.Fatal("a server signed by an unknown root must be refused even in tolerant mode")
	}
}

func TestWarnLimiterAllowsOncePerInterval(t *testing.T) {
	w := newWarnLimiter(time.Hour)
	if !w.allow() {
		t.Fatal("first call should pass")
	}
	if w.allow() {
		t.Fatal("second call inside the interval should be blocked")
	}
}
