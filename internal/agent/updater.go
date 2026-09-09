package agent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var releaseVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z]+(?:\.[0-9A-Za-z]+)*)?$`)

// versionFilePath mirrors scripts/install.sh's fixed VERSION_FILE
// (CONFIG_DIR=/etc/ezhiklb, not configurable via any env var there either).
// Kept in sync here so a self-update doesn't leave it pointing at whatever
// version install.sh was last run with — otherwise a later manual install.sh
// run misreports "Existing EzhikLB <stale version> detected" even though the
// node has been self-updated several versions past that point.
const versionFilePath = "/etc/ezhiklb/version"

// Update stage names reported to the panel via heartbeat while an update is
// in progress, so the UI can show real (coarse-grained) progress instead of
// a single opaque "updating" state.
const (
	UpdateStageDownloading = "downloading"
	UpdateStageVerifying   = "verifying"
	UpdateStageInstalling  = "installing"
)

// InstallAgentUpdate downloads an official release, verifies SHA-256 and
// atomically replaces only the agent binary. No command comes from the panel.
// onStage, if non-nil, is called as each stage starts so the caller can
// report progress upstream before the (possibly slow) step runs.
func InstallAgentUpdate(ctx context.Context, version string, onStage func(stage string)) error {
	if !releaseVersionPattern.MatchString(version) { return fmt.Errorf("invalid update version %q", version) }
	if onStage == nil { onStage = func(string) {} }
	asset := fmt.Sprintf("ezhiklb_%s_linux_amd64.tar.gz", version)
	base := fmt.Sprintf("https://github.com/ezhikdev/ezhiklb/releases/download/v%s/", version)
	onStage(UpdateStageDownloading)
	archive, err := download(ctx, base+asset, 256<<20); if err != nil { return err }
	checksum, err := download(ctx, base+asset+".sha256", 4096); if err != nil { return err }
	onStage(UpdateStageVerifying)
	want := strings.Fields(string(checksum)); if len(want) == 0 { return fmt.Errorf("empty checksum file") }
	got := sha256.Sum256(archive); if !strings.EqualFold(hex.EncodeToString(got[:]), want[0]) { return fmt.Errorf("release checksum mismatch") }
	onStage(UpdateStageInstalling)
	binary, err := extractAgent(archive); if err != nil { return err }
	current, err := os.Executable(); if err != nil { return err }
	tmp, err := os.CreateTemp(filepath.Dir(current), ".ezhiklb-agent-update-*"); if err != nil { return err }
	tmpName := tmp.Name(); defer os.Remove(tmpName)
	if _, err = tmp.Write(binary); err == nil { err = tmp.Sync() }
	if closeErr := tmp.Close(); err == nil { err = closeErr }; if err != nil { return err }
	if err = os.Chmod(tmpName, 0755); err != nil { return err }
	if err := os.Rename(tmpName, current); err != nil { return err }
	// Best-effort: the binary swap above is what actually matters and has
	// already succeeded, so a failure writing this bookkeeping file must
	// not be reported as a failed update.
	_ = os.WriteFile(versionFilePath, []byte(version+"\n"), 0644)
	return nil
}

func download(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil); if err != nil { return nil, err }
	res, err := http.DefaultClient.Do(req); if err != nil { return nil, err }; defer res.Body.Close()
	if res.StatusCode != http.StatusOK { return nil, fmt.Errorf("download %s: %s", url, res.Status) }
	return io.ReadAll(io.LimitReader(res.Body, limit))
}

func extractAgent(data []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data)); if err != nil { return nil, err }; defer gz.Close()
	tr := tar.NewReader(gz)
	for { header, err := tr.Next(); if err == io.EOF { break }; if err != nil { return nil, err }
		if filepath.Base(header.Name) == "ezhiklb-agent" && header.Typeflag == tar.TypeReg { return io.ReadAll(io.LimitReader(tr, 128<<20)) }
	}
	return nil, fmt.Errorf("ezhiklb-agent is missing from release archive")
}
