package store

import (
	"context"
	"crypto/rand"
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
			profile_id TEXT REFERENCES profiles(id) ON DELETE SET NULL,
			desired_revision INTEGER NOT NULL DEFAULT 0,
			applied_revision INTEGER NOT NULL DEFAULT 0,
			agent_version TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'offline',
			last_seen_at TEXT,
			last_error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
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
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("database migration: %w", err)
		}
	}
	return nil
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
		if node.LastSeenAt == nil || time.Since(*node.LastSeenAt) > 20*time.Second {
			node.Status = "offline"
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
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
		SELECT n.id,n.ingress_address,n.desired_revision,p.id,p.name,r.config_json
		FROM nodes n
		JOIN profiles p ON p.id=n.profile_id
		JOIN profile_revisions r ON r.profile_id=p.id AND r.number=n.desired_revision
		WHERE n.id=?`, nodeID).Scan(&result.NodeID, &result.IngressAddress, &result.Revision, &result.ProfileID, &result.ProfileName, &configJSON)
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

func (s *Store) Heartbeat(ctx context.Context, nodeID, version string, applied int64, applyError string, health []domain.BackendHealth) error {
	status := "online"
	if applyError != "" {
		status = "error"
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE nodes SET applied_revision=?,agent_version=?,status=?,last_seen_at=?,last_error=?,updated_at=? WHERE id=?`,
		applied, version, status, formatTime(now), applyError, formatTime(now), nodeID)
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
	return tx.Commit()
}

func (s *Store) ListHealth(ctx context.Context) ([]domain.BackendHealth, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT node_id,address,state,consecutive_successes,consecutive_failures,latency_millis,checked_at FROM backend_health ORDER BY node_id,address`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.BackendHealth
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

const nodeSelect = `SELECT id,name,ingress_address,COALESCE(profile_id,''),desired_revision,applied_revision,agent_version,status,last_seen_at,last_error,created_at,updated_at FROM nodes`

func scanNode(row scanner) (domain.Node, error) {
	var n domain.Node
	var lastSeen sql.NullString
	var created, updated string
	err := row.Scan(&n.ID, &n.Name, &n.IngressAddress, &n.ProfileID, &n.DesiredRevision, &n.AppliedRevision, &n.AgentVersion, &n.Status, &lastSeen, &n.LastError, &created, &updated)
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
	n.CreatedAt, err = parseTime(created)
	if err == nil {
		n.UpdatedAt, err = parseTime(updated)
	}
	return n, err
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
