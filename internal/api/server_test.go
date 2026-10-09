package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ezhik-lb/ezhiklb/internal/domain"
	"github.com/ezhik-lb/ezhiklb/internal/store"
)

func testNodeServer(t *testing.T) (*store.Store, http.Handler, domain.Node, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.Bootstrap(ctx, "", domain.DefaultProfileConfig(), "Default", false); err != nil {
		t.Fatal(err)
	}
	profiles, err := st.ListProfiles(ctx)
	if err != nil || len(profiles) == 0 {
		t.Fatalf("profiles: %v, %v", profiles, err)
	}
	node, token, err := st.CreateNode(ctx, "Node", "198.51.100.10", profiles[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(st, Options{AdminToken: strings.Repeat("a", 32), AgentToken: strings.Repeat("b", 32)})
	if err != nil {
		t.Fatal(err)
	}
	return st, server.AgentHandler(), node, token
}

func agentRequest(handler http.Handler, method, path, token, body, etag string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-EzhikLB-Agent-Version", Version)
	request.Header.Set("If-None-Match", etag)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestDesiredStateIPChangeInvalidatesETagWithoutPublishing(t *testing.T) {
	st, handler, node, token := testNodeServer(t)
	path := "/agent/v1/nodes/" + node.ID + "/desired"
	first := agentRequest(handler, "GET", path, token, "", "")
	if first.Code != http.StatusOK {
		t.Fatalf("first desired: %d %s", first.Code, first.Body)
	}
	etag := first.Header().Get("ETag")
	if response := agentRequest(handler, "GET", path, token, "", etag); response.Code != http.StatusNotModified {
		t.Fatalf("unchanged state: %d", response.Code)
	}
	if err := st.UpdateNode(context.Background(), node.ID, node.Name, "198.51.100.20"); err != nil {
		t.Fatal(err)
	}
	changed := agentRequest(handler, "GET", path, token, "", etag)
	if changed.Code != http.StatusOK || changed.Header().Get("ETag") == etag {
		t.Fatalf("IP change was cached: %d %s", changed.Code, changed.Body)
	}
	var desired domain.NodeDesiredState
	if err := json.Unmarshal(changed.Body.Bytes(), &desired); err != nil {
		t.Fatal(err)
	}
	if desired.IngressAddress != "198.51.100.20" || desired.Revision != node.DesiredRevision {
		t.Fatalf("desired state: %#v", desired)
	}
}

func TestSelfUninstallRequiresRequestAndOwnCredential(t *testing.T) {
	st, handler, node, token := testNodeServer(t)
	base := "/agent/v1/nodes/" + node.ID
	// An unsolicited cleanup acknowledgement must not remove the record.
	ack := `{"version":"1.2.1","decommissioned":true}`
	if response := agentRequest(handler, "POST", base+"/heartbeat", token, ack, ""); response.Code != http.StatusNoContent {
		t.Fatalf("heartbeat: %d %s", response.Code, response.Body)
	}
	if response := agentRequest(handler, "GET", base+"/desired", token, "", ""); response.Code != http.StatusOK {
		t.Fatalf("node removed before request: %d", response.Code)
	}
	_, otherToken, err := st.CreateNode(context.Background(), "Other", "198.51.100.30", node.ProfileID)
	if err != nil {
		t.Fatal(err)
	}
	if response := agentRequest(handler, "POST", base+"/decommission", otherToken, "", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("foreign credential accepted: %d", response.Code)
	}
	if response := agentRequest(handler, "POST", base+"/decommission", token, "", ""); response.Code != http.StatusNoContent {
		t.Fatalf("request: %d %s", response.Code, response.Body)
	}
	pending := agentRequest(handler, "GET", base+"/desired", token, "", "")
	var desired domain.NodeDesiredState
	if err := json.Unmarshal(pending.Body.Bytes(), &desired); err != nil || !desired.Decommission {
		t.Fatalf("node must wait for cleanup: %s (%v)", pending.Body, err)
	}
	if response := agentRequest(handler, "POST", base+"/heartbeat", token, ack, ""); response.Code != http.StatusNoContent {
		t.Fatalf("cleanup acknowledgement: %d %s", response.Code, response.Body)
	}
	if st.ValidateNodeCredential(context.Background(), node.ID, token) {
		t.Fatal("deleted node still authenticates")
	}
}
