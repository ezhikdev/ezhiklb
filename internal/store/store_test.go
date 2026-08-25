package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ezhik-lb/ezhiklb/internal/domain"
)

func TestResolveVersion(t *testing.T) {
	tests := []struct {
		name      string
		automatic bool
		requested string
		number    int64
		want      string
		wantError bool
	}{
		{name: "automatic", automatic: true, requested: "ignored", number: 3, want: "v3"},
		{name: "manual", requested: "release-1.2", number: 4, want: "release-1.2"},
		{name: "reject underscore", requested: "release_1", number: 4, wantError: true},
		{name: "reject Cyrillic", requested: "версия-1", number: 4, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveVersion(test.automatic, test.requested, test.number)
			if test.wantError {
				if err == nil { t.Fatal("expected validation error") }
				return
			}
			if err != nil { t.Fatal(err) }
			if got != test.want { t.Fatalf("version = %q, want %q", got, test.want) }
		})
	}
}

func TestProfileVersionsAndAuditRetention(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "ezhiklb.db"))
	if err != nil { t.Fatal(err) }
	defer s.Close()
	if err := s.Bootstrap(ctx, "198.51.100.10", domain.DefaultProfileConfig(), "Default"); err != nil { t.Fatal(err) }
	profiles, err := s.ListProfiles(ctx)
	if err != nil { t.Fatal(err) }
	if len(profiles) != 1 || profiles[0].Version != "v1" || !profiles[0].AutoVersion { t.Fatalf("unexpected bootstrap profile: %#v", profiles) }
	profile, revision, err := s.PublishRevision(ctx, profiles[0].ID, profiles[0].Name, profiles[0].Description, domain.DefaultProfileConfig(), true, "")
	if err != nil { t.Fatal(err) }
	if profile.Version != "v2" || revision.Version != "v2" { t.Fatalf("automatic version = %q/%q, want v2", profile.Version, revision.Version) }
	if _, _, err := s.PublishRevision(ctx, profile.ID, profile.Name, profile.Description, domain.DefaultProfileConfig(), false, "v2"); err == nil { t.Fatal("expected unchanged manual version to fail") }
	profile, _, err = s.PublishRevision(ctx, profile.ID, profile.Name, profile.Description, domain.DefaultProfileConfig(), false, "vpn-2026.08")
	if err != nil { t.Fatal(err) }
	if profile.Version != "vpn-2026.08" || profile.AutoVersion { t.Fatalf("unexpected manual profile: %#v", profile) }

	if _, err := s.db.ExecContext(ctx, `INSERT INTO audit_events(action,target_type,target_id,details_json,created_at) VALUES(?,?,?,?,?)`, "node.apply_failed", "node", "old", `{}`, formatTime(time.Now().UTC().Add(-15*24*time.Hour))); err != nil { t.Fatal(err) }
	if err := s.Audit(ctx, "node.apply_failed", "node", "current", map[string]any{"error": "test"}); err != nil { t.Fatal(err) }
	events, err := s.ListAudit(ctx, "errors", 200)
	if err != nil { t.Fatal(err) }
	if len(events) != 1 || events[0].TargetID != "current" { t.Fatalf("unexpected retained errors: %#v", events) }
}
