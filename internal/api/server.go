package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ezhik-lb/ezhiklb/internal/domain"
	"github.com/ezhik-lb/ezhiklb/internal/store"
)

type Server struct {
	store      *store.Store
	adminToken string
	agentToken string
	secure     bool
	webDir     string
	logger     *slog.Logger
	settings   domain.SystemSettings
	restart    func()
}

type Options struct {
	AdminToken string
	AgentToken string
	Secure     bool
	WebDir     string
	Logger     *slog.Logger
	Settings   domain.SystemSettings
	Restart    func()
}

func New(st *store.Store, opts Options) (*Server, error) {
	if len(opts.AdminToken) < 24 || len(opts.AgentToken) < 24 {
		return nil, errors.New("admin and agent tokens must contain at least 24 characters")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Settings.PanelPort == 0 { opts.Settings.PanelPort = 8080 }
	if opts.Settings.AgentPort == 0 { opts.Settings.AgentPort = 8081 }
	return &Server{store: st, adminToken: opts.AdminToken, agentToken: opts.AgentToken, secure: opts.Secure, webDir: opts.WebDir, logger: opts.Logger, settings: opts.Settings, restart: opts.Restart}, nil
}

func (s *Server) Handler() http.Handler {
	return s.PanelHandler()
}

func (s *Server) PanelHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.HandleFunc("POST /api/v1/auth/logout", s.logout)
	mux.Handle("GET /api/v1/status", s.admin(http.HandlerFunc(s.status)))
	mux.Handle("GET /api/v1/profiles", s.admin(http.HandlerFunc(s.listProfiles)))
	mux.Handle("POST /api/v1/profiles", s.admin(http.HandlerFunc(s.createProfile)))
	mux.Handle("GET /api/v1/profiles/{id}", s.admin(http.HandlerFunc(s.getProfile)))
	mux.Handle("PUT /api/v1/profiles/{id}", s.admin(http.HandlerFunc(s.publishProfile)))
	mux.Handle("DELETE /api/v1/profiles/{id}", s.admin(http.HandlerFunc(s.deleteProfile)))
	mux.Handle("POST /api/v1/profiles/{id}/clone", s.admin(http.HandlerFunc(s.cloneProfile)))
	mux.Handle("GET /api/v1/profiles/{id}/revisions", s.admin(http.HandlerFunc(s.listRevisions)))
	mux.Handle("POST /api/v1/profiles/{id}/rollback/{number}", s.admin(http.HandlerFunc(s.rollbackProfile)))
	mux.Handle("GET /api/v1/nodes", s.admin(http.HandlerFunc(s.listNodes)))
	mux.Handle("POST /api/v1/nodes", s.admin(http.HandlerFunc(s.createNode)))
	mux.Handle("PUT /api/v1/nodes/{id}", s.admin(http.HandlerFunc(s.updateNode)))
	mux.Handle("DELETE /api/v1/nodes/{id}", s.admin(http.HandlerFunc(s.deleteNode)))
	mux.Handle("PUT /api/v1/nodes/{id}/enabled", s.admin(http.HandlerFunc(s.setNodeEnabled)))
	mux.Handle("POST /api/v1/nodes/{id}/rotate-token", s.admin(http.HandlerFunc(s.rotateNodeToken)))
	mux.Handle("POST /api/v1/nodes/{id}/revoke", s.admin(http.HandlerFunc(s.revokeNode)))
	mux.Handle("POST /api/v1/nodes/{id}/health-probe", s.admin(http.HandlerFunc(s.requestHealthProbe)))
	mux.Handle("POST /api/v1/nodes/{id}/update", s.admin(http.HandlerFunc(s.requestNodeUpdate)))
	mux.Handle("GET /api/v1/health", s.admin(http.HandlerFunc(s.listHealth)))
	mux.Handle("GET /api/v1/stats", s.admin(http.HandlerFunc(s.listStats)))
	mux.Handle("GET /api/v1/metrics/history", s.admin(http.HandlerFunc(s.listMetricHistory)))
	mux.Handle("GET /api/v1/events", s.admin(http.HandlerFunc(s.listEvents)))
	mux.Handle("PUT /api/v1/nodes/{id}/profile", s.admin(http.HandlerFunc(s.assignProfile)))
	mux.Handle("GET /api/v1/settings", s.admin(http.HandlerFunc(s.getSettings)))
	mux.Handle("PUT /api/v1/settings", s.admin(http.HandlerFunc(s.updateSettings)))
	// Alpha.8 keeps the old panel-port agent routes during migration. Newly
	// enrolled nodes use the dedicated agent listener.
	mux.Handle("GET /agent/v1/nodes/{id}/desired", s.agent(http.HandlerFunc(s.desiredState)))
	mux.Handle("POST /agent/v1/nodes/{id}/heartbeat", s.agent(http.HandlerFunc(s.heartbeat)))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, map[string]string{"status": "ok"}) })
	mux.Handle("/", s.static())
	return s.securityHeaders(s.recoverPanics(s.logRequests(mux)))
}

func (s *Server) AgentHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /agent/v1/nodes/{id}/desired", s.agent(http.HandlerFunc(s.desiredState)))
	mux.Handle("POST /agent/v1/nodes/{id}/heartbeat", s.agent(http.HandlerFunc(s.heartbeat)))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, map[string]string{"status": "ok"}) })
	return s.securityHeaders(s.recoverPanics(s.logRequests(mux)))
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct{ Token string `json:"token"` }
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if !sameSecret(body.Token, s.adminToken) {
		time.Sleep(250 * time.Millisecond)
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "Invalid administrator token")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "ezhiklb_session", Value: s.adminToken, Path: "/", HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode, MaxAge: 12 * 60 * 60})
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": true})
}

func (s *Server) logout(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "ezhiklb_session", Value: "", Path: "/", HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.store.ListProfiles(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	nodes, err := s.store.ListNodes(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	online := 0
	listeners := 0
	for _, node := range nodes {
		if node.Status == "online" {
			online++
		}
	}
	for _, profile := range profiles {
		_, revision, err := s.store.GetProfile(r.Context(), profile.ID)
		if err != nil {
			s.internalError(w, err)
			return
		}
		for _, listener := range revision.Config.Listeners {
			if listener.Enabled {
				listeners++
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": Version, "profiles": len(profiles), "nodes": len(nodes), "online_nodes": online, "listeners": listeners})
}

func (s *Server) listProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.store.ListProfiles(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, profiles)
}

type profilePayload struct {
	Name        string               `json:"name"`
	Description string               `json:"description"`
	AutoVersion *bool                `json:"auto_version"`
	Version     string               `json:"version"`
	Config      domain.ProfileConfig `json:"config"`
}

func (p profilePayload) versioning() (bool, string) {
	auto := true
	if p.AutoVersion != nil { auto = *p.AutoVersion }
	return auto, strings.TrimSpace(p.Version)
}

func (s *Server) createProfile(w http.ResponseWriter, r *http.Request) {
	var payload profilePayload
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if strings.TrimSpace(payload.Name) == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "Profile name is required")
		return
	}
	autoVersion, version := payload.versioning()
	profile, revision, err := s.store.CreateProfile(r.Context(), strings.TrimSpace(payload.Name), strings.TrimSpace(payload.Description), payload.Config, autoVersion, version)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"profile": profile, "revision": revision})
}

func (s *Server) getProfile(w http.ResponseWriter, r *http.Request) {
	profile, revision, err := s.store.GetProfile(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Profile not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profile": profile, "revision": revision})
}

func (s *Server) publishProfile(w http.ResponseWriter, r *http.Request) {
	var payload profilePayload
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if strings.TrimSpace(payload.Name) == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "Profile name is required")
		return
	}
	autoVersion, version := payload.versioning()
	profile, revision, err := s.store.PublishRevision(r.Context(), r.PathValue("id"), strings.TrimSpace(payload.Name), strings.TrimSpace(payload.Description), payload.Config, autoVersion, version)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Profile not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profile": profile, "revision": revision})
}

func (s *Server) listRevisions(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListRevisions(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) { writeError(w, http.StatusNotFound, "not_found", "Profile not found"); return }
	if err != nil { s.internalError(w, err); return }
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) rollbackProfile(w http.ResponseWriter, r *http.Request) {
	var number int64
	if _, err := fmt.Sscan(r.PathValue("number"), &number); err != nil || number < 1 { writeError(w, http.StatusBadRequest, "invalid_request", "Invalid revision number"); return }
	profile, revision, err := s.store.RollbackProfile(r.Context(), r.PathValue("id"), number)
	if errors.Is(err, store.ErrNotFound) { writeError(w, http.StatusNotFound, "not_found", "Profile or revision not found"); return }
	if err != nil { s.internalError(w, err); return }
	writeJSON(w, http.StatusOK, map[string]any{"profile": profile, "revision": revision})
}

func (s *Server) cloneProfile(w http.ResponseWriter, r *http.Request) {
	var body struct{ Name string `json:"name"` }
	if err := decodeJSON(r, &body); err != nil { writeError(w, http.StatusBadRequest, "invalid_request", err.Error()); return }
	profile, revision, err := s.store.CloneProfile(r.Context(), r.PathValue("id"), strings.TrimSpace(body.Name))
	if errors.Is(err, store.ErrNotFound) { writeError(w, http.StatusNotFound, "not_found", "Profile not found"); return }
	if err != nil { writeError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error()); return }
	writeJSON(w, http.StatusCreated, map[string]any{"profile": profile, "revision": revision})
}

func (s *Server) deleteProfile(w http.ResponseWriter, r *http.Request) {
	err := s.store.DeleteProfile(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) { writeError(w, http.StatusNotFound, "not_found", "Profile not found"); return }
	if err != nil { writeError(w, http.StatusConflict, "profile_in_use", err.Error()); return }
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.store.ListNodes(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nodes)
}

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListAudit(r.Context(), r.URL.Query().Get("filter"), 200)
	if err != nil { s.internalError(w, err); return }
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) listMetricHistory(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListMetricHistory(r.Context(), r.URL.Query().Get("node_id"))
	if err != nil { s.internalError(w, err); return }
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) createNode(w http.ResponseWriter, r *http.Request) {
	var body struct { Name string `json:"name"`; IngressAddress string `json:"ingress_address"`; ProfileID string `json:"profile_id"` }
	if err := decodeJSON(r, &body); err != nil { writeError(w, http.StatusBadRequest, "invalid_request", err.Error()); return }
	body.Name = strings.TrimSpace(body.Name); body.IngressAddress = strings.TrimSpace(body.IngressAddress)
	if body.Name == "" || body.ProfileID == "" { writeError(w, http.StatusUnprocessableEntity, "validation_failed", "Node name and profile are required"); return }
	node, token, err := s.store.CreateNode(r.Context(), body.Name, body.IngressAddress, body.ProfileID)
	if errors.Is(err, store.ErrNotFound) { writeError(w, http.StatusNotFound, "not_found", "Profile not found"); return }
	if err != nil { writeError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error()); return }
	writeJSON(w, http.StatusCreated, map[string]any{"node": node, "agent_token": token})
}

func (s *Server) updateNode(w http.ResponseWriter, r *http.Request) {
	var body struct { Name string `json:"name"`; IngressAddress string `json:"ingress_address"` }
	if err := decodeJSON(r, &body); err != nil { writeError(w, http.StatusBadRequest, "invalid_request", err.Error()); return }
	if strings.TrimSpace(body.Name) == "" { writeError(w, http.StatusUnprocessableEntity, "validation_failed", "Node name is required"); return }
	err := s.store.UpdateNode(r.Context(), r.PathValue("id"), strings.TrimSpace(body.Name), strings.TrimSpace(body.IngressAddress))
	if errors.Is(err, store.ErrNotFound) { writeError(w, http.StatusNotFound, "not_found", "Node not found"); return }
	if err != nil { writeError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error()); return }
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteNode(w http.ResponseWriter, r *http.Request) {
	err := s.store.DeleteNode(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) { writeError(w, http.StatusNotFound, "not_found", "Node not found"); return }
	if err != nil { writeError(w, http.StatusConflict, "cannot_delete", err.Error()); return }
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setNodeEnabled(w http.ResponseWriter, r *http.Request) {
	var body struct{ Enabled bool `json:"enabled"` }
	if err := decodeJSON(r, &body); err != nil { writeError(w, http.StatusBadRequest, "invalid_request", err.Error()); return }
	if err := s.store.SetNodeEnabled(r.Context(), r.PathValue("id"), body.Enabled); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Node not found")
	} else if err != nil {
		writeError(w, http.StatusConflict, "cannot_change_state", err.Error())
	} else {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) rotateNodeToken(w http.ResponseWriter, r *http.Request) {
	token, err := s.store.RotateNodeCredential(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) { writeError(w, http.StatusNotFound, "not_found", "Remote node credential not found"); return }
	if err != nil { s.internalError(w, err); return }
	writeJSON(w, http.StatusOK, map[string]string{"agent_token": token})
}

func (s *Server) revokeNode(w http.ResponseWriter, r *http.Request) {
	err := s.store.RevokeNodeCredential(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) { writeError(w, http.StatusNotFound, "not_found", "Remote node credential not found"); return }
	if err != nil { writeError(w, http.StatusConflict, "cannot_revoke", err.Error()); return }
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) requestHealthProbe(w http.ResponseWriter, r *http.Request) {
	nonce, err := s.store.RequestHealthProbe(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) { writeError(w, http.StatusNotFound, "not_found", "Node not found"); return }
	if err != nil { s.internalError(w, err); return }
	writeJSON(w, http.StatusAccepted, map[string]int64{"health_probe": nonce})
}

func (s *Server) requestNodeUpdate(w http.ResponseWriter, r *http.Request) {
	if err := s.store.RequestNodeUpdate(r.Context(), r.PathValue("id"), Version); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Node not found")
	} else if err != nil { s.internalError(w, err) } else { writeJSON(w, http.StatusAccepted, map[string]string{"version": Version}) }
}

func (s *Server) listHealth(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListHealth(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) listStats(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListStats(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) assignProfile(w http.ResponseWriter, r *http.Request) {
	var body struct{ ProfileID string `json:"profile_id"` }
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := s.store.AssignProfile(r.Context(), r.PathValue("id"), body.ProfileID); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Node or profile not found")
	} else if err != nil {
		s.internalError(w, err)
	} else {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.GetSystemSettings(r.Context(), s.settings)
	if err != nil { s.internalError(w, err); return }
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) {
	var next domain.SystemSettings
	if err := decodeJSON(r, &next); err != nil { writeError(w, http.StatusBadRequest, "invalid_request", err.Error()); return }
	if next.PanelPort != s.settings.PanelPort { next.LegacyPanelPort = s.settings.PanelPort } else { next.LegacyPanelPort = s.settings.LegacyPanelPort }
	if next.AgentPort != s.settings.AgentPort { next.LegacyAgentPort = s.settings.AgentPort } else { next.LegacyAgentPort = s.settings.LegacyAgentPort }
	if err := s.store.UpdateSystemSettings(r.Context(), next); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"settings": next, "restarting": true})
	if s.restart != nil {
		go func() { time.Sleep(350 * time.Millisecond); s.restart() }()
	}
}

func (s *Server) desiredState(w http.ResponseWriter, r *http.Request) {
	state, err := s.store.DesiredState(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Desired state not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	etag := fmt.Sprintf(`"rev-%d-probe-%d-update-%s"`, state.Revision, state.HealthProbe, state.UpdateVersion)
	if state.Decommission { etag = fmt.Sprintf(`"rev-%d-probe-%d-decommission"`, state.Revision, state.HealthProbe) }
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Version         string `json:"version"`
		AppliedRevision int64  `json:"applied_revision"`
		ApplyError      string `json:"apply_error"`
		ApplyState      string `json:"apply_state"`
		Health          []domain.BackendHealth `json:"health"`
		Stats           []domain.ServiceStat `json:"stats"`
		Metrics         domain.NodeMetrics `json:"metrics"`
		Diagnostics     domain.NodeDiagnostics `json:"diagnostics"`
		UpdateState     string `json:"update_state"`
		UpdateError     string `json:"update_error"`
		Decommissioned  bool `json:"decommissioned"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	observedAddress := remoteIPv4(r)
	if r.PathValue("id") == "local" && (observedAddress == "127.0.0.1" || observedAddress == "::1") { observedAddress = "" }
	if err := s.store.Heartbeat(r.Context(), r.PathValue("id"), body.Version, observedAddress, body.ApplyState, body.AppliedRevision, body.ApplyError, body.Health, body.Stats, body.Metrics, body.Diagnostics, body.UpdateState, body.UpdateError, body.Decommissioned); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Node not found")
	} else if err != nil {
		s.internalError(w, err)
	} else {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) admin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, _ := r.Cookie("ezhiklb_session")
		if cookie == nil || !sameSecret(cookie.Value, s.adminToken) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) agent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		nodeID := r.PathValue("id")
		localValid := nodeID == "local" && s.store.NodeEnabled(r.Context(), nodeID) && sameSecret(value, s.agentToken)
		if !localValid && !s.store.ValidateNodeCredential(r.Context(), nodeID, value) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Invalid agent credentials")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func remoteIPv4(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil { host = r.RemoteAddr }
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil || ip.To4() == nil { return "" }
	return ip.String()
}

func (s *Server) static() http.Handler {
	if s.webDir == "" {
		return http.NotFoundHandler()
	}
	files := http.FileServer(http.Dir(s.webDir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(filepath.Clean("/"+strings.TrimPrefix(r.URL.Path, "/")), "/")
		if clean == "." {
			clean = "index.html"
		}
		if _, err := os.Stat(filepath.Join(s.webDir, clean)); err != nil {
			r.URL.Path = "/"
			clean = "index.html"
		}
		if clean == "index.html" {
			w.Header().Set("Cache-Control", "no-cache")
		} else if strings.HasPrefix(clean, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		s.logger.Info("http request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started))
	})
}

func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("request panic", "error", recovered)
				writeError(w, http.StatusInternalServerError, "internal_error", "Internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) internalError(w http.ResponseWriter, err error) {
	s.logger.Error("api error", "error", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "Internal server error")
}

func decodeJSON(r *http.Request, target any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func sameSecret(a, b string) bool {
	if len(a) != len(b) || len(a) == 0 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

const Version = "0.1.0-beta.3"

func ListenAddress(host string, port int) string { return fmt.Sprintf("%s:%d", host, port) }
