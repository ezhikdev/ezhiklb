package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookie = "ezhiklb_session"
	sessionTTL    = 12 * time.Hour
	// Failed logins allowed per client address within loginWindow.
	loginFailureLimit = 10
	loginWindow       = 10 * time.Minute
)

// sessions issues signed, expiring session cookies. Before 2.0 the cookie
// held the administrator token itself, so a leaked cookie leaked the token.
// The signing key is derived from the token: sessions survive panel restarts
// and are invalidated together with the token.
type sessions struct {
	key     []byte
	mu      sync.Mutex
	revoked map[string]time.Time
	now     func() time.Time
}

func newSessions(adminToken string) *sessions {
	sum := sha256.Sum256([]byte("ezhiklb-session-v2\x00" + adminToken))
	return &sessions{key: sum[:], revoked: map[string]time.Time{}, now: time.Now}
}

func (s *sessions) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, s.key)
	mac.Write(payload)
	return mac.Sum(nil)
}

// issue returns a new cookie value valid for sessionTTL.
func (s *sessions) issue() (string, error) {
	payload := make([]byte, 8+16)
	binary.BigEndian.PutUint64(payload, uint64(s.now().Add(sessionTTL).Unix()))
	if _, err := rand.Read(payload[8:]); err != nil {
		return "", err
	}
	encoding := base64.RawURLEncoding
	return "v2." + encoding.EncodeToString(payload) + "." + encoding.EncodeToString(s.sign(payload)), nil
}

// parse verifies a cookie value and returns its nonce and expiry.
func (s *sessions) parse(value string) (string, time.Time, bool) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 || parts[0] != "v2" {
		return "", time.Time{}, false
	}
	encoding := base64.RawURLEncoding
	payload, err := encoding.DecodeString(parts[1])
	if err != nil || len(payload) != 24 {
		return "", time.Time{}, false
	}
	signature, err := encoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(signature, s.sign(payload)) {
		return "", time.Time{}, false
	}
	expires := time.Unix(int64(binary.BigEndian.Uint64(payload)), 0)
	if !s.now().Before(expires) {
		return "", time.Time{}, false
	}
	return parts[1], expires, true
}

func (s *sessions) valid(value string) bool {
	nonce, _, ok := s.parse(value)
	if !ok {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, revoked := s.revoked[nonce]
	return !revoked
}

// revoke invalidates a session on logout until it would have expired.
func (s *sessions) revoke(value string) {
	nonce, expires, ok := s.parse(value)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for key, until := range s.revoked {
		if !now.Before(until) {
			delete(s.revoked, key)
		}
	}
	s.revoked[nonce] = expires
}

// loginLimiter slows down administrator-token guessing per client address.
type loginLimiter struct {
	mu       sync.Mutex
	failures map[string][]time.Time
	now      func() time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{failures: map[string][]time.Time{}, now: time.Now}
}

func (l *loginLimiter) recent(client string) []time.Time {
	cutoff := l.now().Add(-loginWindow)
	kept := l.failures[client][:0]
	for _, at := range l.failures[client] {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	if len(kept) == 0 {
		delete(l.failures, client)
		return nil
	}
	l.failures[client] = kept
	return kept
}

func (l *loginLimiter) blocked(client string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(client)) >= loginFailureLimit
}

func (l *loginLimiter) fail(client string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	// Bound memory under a distributed attack: forget the oldest records.
	if len(l.failures) > 4096 {
		for key := range l.failures {
			if l.recent(key) == nil || len(l.failures) > 4096 {
				delete(l.failures, key)
			}
		}
	}
	l.failures[client] = append(l.recent(client), l.now())
}

func (l *loginLimiter) succeed(client string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, client)
}

func clientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// sameOriginRequest rejects cross-site state changes. The panel only sends
// JSON from its own pages; browsers cannot send a cross-site JSON request
// without a CORS preflight, which the panel never answers. Sec-Fetch-Site
// additionally refuses requests from other sites on the same host (ports).
func sameOriginRequest(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type"))), "application/json")
}
