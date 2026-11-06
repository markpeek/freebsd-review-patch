// Package prepare implements read-only review retrieval and patch preparation.
package prepare

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Config controls local preparation. Client is injectable for offline tests.
type Config struct {
	Identifier  string
	Repo        string
	Output      string
	Timeout     time.Duration
	MaxBytes    int64
	Snapshot    bool
	GitHubToken string
	Client      *http.Client
	Progress    func(string)
}

type Review struct {
	Source string `json:"source"`
	ID     string `json:"id"`
	URL    string `json:"url"`
	DiffID string `json:"diff_id,omitempty"`
	Base   string `json:"base_sha,omitempty"`
	Head   string `json:"head_sha,omitempty"`
}

type Baseline struct {
	Root    string `json:"root"`
	Head    string `json:"head"`
	Branch  string `json:"branch"`
	Dirty   bool   `json:"dirty"`
	Status  string `json:"status_path"`
	Changes string `json:"changes_path"`
}

// Manifest deliberately omits source, patch, description, and comment contents.
// They are untrusted input and can be large; read the saved artifacts as needed.
type Manifest struct {
	Schema         int                      `json:"schema"`
	State          string                   `json:"state"`
	Directory      string                   `json:"directory"`
	ManifestPath   string                   `json:"manifest_path"`
	Review         Review                   `json:"review"`
	Baseline       Baseline                 `json:"baseline"`
	RetrievedAt    string                   `json:"retrieved_at,omitempty"`
	Patch          string                   `json:"patch_path,omitempty"`
	SHA256         string                   `json:"sha256,omitempty"`
	Context        string                   `json:"context_path,omitempty"`
	Files          string                   `json:"files_path,omitempty"`
	FileCount      int                      `json:"file_count"`
	Strip          int                      `json:"strip"`
	Applicability  string                   `json:"applicability"`
	Diagnostics    string                   `json:"diagnostics_path,omitempty"`
	Workspace      string                   `json:"workspace_path,omitempty"`
	WorkspaceFiles map[string]WorkspaceFile `json:"workspace_files"`
	Warnings       []string                 `json:"warnings,omitempty"`
	Error          string                   `json:"error,omitempty"`
	Analysis       *Analysis                `json:"analysis,omitempty"`
	// Previous lists earlier review attempts, oldest first.
	Previous []*Analysis `json:"previous_analyses,omitempty"`
}

// Analysis describes an optional headless review without inlining its content.
type Analysis struct {
	State       string `json:"state"`
	Reviewer    string `json:"reviewer"`
	Model       string `json:"model,omitempty"`
	Directory   string `json:"directory"`
	Prompt      string `json:"prompt_path"`
	Schema      string `json:"schema_path"`
	Events      string `json:"events_path"`
	Diagnostics string `json:"diagnostics_path"`
	Findings    string `json:"findings_path,omitempty"`
	Report      string `json:"report_path,omitempty"`
	Count       *int   `json:"finding_count,omitempty"`
	StartedAt   string `json:"started_at"`
	FinishedAt  string `json:"finished_at,omitempty"`
	Error       string `json:"error,omitempty"`
}

type runner struct {
	ctx      context.Context
	cfg      Config
	m        *Manifest
	expected []remoteFile
}

func (r *runner) progress(message string) {
	if r.cfg.Progress != nil {
		r.cfg.Progress(message)
	}
}

var (
	dNumber   = regexp.MustCompile(`^D([1-9][0-9]*)$`)
	prNumber  = regexp.MustCompile(`^(?:PR\s*)?#([1-9][0-9]*)$`)
	digits    = regexp.MustCompile(`^[1-9][0-9]*$`)
	sha       = regexp.MustCompile(`^[0-9a-f]{40}$`)
	sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func parseIdentifier(input string) (Review, error) {
	input = strings.TrimSpace(input)
	if match := dNumber.FindStringSubmatch(input); match != nil {
		return Review{Source: "differential", ID: input, URL: "https://reviews.freebsd.org/" + input}, nil
	}
	if match := prNumber.FindStringSubmatch(input); match != nil {
		return Review{Source: "github", ID: match[1], URL: "https://github.com/freebsd/freebsd-src/pull/" + match[1]}, nil
	}
	u, err := url.Parse(input)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return Review{}, errors.New("expected D-number, PR #number, or HTTPS FreeBSD review/PR URL")
	}
	if u.Host == "reviews.freebsd.org" {
		id := strings.Trim(u.Path, "/")
		if !dNumber.MatchString(id) {
			return Review{}, errors.New("invalid Differential URL")
		}
		diff := u.Query().Get("id")
		if diff != "" && !digits.MatchString(diff) {
			return Review{}, errors.New("invalid Differential diff ID")
		}
		return Review{Source: "differential", ID: id, URL: "https://reviews.freebsd.org/" + id, DiffID: diff}, nil
	}
	if u.Host == "github.com" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) < 4 || parts[0] != "freebsd" || parts[1] != "freebsd-src" || parts[2] != "pull" {
			return Review{}, errors.New("only pull requests in freebsd/freebsd-src are supported")
		}
		id := strings.TrimSuffix(strings.TrimSuffix(parts[3], ".diff"), ".patch")
		if !digits.MatchString(id) || len(parts) > 5 || (len(parts) == 5 && parts[4] != "files" && parts[4] != "commits" && parts[4] != "checks") {
			return Review{}, errors.New("invalid GitHub pull request URL")
		}
		return Review{Source: "github", ID: id, URL: "https://github.com/freebsd/freebsd-src/pull/" + id}, nil
	}
	return Review{}, errors.New("unsupported review host")
}

// Run leaves the checkout and index unchanged. A failed run retains artifacts
// and returns an error manifest so callers can inspect or resume manually.
func Run(ctx context.Context, cfg Config) (manifest *Manifest, resultErr error) {
	review, err := parseIdentifier(cfg.Identifier)
	if err != nil {
		return nil, err
	}
	if cfg.Timeout <= 0 || cfg.MaxBytes <= 0 || cfg.MaxBytes > 1<<30 {
		return nil, errors.New("timeout must be positive and max-bytes must be between 1 and 1073741824")
	}
	if cfg.Repo == "" {
		cfg.Repo = "."
	}
	m := &Manifest{Schema: 1, State: "preparing", Review: review, Applicability: "not_checked"}
	r := &runner{ctx: ctx, cfg: cfg, m: m}
	r.progress("Recording the source checkout baseline...")
	baseline, err := r.baseline()
	if err != nil {
		return nil, err
	}
	m.Baseline = baseline.info
	dir, err := createOutput(cfg.Output, baseline.info.Root)
	if err != nil {
		return nil, err
	}
	m.Directory = dir
	m.ManifestPath = filepath.Join(dir, "manifest.json")
	r.progress("Saving review artifacts in " + dir)
	defer func() {
		if resultErr != nil {
			m.State = "error"
			m.Error = resultErr.Error()
		} else {
			m.State = "ready"
		}
		if err := writeJSON(m.ManifestPath, m); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("save manifest: %w", err))
			m.State, m.Error = "error", resultErr.Error()
		}
	}()
	m.Baseline.Status = filepath.Join(dir, "status.z")
	m.Baseline.Changes = filepath.Join(dir, "local.patch")
	if err := os.WriteFile(m.Baseline.Status, baseline.status, 0600); err != nil {
		return m, err
	}
	if err := os.WriteFile(m.Baseline.Changes, baseline.changes, 0600); err != nil {
		return m, err
	}
	if err := os.WriteFile(filepath.Join(dir, "staged.patch"), baseline.staged, 0600); err != nil {
		return m, err
	}
	if err := os.WriteFile(filepath.Join(dir, "unstaged.patch"), baseline.unstaged, 0600); err != nil {
		return m, err
	}
	if review.Source == "github" {
		err = r.github()
	} else {
		err = r.differential()
	}
	if err != nil {
		return m, err
	}
	r.progress("Checking patch completeness and applicability...")
	if err := r.checkPatch(); err != nil {
		return m, err
	}
	r.progress("Verifying that the source checkout is unchanged...")
	after, err := r.baseline()
	if err != nil {
		return m, err
	}
	if !baseline.same(after) {
		return m, errors.New("checkout changed during preparation; rerun against a stable baseline")
	}
	r.progress(fmt.Sprintf("Preparation complete: %d changed files; applicability: %s", m.FileCount, m.Applicability))
	return m, nil
}

func createOutput(requested, repo string) (string, error) {
	if requested == "" {
		parent, err := filepath.Abs(os.TempDir())
		if err != nil {
			return "", err
		}
		parent, err = filepath.EvalSymlinks(parent)
		if err != nil {
			return "", err
		}
		if within(repo, parent) {
			return "", errors.New("temporary directory is inside checkout; use --output outside it")
		}
		return os.MkdirTemp(parent, "freebsd-review-")
	}
	abs, err := filepath.Abs(requested)
	if err != nil {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	abs = filepath.Join(parent, filepath.Base(abs))
	if within(repo, abs) {
		return "", errors.New("artifact directory must be outside the checkout")
	}
	if err := os.Mkdir(abs, 0700); err != nil {
		return "", fmt.Errorf("create new artifact directory: %w", err)
	}
	return abs, nil
}

func within(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0600)
}

func (r *runner) get(endpoint, accept string) ([]byte, http.Header, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || (u.Host != "api.github.com" && u.Host != "reviews.freebsd.org") {
		return nil, nil, errors.New("refusing unsupported HTTP endpoint")
	}
	ctx, cancel := context.WithTimeout(r.ctx, r.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", "freebsd-review/1")
	req.Header.Set("Accept", accept)
	if u.Host == "api.github.com" {
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if r.cfg.GitHubToken != "" {
			req.Header.Set("Authorization", "Bearer "+r.cfg.GitHubToken)
		}
	}
	client := http.Client{}
	if r.cfg.Client != nil {
		client = *r.cfg.Client
	}
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 5 || next.URL.Scheme != "https" || next.URL.Host != u.Host || next.URL.User != nil {
			return errors.New("refusing cross-host or insecure HTTP redirect")
		}
		return nil
	}
	resp, err := client.Do(req)
	// All review data comes from the public freebsd/freebsd-src repository.
	// A stale inherited token must not prevent access that works anonymously.
	publicGitHub := u.Host == "api.github.com" && strings.HasPrefix(u.Path, "/repos/freebsd/freebsd-src/")
	if err == nil && resp.StatusCode == http.StatusUnauthorized && publicGitHub && req.Header.Get("Authorization") != "" {
		_ = resp.Body.Close()
		r.cfg.GitHubToken = ""
		req.Header.Del("Authorization")
		warning := "GitHub rejected the configured token (HTTP 401); retrying anonymously with lower API rate limits. Check GITHUB_TOKEN/GH_TOKEN."
		if r.m != nil {
			r.m.Warnings = append(r.m.Warnings, warning)
		}
		r.progress(warning)
		// Retry once under the original timeout. Later requests stay anonymous.
		resp, err = client.Do(req)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("GET %s: %w", endpoint, err)
	}
	// ReadAll reports payload read errors; closing the read-only body is
	// best effort, including when rejecting a response before reading it.
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("GET %s: HTTP %d (check access, rate limits, or GITHUB_TOKEN/GH_TOKEN)", endpoint, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, r.cfg.MaxBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(data)) > r.cfg.MaxBytes {
		return nil, nil, fmt.Errorf("GET %s exceeds max-bytes", endpoint)
	}
	return data, resp.Header, nil
}

func (r *runner) savePatch(data []byte) error {
	if len(data) == 0 {
		return errors.New("downloaded diff is empty; verify an empty proposal manually")
	}
	trimmed := strings.TrimSpace(string(data))
	if !strings.HasPrefix(trimmed, "diff --git ") && !strings.HasPrefix(trimmed, "Index: ") && !strings.HasPrefix(trimmed, "--- ") {
		return errors.New("download is not a unified diff (possible login page or bot challenge)")
	}
	r.m.Patch = filepath.Join(r.m.Directory, "review.patch")
	if err := os.WriteFile(r.m.Patch, data, 0600); err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	r.m.SHA256 = hex.EncodeToString(sum[:])
	r.m.RetrievedAt = time.Now().UTC().Format(time.RFC3339)
	return nil
}

func decodeJSON(data []byte, value any) error {
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("invalid API JSON: %w", err)
	}
	return nil
}

func numberString(value int) string { return strconv.Itoa(value) }
