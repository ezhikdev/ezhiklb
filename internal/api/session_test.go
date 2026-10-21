package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ezhik-lb/ezhiklb/internal/domain"
	"github.com/ezhik-lb/ezhiklb/internal/store"
)

const testAdminToken = "admin-token-admin-token-admin-token"

func testPanel(t *testing.T) (*store.Store, *Server) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Bootstrap(context.Background(), "198.51.100.10", domain.DefaultProfileConfig(), "Default", true); err != nil {
		t.Fatal(err)
	}
	server, err := New(st, Options{AdminToken: testAdminToken, AgentToken: strings.Repeat("b", 32)})
	if err != nil {
		t.Fatal(err)
	}
	return st, server
}

func panelRequest(handler http.Handler, method, path, body string, cookie *http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = "203.0.113.7:40000"
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func login(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	response := panelRequest(handler, "POST", "/api/v1/auth/login", `{"token":"`+testAdminToken+`"}`, nil, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("login: %d %s", response.Code, response.Body)
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == sessionCookie {
			return cookie
		}
	}
	t.Fatal("no session cookie")
	return nil
}

func TestSessionCookieDoesNotExposeAdminToken(t *testing.T) {
	_, server := testPanel(t)
	handler := server.PanelHandler()
	cookie := login(t, handler)
	if strings.Contains(cookie.Value, testAdminToken) || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("unsafe session cookie: %#v", cookie)
	}
	if response := panelRequest(handler, "GET", "/api/v1/status", "", cookie, nil); response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("session rejected: %d %v", response.Code, response.Header())
	}
	// A 1.x cookie carrying the raw token is no longer a credential.
	if response := panelRequest(handler, "GET", "/api/v1/status", "", &http.Cookie{Name: sessionCookie, Value: testAdminToken}, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("raw token cookie accepted: %d", response.Code)
	}
	tampered := *cookie
	tampered.Value = cookie.Value[:len(cookie.Value)-2] + "AA"
	if response := panelRequest(handler, "GET", "/api/v1/status", "", &tampered, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("tampered cookie accepted: %d", response.Code)
	}
	if response := panelRequest(handler, "POST", "/api/v1/auth/logout", "", cookie, nil); response.Code != http.StatusNoContent {
		t.Fatalf("logout: %d", response.Code)
	}
	if response := panelRequest(handler, "GET", "/api/v1/status", "", cookie, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session still valid: %d", response.Code)
	}
}

func TestSessionExpires(t *testing.T) {
	sessions := newSessions(testAdminToken)
	value, err := sessions.issue()
	if err != nil || !sessions.valid(value) {
		t.Fatalf("fresh session invalid: %v", err)
	}
	sessions.now = func() time.Time { return time.Now().Add(sessionTTL + time.Minute) }
	if sessions.valid(value) {
		t.Fatal("expired session accepted")
	}
	if newSessions("another-admin-token-another-admin").valid(value) {
		t.Fatal("session survived an administrator token change")
	}
}

func TestLoginIsRateLimitedPerClient(t *testing.T) {
	_, server := testPanel(t)
	handler := server.PanelHandler()
	for attempt := 0; attempt < loginFailureLimit; attempt++ {
		if response := panelRequest(handler, "POST", "/api/v1/auth/login", `{"token":"wrong-token-wrong-token-wrong"}`, nil, nil); response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", attempt, response.Code)
		}
	}
	if response := panelRequest(handler, "POST", "/api/v1/auth/login", `{"token":"`+testAdminToken+`"}`, nil, nil); response.Code != http.StatusTooManyRequests {
		t.Fatalf("guessing was not throttled: %d", response.Code)
	}
	server.logins.now = func() time.Time { return time.Now().Add(loginWindow + time.Second) }
	if response := panelRequest(handler, "POST", "/api/v1/auth/login", `{"token":"`+testAdminToken+`"}`, nil, nil); response.Code != http.StatusOK {
		t.Fatalf("limit did not expire: %d", response.Code)
	}
}

func TestCrossSiteStateChangesAreRefused(t *testing.T) {
	_, server := testPanel(t)
	handler := server.PanelHandler()
	cookie := login(t, handler)
	body := `{"name":"Copy"}`
	profiles := panelRequest(handler, "GET", "/api/v1/profiles", "", cookie, nil)
	if profiles.Code != http.StatusOK {
		t.Fatal(profiles.Code)
	}
	for name, headers := range map[string]map[string]string{
		"form post":        {"Content-Type": "text/plain"},
		"cross-site fetch": {"Sec-Fetch-Site": "cross-site"},
		"same-site port":   {"Sec-Fetch-Site": "same-site"},
	} {
		if response := panelRequest(handler, "POST", "/api/v1/nodes/local/health-probe", body, cookie, headers); response.Code != http.StatusForbidden {
			t.Errorf("%s: %d", name, response.Code)
		}
	}
	if response := panelRequest(handler, "POST", "/api/v1/nodes/local/health-probe", "", cookie, map[string]string{"Sec-Fetch-Site": "same-origin"}); response.Code != http.StatusAccepted {
		t.Fatalf("same-origin request refused: %d %s", response.Code, response.Body)
	}
}

func TestDesiredStateWithholdsHiddenProfileFromOldAgents(t *testing.T) {
	st, handler, node, token := testNodeServer(t)
	ctx := context.Background()
	config := domain.DefaultProfileConfig()
	config.Hidden = true
	config.Listeners = []domain.Listener{{ID: "a", Name: "A", Enabled: true, ListenPort: 443, Protocols: []domain.Protocol{domain.ProtocolTCP}, Scheduler: "wrr", SNI: "a.example.com",
		Backends: []domain.Backend{{ID: "b", Address: "192.0.2.10", Port: 8443, Weight: 1, Enabled: true}}}}
	profile, _, err := st.CreateProfile(ctx, "Hidden", "", config, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AssignProfile(ctx, node.ID, profile.ID); err != nil {
		t.Fatal(err)
	}
	path := "/agent/v1/nodes/" + node.ID + "/desired"
	old := httptest.NewRequest("GET", path, nil)
	old.Header.Set("Authorization", "Bearer "+token)
	old.Header.Set("X-EzhikLB-Agent-Version", "1.2.2")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, old)
	if response.Code != http.StatusConflict {
		t.Fatalf("1.2.2 agent received a hidden profile: %d %s", response.Code, response.Body)
	}
	if current := agentRequest(handler, "GET", path, token, "", ""); current.Code != http.StatusOK || !strings.Contains(current.Body.String(), `"hidden":true`) {
		t.Fatalf("2.0 agent: %d %s", current.Code, current.Body)
	}
}
