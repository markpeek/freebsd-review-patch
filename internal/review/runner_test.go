package review

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/markpeek/freebsd-review-patch/internal/prepare"
)

const validReport = `{"summary":"The patch introduces a division-by-zero regression.","findings":[{"priority":"P2","title":"Preserve the zero-divisor guard","path":"file.go","start_line":4,"end_line":4,"side":"new","description":"Calling Divide(0) now panics.","evidence":"The patch removes the x == 0 check before 10 / x.","suggestion":"Retain the guard."}],"validation":["Inspected the changed function and patch."],"limitations":["Build and runtime validation were not performed."]}`

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func gitTest(t *testing.T, repo string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", append([]string{"--no-optional-locks", "-C", repo}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, data)
	}
	return data
}

func preparedFixture(t *testing.T) *prepare.Manifest {
	t.Helper()
	parent := t.TempDir()
	repo, artifacts := filepath.Join(parent, "repo"), filepath.Join(parent, "artifacts")
	for _, dir := range []string{repo, artifacts, filepath.Join(artifacts, "context")} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	gitTest(t, repo, "init", "-q", "--template=", "--initial-branch=main")
	base := "package example\n\nfunc Divide(x int) int {\n\tif x == 0 {\n\t\treturn 0\n\t}\n\treturn 10 / x\n}\n"
	proposed := "package example\n\nfunc Divide(x int) int {\n\treturn 10 / x\n}\n"
	writeTestFile(t, filepath.Join(repo, "file.go"), base)
	gitTest(t, repo, "add", "file.go")
	message := filepath.Join(parent, "headless-review-fixture-commit.txt")
	writeTestFile(t, message, "Create headless review test fixture\n")
	gitTest(t, repo, "-c", "user.name=Review Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "-q", "-F", message)
	writeTestFile(t, filepath.Join(repo, "file.go"), proposed)
	patch := gitTest(t, repo, "diff", "--binary", "HEAD")
	writeTestFile(t, filepath.Join(repo, "file.go"), base)
	m := &prepare.Manifest{
		Schema: 1, State: "ready", Directory: artifacts, ManifestPath: filepath.Join(artifacts, "manifest.json"),
		Review:   prepare.Review{Source: "differential", ID: "D42", URL: "https://reviews.freebsd.org/D42", DiffID: "99"},
		Baseline: prepare.Baseline{Root: repo, Head: strings.TrimSpace(string(gitTest(t, repo, "rev-parse", "HEAD"))), Branch: "main", Status: filepath.Join(artifacts, "status.z"), Changes: filepath.Join(artifacts, "local.patch")},
		Patch:    filepath.Join(artifacts, "review.patch"), Files: filepath.Join(artifacts, "files.json"), Context: filepath.Join(artifacts, "context"),
		SHA256: fmt.Sprintf("%x", sha256.Sum256(patch)), FileCount: 1, Strip: 1, Applicability: "applies", Diagnostics: filepath.Join(artifacts, "apply-check.txt"),
	}
	writeTestFile(t, m.Patch, string(patch))
	m.Workspace = filepath.Join(artifacts, "proposed")
	if err := os.Mkdir(m.Workspace, 0700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(m.Workspace, "file.go"), proposed)
	m.WorkspaceFiles = map[string]prepare.WorkspaceFile{
		"file.go": {SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(proposed))), Mode: 0600},
	}
	writeTestFile(t, m.Files, `[{"path":"file.go","additions":"0","deletions":"3"}]`)
	writeTestFile(t, m.Diagnostics, "Forward applicability check passed.\n")
	for _, name := range []string{"status.z", "local.patch", "staged.patch", "unstaged.patch"} {
		writeTestFile(t, filepath.Join(artifacts, name), "")
	}
	writeTestFile(t, filepath.Join(m.Context, "review.txt"), "Remove the zero check from Divide.\n")
	if err := prepare.SaveManifest(m); err != nil {
		t.Fatal(err)
	}
	return m
}

func fakeReviewers(t *testing.T) {
	t.Helper()
	if os.PathSeparator != '/' {
		t.Skip("fake CLI wrappers require a POSIX shell")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, name := range []string{"codex", "claude"} {
		quoted := "'" + strings.ReplaceAll(executable, "'", "'\\''") + "'"
		wrapper := "#!/bin/sh\nexec " + quoted + " -test.run=^TestReviewerProcess$ -- \"$@\"\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(wrapper), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FREEBSD_REVIEW_TEST_PROCESS", "1")
	t.Setenv("FREEBSD_REVIEW_HEADLESS", "")
}

// TestReviewerProcess runs only in the fake executable, never as a model call.
func TestReviewerProcess(t *testing.T) {
	if os.Getenv("FREEBSD_REVIEW_TEST_PROCESS") != "1" {
		return
	}
	args := os.Args
	for i, arg := range args {
		if arg == "--" {
			args = args[i+1:]
			break
		}
	}
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(2)
	}
	if capture := os.Getenv("FREEBSD_REVIEW_TEST_CAPTURE"); capture != "" {
		data, _ := json.Marshal(struct {
			Args   []string `json:"args"`
			Input  string   `json:"input"`
			CWD    string   `json:"cwd"`
			Nested string   `json:"nested"`
			Token  string   `json:"token"`
		}{args, string(input), os.Getenv("PWD"), os.Getenv("FREEBSD_REVIEW_HEADLESS"), os.Getenv("GITHUB_TOKEN")})
		if os.WriteFile(capture, data, 0600) != nil {
			os.Exit(2)
		}
	}
	mode := os.Getenv("FREEBSD_REVIEW_TEST_MODE")
	if mode == "exit" {
		_, _ = fmt.Fprintln(os.Stderr, "authentication failed")
		os.Exit(3)
	}
	if mode == "timeout" {
		// This descendant must be killed along with the wrapper process.
		cmd := exec.Command("sleep", "60")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Start(); err != nil {
			os.Exit(2)
		}
		if pid := os.Getenv("FREEBSD_REVIEW_TEST_PID"); pid != "" {
			_ = os.WriteFile(pid, []byte(strconv.Itoa(cmd.Process.Pid)), 0600)
		}
		_ = cmd.Wait()
		os.Exit(0)
	}
	if mode == "oversized" {
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("x", 1<<20))
		os.Exit(0)
	}
	if mode == "mutate" {
		_ = os.WriteFile("file.go", []byte("unexpected mutation\n"), 0600)
	}
	if mode == "mutate-workspace" {
		_ = os.WriteFile(os.Getenv("FREEBSD_REVIEW_TEST_WORKSPACE"), []byte("unexpected workspace mutation\n"), 0600)
	}
	result := validReport
	switch mode {
	case "empty":
		result = `{"summary":"No actionable defects found.","findings":[],"validation":[],"limitations":["Inspection only."]}`
	case "malformed":
		result = `{}`
	}
	if len(args) > 0 && args[0] == "exec" {
		for i, arg := range args {
			if arg == "--output-last-message" && i+1 < len(args) && mode != "missing-result" {
				if os.WriteFile(args[i+1], []byte(result), 0600) != nil {
					os.Exit(2)
				}
			}
		}
		if mode == "reported-error" {
			_, _ = fmt.Fprintln(os.Stdout, `{"type":"turn.failed"}`)
		} else {
			_, _ = fmt.Fprintln(os.Stdout, `{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":10}}`)
		}
	} else {
		envelope := map[string]any{"type": "result", "subtype": "success", "is_error": false, "structured_output": json.RawMessage(result)}
		switch mode {
		case "reported-error":
			envelope["is_error"] = true
		case "missing-result":
			delete(envelope, "structured_output")
		}
		_ = json.NewEncoder(os.Stdout).Encode(envelope)
	}
	os.Exit(0)
}

func reviewerConfig(name string) Config {
	return Config{Reviewer: name, Model: "test-model", Timeout: 10 * time.Second, IOTimeout: 5 * time.Second, MaxBytes: 1 << 20}
}

func TestHeadlessReview(t *testing.T) {
	fakeReviewers(t)
	for _, backend := range []string{"codex", "claude"} {
		t.Run(backend, func(t *testing.T) {
			m := preparedFixture(t)
			capture := filepath.Join(t.TempDir(), "request.json")
			t.Setenv("FREEBSD_REVIEW_TEST_CAPTURE", capture)
			t.Setenv("GITHUB_TOKEN", "must-not-reach-child")
			index, err := os.ReadFile(filepath.Join(m.Baseline.Root, ".git", "index"))
			if err != nil {
				t.Fatal(err)
			}
			if err := Run(context.Background(), reviewerConfig(backend), m); err != nil {
				t.Fatal(err)
			}
			if m.State != "ready" || m.Analysis.State != "complete" || m.Analysis.Count == nil || *m.Analysis.Count != 1 {
				t.Fatalf("unexpected review state: %+v", m.Analysis)
			}
			data, err := os.ReadFile(m.Analysis.Report)
			if err != nil || !strings.Contains(string(data), "Preserve the zero-divisor guard") || !strings.Contains(string(data), m.SHA256) {
				t.Fatalf("incomplete report: %s, %v", data, err)
			}
			request, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			var captured struct {
				Args   []string
				Input  string
				Nested string
				Token  string
			}
			if err := json.Unmarshal(request, &captured); err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(captured.Args, " ")
			if !strings.Contains(joined, "test-model") || captured.Nested != "1" || captured.Token != "" || !strings.Contains(captured.Input, m.Patch) || !strings.Contains(captured.Input, "Shared review guidance") {
				t.Fatalf("reviewer request missing context or controls: %s", request)
			}
			if backend == "codex" && (!strings.Contains(joined, "--sandbox read-only") || !strings.Contains(joined, `approval_policy="never"`) || !strings.Contains(joined, "--ignore-user-config") || !strings.Contains(joined, "--ignore-rules")) {
				t.Fatal("missing Codex restrictions")
			}
			if backend == "claude" && (!strings.Contains(joined, "--tools Read,Grep,Glob") || !strings.Contains(joined, "--permission-mode dontAsk")) {
				t.Fatal("missing Claude restrictions")
			}
			after, _ := os.ReadFile(filepath.Join(m.Baseline.Root, ".git", "index"))
			if !bytes.Equal(index, after) {
				t.Fatal("index changed during review")
			}
			loaded, err := prepare.LoadManifest(m.ManifestPath, 1<<20)
			if err != nil || loaded.Analysis == nil || loaded.Analysis.State != "complete" {
				t.Fatalf("saved manifest: %+v, %v", loaded, err)
			}
			previous := m.Analysis.Directory
			if err := Run(context.Background(), reviewerConfig(backend), loaded); err != nil {
				t.Fatal(err)
			}
			if loaded.Analysis.Directory == previous {
				t.Fatal("rerun overwrote prior analysis")
			}
			if len(loaded.Previous) != 1 || loaded.Previous[0].Directory != previous {
				t.Fatalf("earlier attempt not preserved: %+v", loaded.Previous)
			}
		})
	}
}

func TestReviewerFailures(t *testing.T) {
	fakeReviewers(t)
	for _, backend := range []string{"codex", "claude"} {
		for _, mode := range []string{"exit", "reported-error", "missing-result", "malformed", "oversized", "mutate", "timeout"} {
			t.Run(backend+"/"+mode, func(t *testing.T) {
				m := preparedFixture(t)
				t.Setenv("FREEBSD_REVIEW_TEST_MODE", mode)
				cfg := reviewerConfig(backend)
				if mode == "oversized" {
					cfg.MaxBytes = 16 << 10
				}
				if mode == "timeout" {
					cfg.Timeout = 300 * time.Millisecond
				}
				start := time.Now()
				err := Run(context.Background(), cfg, m)
				if err == nil || m.State != "ready" || m.Analysis.State == "complete" || m.Analysis.Count != nil || m.Analysis.Report != "" {
					t.Fatalf("failed review reported success: %+v, %v", m.Analysis, err)
				}
				if mode == "timeout" && (m.Analysis.State != "timeout" || time.Since(start) > 5*time.Second) {
					t.Fatalf("timeout did not stop descendants: %v, %+v", time.Since(start), m.Analysis)
				}
				if mode == "mutate" && !strings.Contains(err.Error(), "baseline changed") {
					t.Fatalf("missed mutation: %v", err)
				}
				if _, err := os.Stat(m.Analysis.Prompt); err != nil {
					t.Fatal("failure artifacts were lost")
				}
			})
		}
	}
}

func TestNoFindingsAndStaleManifest(t *testing.T) {
	fakeReviewers(t)
	t.Setenv("FREEBSD_REVIEW_TEST_MODE", "empty")
	m := preparedFixture(t)
	if err := Run(context.Background(), reviewerConfig("claude"), m); err != nil {
		t.Fatal(err)
	}
	if m.Analysis.Count == nil || *m.Analysis.Count != 0 {
		t.Fatal("empty review was not successful")
	}
	writeTestFile(t, filepath.Join(m.Baseline.Root, "file.go"), "changed\n")
	if err := Run(context.Background(), reviewerConfig("claude"), m); err == nil {
		t.Fatal("reviewed stale baseline")
	}
}

func TestWorkspaceVerificationAroundReview(t *testing.T) {
	fakeReviewers(t)
	for _, backend := range []string{"codex", "claude"} {
		for _, when := range []string{"before", "after"} {
			t.Run(backend+"/"+when, func(t *testing.T) {
				m := preparedFixture(t)
				capture := filepath.Join(t.TempDir(), "request.json")
				t.Setenv("FREEBSD_REVIEW_TEST_CAPTURE", capture)
				file := filepath.Join(m.Workspace, "file.go")
				if when == "before" {
					writeTestFile(t, file, "unrelated proposed file\n")
				} else {
					t.Setenv("FREEBSD_REVIEW_TEST_MODE", "mutate-workspace")
					t.Setenv("FREEBSD_REVIEW_TEST_WORKSPACE", file)
				}
				err := Run(context.Background(), reviewerConfig(backend), m)
				if err == nil || !strings.Contains(err.Error(), "recorded content hashes") || m.Analysis.State != "error" || m.Analysis.Count != nil || m.Analysis.Report != "" {
					t.Fatalf("workspace mutation accepted: %+v, %v", m.Analysis, err)
				}
				_, captureErr := os.Stat(capture)
				if (when == "before" && !os.IsNotExist(captureErr)) || (when == "after" && captureErr != nil) {
					t.Fatalf("unexpected reviewer invocation: %v", captureErr)
				}
			})
		}
	}
}

func TestInvalidFindings(t *testing.T) {
	m := preparedFixture(t)
	for _, data := range []string{
		`{}`, `{"summary":"ok","findings":null,"validation":[],"limitations":[]}`,
		strings.Replace(validReport, `"path":"file.go"`, `"path":"../outside"`, 1),
		strings.Replace(validReport, `"start_line":4`, `"start_line":0`, 1),
		strings.Replace(validReport, `"P2"`, `"P9"`, 1),
		strings.Replace(validReport, `"start_line":4,"end_line":4`, `"start_line":9999,"end_line":9999`, 1),
		strings.Replace(validReport, `"start_line":4,"end_line":4`, `"start_line":4,"end_line":6`, 1),
		strings.Replace(validReport, `"suggestion":"Retain the guard."`, `"unexpected":true`, 1),
	} {
		if _, err := parseReport([]byte(data), m, 1<<20); err == nil {
			t.Fatalf("accepted invalid findings: %s", data)
		}
	}
}

func TestManifestTampering(t *testing.T) {
	m := preparedFixture(t)
	copy := *m
	copy.Patch = filepath.Join(t.TempDir(), "outside.patch")
	if err := prepare.SaveManifest(&copy); err != nil {
		t.Fatal(err)
	}
	if _, err := prepare.LoadManifest(copy.ManifestPath, 1<<20); err == nil {
		t.Fatal("accepted external artifact path")
	}
	if err := prepare.SaveManifest(m); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, m.Patch, "altered\n")
	if err := prepare.VerifyBaseline(context.Background(), m, time.Second, 1<<20); err == nil {
		t.Fatal("accepted patch hash mismatch")
	}
}

func TestNestedReviewRejected(t *testing.T) {
	fakeReviewers(t)
	t.Setenv("FREEBSD_REVIEW_HEADLESS", "1")
	if err := reviewerConfig("codex").Validate(); err == nil {
		t.Fatal("accepted nested review")
	}
}

func TestReviewHeartbeat(t *testing.T) {
	messages := make(chan string, 16)
	stop := startHeartbeat(func(message string) {
		select {
		case messages <- message:
		default:
		}
	}, "codex", time.Millisecond)
	select {
	case message := <-messages:
		if !strings.Contains(message, "codex review still running") {
			t.Errorf("unexpected heartbeat: %s", message)
		}
	case <-time.After(time.Second):
		t.Error("no progress while the reviewer was running")
	}
	stop()
	// stop waits for the callback, so closing this channel cannot race a send.
	close(messages)
	startHeartbeat(nil, "codex", time.Millisecond)()
}

func TestCanceledReview(t *testing.T) {
	fakeReviewers(t)
	t.Setenv("FREEBSD_REVIEW_TEST_MODE", "timeout")
	m := preparedFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timer := time.AfterFunc(300*time.Millisecond, cancel)
	defer timer.Stop()
	if err := Run(ctx, reviewerConfig("codex"), m); err == nil || m.Analysis.State != "canceled" {
		t.Fatalf("canceled review state: %+v, %v", m.Analysis, err)
	}
}

func TestLiveHeadless(t *testing.T) {
	backend := os.Getenv("FREEBSD_REVIEW_LIVE_REVIEWER")
	if backend == "" {
		t.Skip("set FREEBSD_REVIEW_LIVE_REVIEWER=codex or claude for an opt-in authenticated model call")
	}
	m := preparedFixture(t)
	cfg := reviewerConfig(backend)
	cfg.Model, cfg.Timeout, cfg.MaxBytes = os.Getenv("FREEBSD_REVIEW_LIVE_MODEL"), 2*time.Minute, 16<<20
	if err := Run(context.Background(), cfg, m); err != nil {
		t.Fatal(err)
	}
	t.Logf("headless %s completed with %d findings", backend, *m.Analysis.Count)
}

func TestChildEnvAllowlist(t *testing.T) {
	env := childEnv([]string{
		"PATH=/bin", "HOME=/home/u", "LC_ALL=C", "ANTHROPIC_API_KEY=k",
		"GITHUB_TOKEN=x", "GH_TOKEN=y", "CLAUDECODE=1", "PWD=/old",
		"SECRET_CLOUD_KEY=z", "FREEBSD_REVIEW_HEADLESS=0",
	}, "/src")
	got := strings.Join(env, "\n")
	for _, want := range []string{"PATH=/bin", "HOME=/home/u", "LC_ALL=C", "ANTHROPIC_API_KEY=k", "FREEBSD_REVIEW_HEADLESS=1", "PWD=/src"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %q", want, got)
		}
	}
	for _, bad := range []string{"GITHUB_TOKEN", "GH_TOKEN", "CLAUDECODE", "PWD=/old", "SECRET_CLOUD_KEY", "HEADLESS=0"} {
		if strings.Contains(got, bad) {
			t.Errorf("leaked %s in %q", bad, got)
		}
	}
}

func TestFindingHunkRanges(t *testing.T) {
	m := preparedFixture(t)
	old := strings.Replace(validReport, `"start_line":4,"end_line":4,"side":"new"`, `"start_line":4,"end_line":6,"side":"old"`, 1)
	if _, err := parseReport([]byte(old), m, 1<<20); err != nil {
		t.Fatalf("rejected old-side range inside hunk: %v", err)
	}
	if _, err := parseReport([]byte(strings.Replace(old, `"end_line":6`, `"end_line":9`, 1)), m, 1<<20); err == nil {
		t.Fatal("accepted old-side range past the hunk")
	}
}

func TestFindingsSortedByPriority(t *testing.T) {
	m := preparedFixture(t)
	finding := func(priority string) string {
		return strings.Replace(validReport[strings.Index(validReport, `{"priority"`):strings.Index(validReport, `],"validation"`)], `"P2"`, `"`+priority+`"`, 1)
	}
	data := `{"summary":"s","findings":[` + finding("P3") + `,` + finding("P0") + `,` + finding("P2") + `],"validation":[],"limitations":[]}`
	report, err := parseReport([]byte(data), m, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range report.Findings {
		got = append(got, f.Priority)
	}
	if strings.Join(got, ",") != "P0,P2,P3" {
		t.Fatalf("unexpected order %v", got)
	}
}

func TestLoadManifestThroughSymlink(t *testing.T) {
	m := preparedFixture(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(m.Directory, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := prepare.LoadManifest(filepath.Join(link, "manifest.json"), 1<<20); err != nil {
		t.Fatalf("symlinked manifest path rejected: %v", err)
	}
}
