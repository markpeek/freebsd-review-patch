package prepare

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// LoadManifest accepts the helper's own saved layout, not arbitrary paths from
// an unchecked JSON document. VerifyBaseline must also run before reviewing.
func LoadManifest(name string, maxBytes int64) (*Manifest, error) {
	if maxBytes <= 0 || maxBytes > 1<<30 {
		return nil, errors.New("invalid max-bytes")
	}
	abs, err := filepath.Abs(name)
	if err != nil {
		return nil, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	data, err := readLimited(abs, maxBytes)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("decode preparation manifest: %w", err)
	}
	if m.Schema != 1 || m.State != "ready" || m.ManifestPath != abs || m.Directory != filepath.Dir(abs) {
		return nil, errors.New("manifest must describe a completed preparation in its original artifact directory")
	}
	if m.Applicability != "applies" && m.Applicability != "conflicts" {
		return nil, errors.New("manifest has an unsupported applicability state")
	}
	review, err := parseIdentifier(m.Review.URL)
	if err != nil || review.Source != m.Review.Source || review.ID != m.Review.ID {
		return nil, errors.New("invalid review identity in manifest")
	}
	if (m.Review.Source == "differential" && !digits.MatchString(m.Review.DiffID)) || (m.Review.Source == "github" && (!sha.MatchString(m.Review.Base) || !sha.MatchString(m.Review.Head))) {
		return nil, errors.New("invalid pinned review version in manifest")
	}
	if m.Strip < 0 || m.Strip > 1 || m.FileCount < 0 || !sha256Hex.MatchString(m.SHA256) {
		return nil, errors.New("invalid patch metadata in manifest")
	}
	if !sha.MatchString(m.Baseline.Head) || !filepath.IsAbs(m.Baseline.Root) {
		return nil, errors.New("invalid checkout baseline in manifest")
	}
	repo, err := filepath.EvalSymlinks(m.Baseline.Root)
	if err != nil || repo != m.Baseline.Root || within(repo, m.Directory) {
		return nil, errors.New("manifest artifacts must be outside the canonical source checkout")
	}
	for _, field := range []struct{ actual, relative string }{
		{m.Patch, "review.patch"}, {m.Files, "files.json"}, {m.Context, "context"},
		{m.Diagnostics, "apply-check.txt"}, {m.Baseline.Status, "status.z"}, {m.Baseline.Changes, "local.patch"},
	} {
		want := filepath.Join(m.Directory, field.relative)
		resolved, err := filepath.EvalSymlinks(field.actual)
		if field.actual != want || err != nil || resolved != want {
			return nil, fmt.Errorf("invalid artifact path for %s", field.relative)
		}
	}
	if m.Workspace != "" {
		want := filepath.Join(m.Directory, "proposed")
		resolved, err := filepath.EvalSymlinks(m.Workspace)
		if m.Workspace != want || err != nil || resolved != want {
			return nil, errors.New("invalid proposed workspace path")
		}
	}
	if err := validateWorkspaceFiles(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

func readLimited(path string, maxBytes int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("artifact exceeds max-bytes: %s", path)
	}
	return data, nil
}

// VerifyBaseline detects stale/replaced artifacts and local changes before and
// after a headless review. It never restores or overwrites the source checkout.
func VerifyBaseline(ctx context.Context, m *Manifest, timeout time.Duration, maxBytes int64) error {
	if timeout <= 0 || maxBytes <= 0 || maxBytes > 1<<30 {
		return errors.New("invalid verification limits")
	}
	r := &runner{ctx: ctx, cfg: Config{Repo: m.Baseline.Root, Timeout: timeout, MaxBytes: maxBytes}, m: m}
	current, err := r.baseline()
	if err != nil {
		return err
	}
	if current.info.Head != m.Baseline.Head || current.info.Branch != m.Baseline.Branch || current.info.Dirty != m.Baseline.Dirty {
		return errors.New("source baseline changed; prepare the review again")
	}
	for name, actual := range map[string][]byte{
		"status.z": current.status, "local.patch": current.changes,
		"staged.patch": current.staged, "unstaged.patch": current.unstaged,
	} {
		saved, err := readLimited(filepath.Join(m.Directory, name), maxBytes)
		if err != nil {
			return err
		}
		if string(saved) != string(actual) {
			return errors.New("source baseline changed; prepare the review again")
		}
	}
	patch, err := readLimited(m.Patch, maxBytes)
	if err != nil {
		return err
	}
	if fmt.Sprintf("%x", sha256.Sum256(patch)) != m.SHA256 {
		return errors.New("prepared patch hash does not match the manifest")
	}
	stats, err := r.git(m.Baseline.Root, "apply", fmt.Sprintf("-p%d", m.Strip), "--numstat", "-z", "--", m.Patch)
	if err != nil {
		return err
	}
	files, err := parseNumstat(stats)
	if err != nil {
		return err
	}
	savedFiles, err := readLimited(m.Files, maxBytes)
	if err != nil {
		return err
	}
	var expected []changedFile
	if err := json.Unmarshal(savedFiles, &expected); err != nil {
		return err
	}
	actualJSON, err := json.Marshal(files)
	if err != nil {
		return err
	}
	expectedJSON, err := json.Marshal(expected)
	if err != nil {
		return err
	}
	if m.FileCount != len(files) || !bytes.Equal(actualJSON, expectedJSON) {
		return errors.New("changed-file metadata does not match the prepared patch")
	}
	return verifyWorkspace(ctx, m, maxBytes)
}

// SaveManifest atomically updates the manifest without damaging a previous
// successful preparation if writing the optional review state fails.
func SaveManifest(m *Manifest) (err error) {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(m.Directory, ".manifest-*")
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), m.ManifestPath)
}
