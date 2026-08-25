package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ezhik-lb/ezhiklb/internal/domain"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	statements := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA foreign_keys=ON`,
		`PRAGMA busy_timeout=5000`,
		`CREATE TABLE IF NOT EXISTS profiles (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			description TEXT NOT NULL DEFAULT '',
			current_revision INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS profile_revisions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			profile_id TEXT NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
			number INTEGER NOT NULL,
			config_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			UNIQUE(profile_id, number)
		)`,
		`CREATE TABLE IF NOT EXISTS nodes (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			ingress_address TEXT NOT NULL DEFAULT '',
			observed_address TEXT NOT NULL DEFAULT '',
			profile_id TEXT REFERENCES profiles(id) ON DELETE SET NULL,
			desired_revision INTEGER NOT NULL DEFAULT 0,
			applied_revision INTEGER NOT NULL DEFAULT 0,
			agent_version TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'offline',
			apply_state TEXT NOT NULL DEFAULT 'waiting',
			last_seen_at TEXT,
			online_since TEXT,
			last_error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS node_credentials (
			node_id TEXT PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
			token_hash TEXT NOT NULL,
			created_at TEXT NOT NULL,
			rotated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS node_probe_requests (
			node_id TEXT PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
			nonce INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			action TEXT NOT NULL,
			target_type TEXT NOT NULL,
			target_id TEXT NOT NULL,
			details_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS backend_health (
			node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
			address TEXT NOT NULL,
			state TEXT NOT NULL,
			consecutive_successes INTEGER NOT NULL DEFAULT 0,
			consecutive_failures INTEGER NOT NULL DEFAULT 0,
			latency_millis INTEGER NOT NULL DEFAULT 0,
			checked_at TEXT NOT NULL,
			PRIMARY KEY(node_id, address)
		)`,
		`CREATE TABLE IF NOT EXISTS service_stats (
			node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
			protocol TEXT NOT NULL,
			listen_address TEXT NOT NULL,
			listen_port INTEGER NOT NULL,
			backend_address TEXT NOT NULL DEFAULT '',
			backend_port INTEGER NOT NULL DEFAULT 0,
			connections INTEGER NOT NULL DEFAULT 0,
			incoming_packets INTEGER NOT NULL DEFAULT 0,
			outgoing_packets INTEGER NOT NULL DEFAULT 0,
			incoming_bytes INTEGER NOT NULL DEFAULT 0,
			outgoing_bytes INTEGER NOT NULL DEFAULT 0,
			collected_at TEXT NOT NULL,
			PRIMARY KEY(node_id, protocol, listen_address, listen_port, backend_address, backend_port)
		)`,
		`CREATE TABLE IF NOT EXISTS system_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("database migration: %w", err)
		}
	}
	for _, column := range []struct{ name, definition string }{
		{"observed_address", `TEXT NOT NULL DEFAULT ''`},
		{"apply_state", `TEXT NOT NULL DEFAULT 'waiting'`},
		{"online_since", `TEXT`},
		{"ram_used_percent", `REAL NOT NULL DEFAULT 0`},
		{"cpu_used_percent", `REAL NOT NULL DEFAULT 0`},
		{"load_1", `REAL NOT NULL DEFAULT 0`},
		{"cpu_cores", `INTEGER NOT NULL DEFAULT 0`},
		{"network_rx_bps", `INTEGER NOT NULL DEFAULT 0`},
		{"network_tx_bps", `INTEGER NOT NULL DEFAULT 0`},
		{"active_ips", `INTEGER NOT NULL DEFAULT 0`},
		{"metrics_collected_at", `TEXT`},
	} {
		if err := s.ensureColumn(ctx, "nodes", column.name, column.definition); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ensureColumn(ctx context.Context, table, name, definition string) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil { return err }
	found := false
	for rows.Next() {
		var cid int
		var columnName, columnType string
		var notNull, primaryKey int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &columnName, &columnType, &notNull, &defaultValue, &primaryKey); err != nil { rows.Close(); return err }
		if columnName == name { found = true }
	}
	if err := rows.Close(); err != nil { return err }
	if found { return nil }
	_, err = s.db.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN `+name+` `+definition)
	return err
}

func newToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil { return "", err }
	return hex.EncodeToString(buf), nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func NewID(prefix string) (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(buf), nil
}

func (s *Store) Bootstrap(ctx context.Context, ingressAddress string, initial domain.ProfileConfig, profileName string) error {
	count := 0
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM profiles`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	if err := initial.Validate(); err != nil {
		return fmt.Errorf("initial profile: %w", err)
	}
	if profileName == "" {
		profileName = "Default"
	}
	profileID, err := NewID("prf")
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO profiles(id,name,description,current_revision,created_at,updated_at) VALUES(?,?,?,?,?,?)`,
		profileID, profileName, "Initial EzhikLB profile", 1, formatTime(now), formatTime(now)); err != nil {
		return err
	}
	configJSON, _ := json.Marshal(initial)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO profile_revisions(profile_id,number,config_json,created_at) VALUES(?,?,?,?)`,
		profileID, 1, string(configJSON), formatTime(now)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO nodes(id,name,ingress_address,profile_id,desired_revision,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		"local", "Local node", ingressAddress, profileID, 1, "offline", formatTime(now), formatTime(now)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListProfiles(ctx context.Context) ([]domain.Profile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,description,current_revision,created_at,updated_at FROM profiles ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Profile
	for rows.Next() {
		profile, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, profile)
	}
	return result, rows.Err()
}

func (s *Store) GetProfile(ctx context.Context, id string) (domain.Profile, domain.Revision, error) {
	profile, err := scanProfile(s.db.QueryRowContext(ctx,
		`SELECT id,name,description,current_revision,created_at,updated_at FROM profiles WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Profile{}, domain.Revision{}, ErrNotFound
	}
	if err != nil {
		return domain.Profile{}, domain.Revision{}, err
	}
	revision, err := s.getRevision(ctx, id, profile.CurrentRevision)
	return profile, revision, err
}

func (s *Store) CreateProfile(ctx context.Context, name, description string, config domain.ProfileConfig) (domain.Profile, domain.Revision, error) {
	if err := config.Validate(); err != nil {
		return domain.Profile{}, domain.Revision{}, err
	}
	id, err := NewID("prf")
	if err != nil {
		return domain.Profile{}, domain.Revision{}, err
	}
	now := time.Now().UTC()
	data, _ := json.Marshal(config)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Profile{}, domain.Revision{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO profiles(id,name,description,current_revision,created_at,updated_at) VALUES(?,?,?,?,?,?)`,
		id, name, description, 1, formatTime(now), formatTime(now)); err != nil {
		return domain.Profile{}, domain.Revision{}, err
	}
	result, err := tx.ExecContext(ctx,
		`INSERT INTO profile_revisions(profile_id,number,config_json,created_at) VALUES(?,?,?,?)`,
		id, 1, string(data), formatTime(now))
	if err != nil {
		return domain.Profile{}, domain.Revision{}, err
	}
	revisionID, _ := result.LastInsertId()
	if err = auditTx(ctx, tx, "profile.created", "profile", id, map[string]any{"revision": 1}); err != nil {
		return domain.Profile{}, domain.Revision{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.Profile{}, domain.Revision{}, err
	}
	profile := domain.Profile{ID: id, Name: name, Description: description, CurrentRevision: 1, CreatedAt: now, UpdatedAt: now}
	revision := domain.Revision{ID: revisionID, ProfileID: id, Number: 1, Config: config, CreatedAt: now}
	return profile, revision, nil
}

func (s *Store) PublishRevision(ctx context.Context, profileID, name, description string, config domain.ProfileConfig) (domain.Profile, domain.Revision, error) {
	if err := config.Validate(); err != nil {
		return domain.Profile{}, domain.Revision{}, err
	}
	data, _ := json.Marshal(config)
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Profile{}, domain.Revision{}, err
	}
	defer tx.Rollback()
	var current int64
	if err = tx.QueryRowContext(ctx, `SELECT current_revision FROM profiles WHERE id=?`, profileID).Scan(&current); errors.Is(err, sql.ErrNoRows) {
		return domain.Profile{}, domain.Revision{}, ErrNotFound
	} else if err != nil {
		return domain.Profile{}, domain.Revision{}, err
	}
	next := current + 1
	result, err := tx.ExecContext(ctx,
		`INSERT INTO profile_revisions(profile_id,number,config_json,created_at) VALUES(?,?,?,?)`,
		profileID, next, string(data), formatTime(now))
	if err != nil {
		return domain.Profile{}, domain.Revision{}, err
	}
	if _, err = tx.ExecContext(ctx,
		`UPDATE profiles SET name=?,description=?,current_revision=?,updated_at=? WHERE id=?`,
		name, description, next, formatTime(now), profileID); err != nil {
		return domain.Profile{}, domain.Revision{}, err
	}
	if _, err = tx.ExecContext(ctx,
		`UPDATE nodes SET desired_revision=?,updated_at=? WHERE profile_id=?`, next, formatTime(now), profileID); err != nil {
		return domain.Profile{}, domain.Revision{}, err
	}
	if err = auditTx(ctx, tx, "profile.published", "profile", profileID, map[string]any{"revision": next}); err != nil {
		return domain.Profile{}, domain.Revision{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.Profile{}, domain.Revision{}, err
	}
	revisionID, _ := result.LastInsertId()
	profile, _, err := s.GetProfile(ctx, profileID)
	return profile, domain.Revision{ID: revisionID, ProfileID: profileID, Number: next, Config: config, CreatedAt: now}, err
}

func (s *Store) ListRevisions(ctx context.Context, profileID string) ([]domain.Revision, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,profile_id,number,config_json,created_at FROM profile_revisions WHERE profile_id=? ORDER BY number DESC`, profileID)
	if err != nil { return nil, err }
	defer rows.Close()
	result := make([]domain.Revision, 0)
	for rows.Next() {
		var item domain.Revision
		var configJSON, created string
		if err := rows.Scan(&item.ID, &item.ProfileID, &item.Number, &configJSON, &created); err != nil { return nil, err }
		if err := json.Unmarshal([]byte(configJSON), &item.Config); err != nil { return nil, err }
		item.CreatedAt, err = parseTime(created)
		if err != nil { return nil, err }
		result = append(result, item)
	}
	if len(result) == 0 { return nil, ErrNotFound }
	return result, rows.Err()
}

func (s *Store) RollbackProfile(ctx context.Context, profileID string, number int64) (domain.Profile, domain.Revision, error) {
	profile, _, err := s.GetProfile(ctx, profileID)
	if err != nil { return domain.Profile{}, domain.Revision{}, err }
	target, err := s.getRevision(ctx, profileID, number)
	if err != nil { return domain.Profile{}, domain.Revision{}, err }
	profile, revision, err := s.PublishRevision(ctx, profileID, profile.Name, profile.Description, target.Config)
	if err == nil { _ = s.Audit(ctx, "profile.rolled_back", "profile", profileID, map[string]any{"from_revision": number, "new_revision": revision.Number}) }
	return profile, revision, err
}

func (s *Store) CloneProfile(ctx context.Context, profileID, name string) (domain.Profile, domain.Revision, error) {
	profile, revision, err := s.GetProfile(ctx, profileID)
	if err != nil { return domain.Profile{}, domain.Revision{}, err }
	if name == "" { name = profile.Name + " — копия" }
	return s.CreateProfile(ctx, name, profile.Description, revision.Config)
}

func (s *Store) DeleteProfile(ctx context.Context, profileID string) error {
	var assigned int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes WHERE profile_id=?`, profileID).Scan(&assigned); err != nil { return err }
	if assigned > 0 { return fmt.Errorf("profile is assigned to %d node(s)", assigned) }
	result, err := s.db.ExecContext(ctx, `DELETE FROM profiles WHERE id=?`, profileID)
	if err != nil { return err }
	affected, _ := result.RowsAffected()
	if affected == 0 { return ErrNotFound }
	return s.Audit(ctx, "profile.deleted", "profile", profileID, map[string]any{})
}

func (s *Store) ListNodes(ctx context.Context) ([]domain.Node, error) {
	rows, err := s.db.QueryContext(ctx, nodeSelect+` ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var nodes []domain.Node
	for rows.Next() {
		node, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		if node.Status != "disabled" && node.Status != "deleting" && node.LastSeenAt == nil {
			node.Status = "connecting"
		} else if node.Status != "disabled" && node.Status != "deleting" && time.Since(*node.LastSeenAt) > 45*time.Second {
			node.Status = "offline"
			node.OnlineSince = nil
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

func (s *Store) CreateNode(ctx context.Context, name, ingressAddress, profileID string) (domain.Node, string, error) {
	var revision int64
	if err := s.db.QueryRowContext(ctx, `SELECT current_revision FROM profiles WHERE id=?`, profileID).Scan(&revision); errors.Is(err, sql.ErrNoRows) { return domain.Node{}, "", ErrNotFound } else if err != nil { return domain.Node{}, "", err }
	id, err := NewID("nod")
	if err != nil { return domain.Node{}, "", err }
	token, err := newToken()
	if err != nil { return domain.Node{}, "", err }
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil { return domain.Node{}, "", err }
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO nodes(id,name,ingress_address,profile_id,desired_revision,status,apply_state,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, id, name, ingressAddress, profileID, revision, "connecting", "waiting", formatTime(now), formatTime(now)); err != nil { return domain.Node{}, "", err }
	if _, err = tx.ExecContext(ctx, `INSERT INTO node_credentials(node_id,token_hash,created_at,rotated_at) VALUES(?,?,?,?)`, id, tokenHash(token), formatTime(now), formatTime(now)); err != nil { return domain.Node{}, "", err }
	if err = auditTx(ctx, tx, "node.created", "node", id, map[string]any{"profile_id": profileID}); err != nil { return domain.Node{}, "", err }
	if err = tx.Commit(); err != nil { return domain.Node{}, "", err }
	return domain.Node{ID:id, Name:name, IngressAddress:ingressAddress, ProfileID:profileID, DesiredRevision:revision, Status:"connecting", ApplyState:"waiting", CreatedAt:now, UpdatedAt:now}, token, nil
}

func (s *Store) ValidateNodeCredential(ctx context.Context, nodeID, token string) bool {
	var expected string
	if err := s.db.QueryRowContext(ctx, `SELECT c.token_hash FROM node_credentials c JOIN nodes n ON n.id=c.node_id WHERE c.node_id=? AND n.status<>'disabled'`, nodeID).Scan(&expected); err != nil { return false }
	return expected == tokenHash(token)
}

func (s *Store) NodeEnabled(ctx context.Context, nodeID string) bool {
	var enabled int
	return s.db.QueryRowContext(ctx, `SELECT CASE WHEN status='disabled' THEN 0 ELSE 1 END FROM nodes WHERE id=?`, nodeID).Scan(&enabled) == nil && enabled == 1
}

func (s *Store) RotateNodeCredential(ctx context.Context, nodeID string) (string, error) {
	token, err := newToken()
	if err != nil { return "", err }
	now := formatTime(time.Now().UTC())
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes WHERE id=? AND id<>'local'`, nodeID).Scan(&exists); err != nil { return "", err }
	if exists == 0 { return "", ErrNotFound }
	_, err = s.db.ExecContext(ctx, `INSERT INTO node_credentials(node_id,token_hash,created_at,rotated_at) VALUES(?,?,?,?) ON CONFLICT(node_id) DO UPDATE SET token_hash=excluded.token_hash,rotated_at=excluded.rotated_at`, nodeID, tokenHash(token), now, now)
	if err != nil { return "", err }
	_, _ = s.db.ExecContext(ctx, `UPDATE nodes SET status='offline',last_error='',updated_at=? WHERE id=?`, now, nodeID)
	_ = s.Audit(ctx, "node.credential_rotated", "node", nodeID, map[string]any{})
	return token, nil
}

func (s *Store) RevokeNodeCredential(ctx context.Context, nodeID string) error {
	if nodeID == "local" { return errors.New("local node credential cannot be revoked") }
	result, err := s.db.ExecContext(ctx, `DELETE FROM node_credentials WHERE node_id=?`, nodeID)
	if err != nil { return err }
	affected, _ := result.RowsAffected()
	if affected == 0 { return ErrNotFound }
	_, err = s.db.ExecContext(ctx, `UPDATE nodes SET status='disabled',updated_at=? WHERE id=?`, formatTime(time.Now().UTC()), nodeID)
	if err != nil { return err }
	return s.Audit(ctx, "node.credential_revoked", "node", nodeID, map[string]any{})
}

func (s *Store) SetNodeEnabled(ctx context.Context, nodeID string, enabled bool) error {
	status := "disabled"
	applyState := "disabled"
	if enabled {
		status = "connecting"
		applyState = "waiting"
	}
	result, err := s.db.ExecContext(ctx, `UPDATE nodes SET status=?,apply_state=?,last_error='',online_since=NULL,updated_at=? WHERE id=?`, status, applyState, formatTime(time.Now().UTC()), nodeID)
	if err != nil { return err }
	affected, _ := result.RowsAffected()
	if affected == 0 { return ErrNotFound }
	return s.Audit(ctx, "node.enabled_changed", "node", nodeID, map[string]any{"enabled": enabled})
}

func (s *Store) UpdateNode(ctx context.Context, nodeID, name, ingressAddress string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE nodes SET name=?,ingress_address=?,updated_at=? WHERE id=?`, name, ingressAddress, formatTime(time.Now().UTC()), nodeID)
	if err != nil { return err }
	affected, _ := result.RowsAffected()
	if affected == 0 { return ErrNotFound }
	return s.Audit(ctx, "node.updated", "node", nodeID, map[string]any{"name": name, "ingress_address": ingressAddress})
}

func (s *Store) DeleteNode(ctx context.Context, nodeID string) error {
	if nodeID == "local" { return errors.New("local node cannot be deleted") }
	result, err := s.db.ExecContext(ctx, `UPDATE nodes SET status='deleting',apply_state='decommissioning',last_error='',updated_at=? WHERE id=?`, formatTime(time.Now().UTC()), nodeID)
	if err != nil { return err }
	affected, _ := result.RowsAffected()
	if affected == 0 { return ErrNotFound }
	return s.Audit(ctx, "node.decommission_requested", "node", nodeID, map[string]any{})
}

func (s *Store) AssignProfile(ctx context.Context, nodeID, profileID string) error {
	var revision int64
	if err := s.db.QueryRowContext(ctx, `SELECT current_revision FROM profiles WHERE id=?`, profileID).Scan(&revision); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx,
		`UPDATE nodes SET profile_id=?,desired_revision=?,updated_at=? WHERE id=?`, profileID, revision, formatTime(time.Now().UTC()), nodeID)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return s.Audit(ctx, "node.profile_assigned", "node", nodeID, map[string]any{"profile_id": profileID, "revision": revision})
}

func (s *Store) DesiredState(ctx context.Context, nodeID string) (domain.NodeDesiredState, error) {
	var result domain.NodeDesiredState
	var configJSON string
	err := s.db.QueryRowContext(ctx, `
		SELECT n.id,n.ingress_address,n.desired_revision,p.id,p.name,COALESCE(q.nonce,0),n.status='deleting',r.config_json
		FROM nodes n
		JOIN profiles p ON p.id=n.profile_id
		JOIN profile_revisions r ON r.profile_id=p.id AND r.number=n.desired_revision
		LEFT JOIN node_probe_requests q ON q.node_id=n.id
		WHERE n.id=? AND n.status<>'disabled'`, nodeID).Scan(&result.NodeID, &result.IngressAddress, &result.Revision, &result.ProfileID, &result.ProfileName, &result.HealthProbe, &result.Decommission, &configJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal([]byte(configJSON), &result.Config); err != nil {
		return result, err
	}
	return result, nil
}

func (s *Store) RequestHealthProbe(ctx context.Context, nodeID string) (int64, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes WHERE id=?`, nodeID).Scan(&exists); err != nil { return 0, err }
	if exists == 0 { return 0, ErrNotFound }
	if _, err := s.db.ExecContext(ctx, `INSERT INTO node_probe_requests(node_id,nonce) VALUES(?,1) ON CONFLICT(node_id) DO UPDATE SET nonce=nonce+1`, nodeID); err != nil { return 0, err }
	var nonce int64
	if err := s.db.QueryRowContext(ctx, `SELECT nonce FROM node_probe_requests WHERE node_id=?`, nodeID).Scan(&nonce); err != nil { return 0, err }
	_ = s.Audit(ctx, "node.health_probe_requested", "node", nodeID, map[string]any{"nonce": nonce})
	return nonce, nil
}

func (s *Store) Heartbeat(ctx context.Context, nodeID, version, observedAddress, applyState string, applied int64, applyError string, health []domain.BackendHealth, stats []domain.ServiceStat, metrics domain.NodeMetrics, decommissioned bool) error {
	status := "online"
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previousStatus string
	var previousSeen, previousOnline sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT status,last_seen_at,online_since FROM nodes WHERE id=? AND status<>'disabled'`, nodeID).Scan(&previousStatus, &previousSeen, &previousOnline); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if decommissioned && previousStatus == "deleting" {
		if err := auditTx(ctx, tx, "node.decommissioned", "node", nodeID, map[string]any{"agent_version": version}); err != nil { return err }
		if _, err := tx.ExecContext(ctx, `DELETE FROM nodes WHERE id=?`, nodeID); err != nil { return err }
		return tx.Commit()
	}
	if previousStatus == "deleting" {
		status = "deleting"
		applyState = "decommissioning"
	}
	onlineSince := previousOnline.String
	if !previousOnline.Valid || !previousSeen.Valid || previousStatus == "offline" || previousStatus == "connecting" {
		onlineSince = formatTime(now)
	} else if parsed, parseErr := parseTime(previousSeen.String); parseErr != nil || now.Sub(parsed) > 45*time.Second {
		onlineSince = formatTime(now)
	}
	if applyState == "" {
		if applyError != "" { applyState = "error" } else if applied > 0 { applyState = "applied" } else { applyState = "waiting" }
	}
	metricsAt := formatTime(metrics.CollectedAt)
	if metrics.CollectedAt.IsZero() { metricsAt = formatTime(now) }
	result, err := tx.ExecContext(ctx, `UPDATE nodes SET applied_revision=?,agent_version=?,observed_address=CASE WHEN ?='' THEN observed_address ELSE ? END,status=?,apply_state=?,last_seen_at=?,online_since=?,last_error=?,ram_used_percent=?,cpu_used_percent=?,load_1=?,cpu_cores=?,network_rx_bps=?,network_tx_bps=?,active_ips=?,metrics_collected_at=?,updated_at=? WHERE id=? AND status<>'disabled'`,
		applied, version, observedAddress, observedAddress, status, applyState, formatTime(now), onlineSince, applyError, metrics.RAMUsedPercent, metrics.CPUUsedPercent, metrics.Load1, metrics.CPUCores, metrics.NetworkRxBPS, metrics.NetworkTxBPS, metrics.ActiveIPs, metricsAt, formatTime(now), nodeID)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM backend_health WHERE node_id=?`, nodeID); err != nil {
		return err
	}
	for _, item := range health {
		if _, err := tx.ExecContext(ctx, `INSERT INTO backend_health(node_id,address,state,consecutive_successes,consecutive_failures,latency_millis,checked_at)
			VALUES(?,?,?,?,?,?,?) ON CONFLICT(node_id,address) DO UPDATE SET state=excluded.state,consecutive_successes=excluded.consecutive_successes,consecutive_failures=excluded.consecutive_failures,latency_millis=excluded.latency_millis,checked_at=excluded.checked_at`,
			nodeID, item.Address, item.State, item.ConsecutiveUp, item.ConsecutiveDown, item.LatencyMillis, formatTime(item.CheckedAt)); err != nil {
			return err
		}
	}
	if stats != nil {
		if _, err := tx.ExecContext(ctx, `DELETE FROM service_stats WHERE node_id=?`, nodeID); err != nil {
			return err
		}
		for _, item := range stats {
			if _, err := tx.ExecContext(ctx, `INSERT INTO service_stats(node_id,protocol,listen_address,listen_port,backend_address,backend_port,connections,incoming_packets,outgoing_packets,incoming_bytes,outgoing_bytes,collected_at)
				VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, nodeID, item.Protocol, item.ListenAddress, item.ListenPort, item.BackendAddress, item.BackendPort, item.Connections, item.IncomingPackets, item.OutgoingPackets, item.IncomingBytes, item.OutgoingBytes, formatTime(item.CollectedAt)); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) ListHealth(ctx context.Context) ([]domain.BackendHealth, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT node_id,address,state,consecutive_successes,consecutive_failures,latency_millis,checked_at FROM backend_health ORDER BY node_id,address`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.BackendHealth, 0)
	for rows.Next() {
		var item domain.BackendHealth
		var checked string
		if err := rows.Scan(&item.NodeID, &item.Address, &item.State, &item.ConsecutiveUp, &item.ConsecutiveDown, &item.LatencyMillis, &checked); err != nil {
			return nil, err
		}
		item.CheckedAt, err = parseTime(checked)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) ListStats(ctx context.Context) ([]domain.ServiceStat, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT node_id,protocol,listen_address,listen_port,backend_address,backend_port,connections,incoming_packets,outgoing_packets,incoming_bytes,outgoing_bytes,collected_at FROM service_stats ORDER BY node_id,protocol,listen_address,listen_port,backend_address,backend_port`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.ServiceStat, 0)
	for rows.Next() {
		var item domain.ServiceStat
		var collected string
		if err := rows.Scan(&item.NodeID, &item.Protocol, &item.ListenAddress, &item.ListenPort, &item.BackendAddress, &item.BackendPort, &item.Connections, &item.IncomingPackets, &item.OutgoingPackets, &item.IncomingBytes, &item.OutgoingBytes, &collected); err != nil {
			return nil, err
		}
		item.CollectedAt, err = parseTime(collected)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

type scanner interface{ Scan(...any) error }

func scanProfile(row scanner) (domain.Profile, error) {
	var p domain.Profile
	var created, updated string
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.CurrentRevision, &created, &updated)
	if err != nil {
		return p, err
	}
	p.CreatedAt, err = parseTime(created)
	if err == nil {
		p.UpdatedAt, err = parseTime(updated)
	}
	return p, err
}

func (s *Store) getRevision(ctx context.Context, profileID string, number int64) (domain.Revision, error) {
	var r domain.Revision
	var configJSON, created string
	err := s.db.QueryRowContext(ctx,
		`SELECT id,profile_id,number,config_json,created_at FROM profile_revisions WHERE profile_id=? AND number=?`, profileID, number).
		Scan(&r.ID, &r.ProfileID, &r.Number, &configJSON, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal([]byte(configJSON), &r.Config); err != nil {
		return r, err
	}
	r.CreatedAt, err = parseTime(created)
	return r, err
}

const nodeSelect = `SELECT id,name,ingress_address,observed_address,COALESCE(profile_id,''),desired_revision,applied_revision,agent_version,status,apply_state,last_seen_at,online_since,last_error,ram_used_percent,cpu_used_percent,load_1,cpu_cores,network_rx_bps,network_tx_bps,active_ips,metrics_collected_at,created_at,updated_at FROM nodes`

func scanNode(row scanner) (domain.Node, error) {
	var n domain.Node
	var lastSeen, onlineSince, metricsAt sql.NullString
	var created, updated string
	var metrics domain.NodeMetrics
	err := row.Scan(&n.ID, &n.Name, &n.IngressAddress, &n.ObservedAddress, &n.ProfileID, &n.DesiredRevision, &n.AppliedRevision, &n.AgentVersion, &n.Status, &n.ApplyState, &lastSeen, &onlineSince, &n.LastError, &metrics.RAMUsedPercent, &metrics.CPUUsedPercent, &metrics.Load1, &metrics.CPUCores, &metrics.NetworkRxBPS, &metrics.NetworkTxBPS, &metrics.ActiveIPs, &metricsAt, &created, &updated)
	if err != nil {
		return n, err
	}
	if lastSeen.Valid {
		parsed, parseErr := parseTime(lastSeen.String)
		if parseErr != nil {
			return n, parseErr
		}
		n.LastSeenAt = &parsed
	}
	if onlineSince.Valid && onlineSince.String != "" {
		parsed, parseErr := parseTime(onlineSince.String)
		if parseErr != nil { return n, parseErr }
		n.OnlineSince = &parsed
	}
	if metricsAt.Valid && metricsAt.String != "" {
		parsed, parseErr := parseTime(metricsAt.String)
		if parseErr != nil { return n, parseErr }
		metrics.CollectedAt = parsed
		n.Metrics = &metrics
	}
	n.CreatedAt, err = parseTime(created)
	if err == nil {
		n.UpdatedAt, err = parseTime(updated)
	}
	return n, err
}

func (s *Store) GetSystemSettings(ctx context.Context, defaults domain.SystemSettings) (domain.SystemSettings, error) {
	result := defaults
	rows, err := s.db.QueryContext(ctx, `SELECT key,value FROM system_settings WHERE key IN ('panel_port','agent_port','legacy_panel_port','legacy_agent_port')`)
	if err != nil { return result, err }
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil { return result, err }
		var parsed int
		if _, err := fmt.Sscan(value, &parsed); err != nil { continue }
		if key == "panel_port" { result.PanelPort = parsed }
		if key == "agent_port" { result.AgentPort = parsed }
		if key == "legacy_panel_port" { result.LegacyPanelPort = parsed }
		if key == "legacy_agent_port" { result.LegacyAgentPort = parsed }
	}
	return result, rows.Err()
}

func (s *Store) UpdateSystemSettings(ctx context.Context, settings domain.SystemSettings) error {
	if settings.PanelPort < 1024 || settings.PanelPort > 65535 || settings.AgentPort < 1024 || settings.AgentPort > 65535 {
		return errors.New("ports must be between 1024 and 65535")
	}
	if settings.LegacyPanelPort < 0 || settings.LegacyPanelPort > 65535 || settings.LegacyAgentPort < 0 || settings.LegacyAgentPort > 65535 { return errors.New("legacy port is invalid") }
	if settings.PanelPort == settings.AgentPort { return errors.New("panel and agent ports must be different") }
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil { return err }
	defer tx.Rollback()
	now := formatTime(time.Now().UTC())
	for key, value := range map[string]int{"panel_port": settings.PanelPort, "agent_port": settings.AgentPort, "legacy_panel_port": settings.LegacyPanelPort, "legacy_agent_port": settings.LegacyAgentPort} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO system_settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, fmt.Sprint(value), now); err != nil { return err }
	}
	if err := auditTx(ctx, tx, "settings.updated", "system", "network", settings); err != nil { return err }
	return tx.Commit()
}

func (s *Store) Audit(ctx context.Context, action, targetType, targetID string, details any) error {
	data, _ := json.Marshal(details)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_events(action,target_type,target_id,details_json,created_at) VALUES(?,?,?,?,?)`,
		action, targetType, targetID, string(data), formatTime(time.Now().UTC()))
	return err
}

func auditTx(ctx context.Context, tx *sql.Tx, action, targetType, targetID string, details any) error {
	data, _ := json.Marshal(details)
	_, err := tx.ExecContext(ctx,
		`INSERT INTO audit_events(action,target_type,target_id,details_json,created_at) VALUES(?,?,?,?,?)`,
		action, targetType, targetID, string(data), formatTime(time.Now().UTC()))
	return err
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
func parseTime(value string) (time.Time, error) { return time.Parse(time.RFC3339Nano, value) }
