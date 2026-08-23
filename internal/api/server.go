package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
}

type Options struct {
	AdminToken string
	AgentToken string
	Secure     bool
	WebDir     string
	Logger     *slog.Logger
}

func New(st *store.Store, opts Options) (*Server, error) {
	if len(opts.AdminToken) < 24 || len(opts.AgentToken) < 24 {
		return nil, errors.New("admin and agent tokens must contain at least 24 characters")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Server{store: st, adminToken: opts.AdminToken, agentToken: opts.AgentToken, secure: opts.Secure, webDir: opts.WebDir, logger: opts.Logger}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.HandleFunc("POST /api/v1/auth/logout", s.logout)
	mux.Handle("GET /api/v1/status", s.admin(http.HandlerFunc(s.status)))
	mux.Handle("GET /api/v1/profiles", s.admin(http.HandlerFunc(s.listProfiles)))
	mux.Handle("POST /api/v1/profiles", s.admin(http.HandlerFunc(s.createProfile)))
	mux.Handle("GET /api/v1/profiles/{id}", s.admin(http.HandlerFunc(s.getProfile)))
	mux.Handle("PUT /api/v1/profiles/{id}", s.admin(http.HandlerFunc(s.publishProfile)))
	mux.Handle("GET /api/v1/nodes", s.admin(http.HandlerFunc(s.listNodes)))
	mux.Handle("GET /api/v1/health", s.admin(http.HandlerFunc(s.listHealth)))
	mux.Handle("PUT /api/v1/nodes/{id}/profile", s.admin(http.HandlerFunc(s.assignProfile)))
	mux.Handle("GET /agent/v1/nodes/{id}/desired", s.agent(http.HandlerFunc(s.desiredState)))
	mux.Handle("POST /agent/v1/nodes/{id}/heartbeat", s.agent(http.HandlerFunc(s.heartbeat)))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, map[string]string{"status": "ok"}) })
	mux.Handle("/", s.static())
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
	Config      domain.ProfileConfig `json:"config"`
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
	profile, revision, err := s.store.CreateProfile(r.Context(), strings.TrimSpace(payload.Name), strings.TrimSpace(payload.Description), payload.Config)
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
	profile, revision, err := s.store.PublishRevision(r.Context(), r.PathValue("id"), strings.TrimSpace(payload.Name), strings.TrimSpace(payload.Description), payload.Config)
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

func (s *Server) listNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.store.ListNodes(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nodes)
}

func (s *Server) listHealth(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListHealth(r.Context())
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
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Version         string `json:"version"`
		AppliedRevision int64  `json:"applied_revision"`
		ApplyError      string `json:"apply_error"`
		Health          []domain.BackendHealth `json:"health"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := s.store.Heartbeat(r.Context(), r.PathValue("id"), body.Version, body.AppliedRevision, body.ApplyError, body.Health); errors.Is(err, store.ErrNotFound) {
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
		if !sameSecret(value, s.agentToken) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Invalid agent credentials")
			return
		}
		next.ServeHTTP(w, r)
	})
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

const Version = "0.1.0-alpha.3"

func ListenAddress(host string, port int) string { return fmt.Sprintf("%s:%d", host, port) }
