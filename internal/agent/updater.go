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
	"strings"
)

// InstallAgentUpdate downloads an official release, verifies SHA-256 and
// atomically replaces only the agent binary. No command comes from the panel.
func InstallAgentUpdate(ctx context.Context, version string) error {
	if version == "" || strings.ContainsAny(version, `/\\ \t\r\n`) { return fmt.Errorf("invalid update version") }
	asset := fmt.Sprintf("ezhiklb_%s_linux_amd64.tar.gz", version)
	base := fmt.Sprintf("https://github.com/ezhikdev/ezhiklb/releases/download/v%s/", version)
	archive, err := download(ctx, base+asset, 256<<20); if err != nil { return err }
	checksum, err := download(ctx, base+asset+".sha256", 4096); if err != nil { return err }
	want := strings.Fields(string(checksum)); if len(want) == 0 { return fmt.Errorf("empty checksum file") }
	got := sha256.Sum256(archive); if !strings.EqualFold(hex.EncodeToString(got[:]), want[0]) { return fmt.Errorf("release checksum mismatch") }
	binary, err := extractAgent(archive); if err != nil { return err }
	current, err := os.Executable(); if err != nil { return err }
	tmp, err := os.CreateTemp(filepath.Dir(current), ".ezhiklb-agent-update-*"); if err != nil { return err }
	tmpName := tmp.Name(); defer os.Remove(tmpName)
	if _, err = tmp.Write(binary); err == nil { err = tmp.Sync() }
	if closeErr := tmp.Close(); err == nil { err = closeErr }; if err != nil { return err }
	if err = os.Chmod(tmpName, 0755); err != nil { return err }
	return os.Rename(tmpName, current)
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
