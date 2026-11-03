package alibaba

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// verifyServerCert checks the certificate chain a server sent.
//
// It makes the same checks as the standard library: host name, signature chain to a trusted
// root, and the validity window of every certificate. When acceptExpired is set it forgives
// exactly one failure: a leaf certificate that has already expired. The chain is then checked
// again as of the last second the leaf was valid, so the name, the signatures and the trust
// anchor still have to be right. The intermediates and the root must also be valid today.
// A wrong name, an unknown issuer, a certificate that is not valid yet, or an expired
// intermediate is still refused.
func verifyServerCert(state tls.ConnectionState, host string, roots *x509.CertPool, now time.Time, acceptExpired bool, onAccepted func(expiredFor time.Duration)) error {
	if len(state.PeerCertificates) == 0 {
		return errors.New("alibaba: the server sent no certificate")
	}
	leaf := state.PeerCertificates[0]
	intermediates := x509.NewCertPool()
	for _, c := range state.PeerCertificates[1:] {
		intermediates.AddCert(c)
	}
	opts := x509.VerifyOptions{DNSName: host, Roots: roots, Intermediates: intermediates, CurrentTime: now}
	_, err := leaf.Verify(opts)
	if err == nil {
		return nil
	}

	var invalid x509.CertificateInvalidError
	if !acceptExpired || !errors.As(err, &invalid) || invalid.Reason != x509.Expired || !now.After(leaf.NotAfter) {
		return err
	}
	opts.CurrentTime = leaf.NotAfter.Add(-time.Second)
	chains, pastErr := leaf.Verify(opts)
	if pastErr != nil {
		// Go reports the expiry first. This is the real problem: a wrong name or an unknown issuer.
		return pastErr
	}
	for _, chain := range chains {
		if validAt(chain[1:], now) {
			onAccepted(now.Sub(leaf.NotAfter))
			return nil
		}
	}
	return err
}

// validAt reports whether every certificate is inside its validity window at t.
func validAt(certs []*x509.Certificate, t time.Time) bool {
	for _, c := range certs {
		if t.Before(c.NotBefore) || t.After(c.NotAfter) {
			return false
		}
	}
	return true
}

// expiredCertTLS returns a TLS config that forgives an expired leaf certificate on host.
// Verification moves into VerifyConnection, so nothing is skipped except that one failure.
// roots nil means the system roots.
func expiredCertTLS(host string, roots *x509.CertPool) *tls.Config {
	warn := newWarnLimiter(time.Hour)
	return &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
		// The standard verification is replaced by verifyServerCert, which repeats it.
		InsecureSkipVerify: true,
		VerifyConnection: func(state tls.ConnectionState) error {
			return verifyServerCert(state, host, roots, time.Now(), true, func(expiredFor time.Duration) {
				if warn.allow() {
					slog.Warn("alibaba: accepting an expired server certificate",
						"host", host,
						"expired_for", expiredFor.Round(time.Hour).String(),
						"hint", "set ALIBABA_ACCEPT_EXPIRED_CERT=false once the host serves a valid certificate")
				}
			})
		},
	}
}

// warnLimiter lets a log line through at most once per interval.
type warnLimiter struct {
	mu       sync.Mutex
	interval time.Duration
	last     time.Time
}

func newWarnLimiter(interval time.Duration) *warnLimiter {
	return &warnLimiter{interval: interval}
}

func (w *warnLimiter) allow() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.last.IsZero() && time.Since(w.last) < w.interval {
		return false
	}
	w.last = time.Now()
	return true
}
