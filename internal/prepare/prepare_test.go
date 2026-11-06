package prepare

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testPatch = "diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ b/file.txt\n@@ -1,4 +1,4 @@\n one\n-before\n+after\n three\n four\n"
const originalFile = "one\nbefore\nthree\nfour\nfive\nsix\nseven\neight\nnine\noriginal\n"

func cleanupArtifacts(t *testing.T, m *Manifest) {
	t.Helper()
	t.Cleanup(func() {
		if err := os.RemoveAll(m.Directory); err != nil {
			t.Errorf("remove review artifacts: %v", err)
		}
	})
}

func testGit(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	options := []string{"--no-optional-locks", "-C", dir}
	cmd := exec.Command("git", append(options, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, data)
	}
	return data
}

func putFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func fixtureRepo(t *testing.T) string {
	t.Helper()
	parent := t.TempDir()
	repo := filepath.Join(parent, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	testGit(t, repo, "init", "-q", "--initial-branch=main", "--template=")
	putFile(t, filepath.Join(repo, "file.txt"), originalFile)
	testGit(t, repo, "add", "file.txt")
	// Fixture commits follow the same message-file workflow as real commits.
	message := filepath.Join(parent, "review-helper-fixture-message.txt")
	putFile(t, message, "Create review helper test fixture\n")
	testGit(t, repo, "-c", "user.name=Review Helper Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "-q", "-F", message)
	return repo
}

type testTransport struct {
	base *url.URL
}

func (tr testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme, clone.URL.Host = tr.base.Scheme, tr.base.Host
	clone.Host = req.URL.Host
	return http.DefaultTransport.RoundTrip(clone)
}

func testConfig(t *testing.T, handler http.HandlerFunc) Config {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return Config{
		Identifier: "PR #42", Repo: fixtureRepo(t), Timeout: 5 * time.Second,
		MaxBytes: 1 << 20, Snapshot: true,
		Client: &http.Client{Transport: testTransport{base: base}},
	}
}

func githubMetadata(head string) string {
	return fmt.Sprintf(`{"number":42,"html_url":"https://github.com/freebsd/freebsd-src/pull/42","changed_files":1,"commits":0,"base":{"sha":%q,"repo":{"full_name":"freebsd/freebsd-src"}},"head":{"sha":%q}}`, strings.Repeat("a", 40), head)
}

func githubHandler(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "/repos/freebsd/freebsd-src/pulls/42" {
		if req.Header.Get("Accept") == "application/vnd.github.diff" {
			_, _ = fmt.Fprint(w, testPatch)
		} else {
			_, _ = fmt.Fprint(w, githubMetadata(strings.Repeat("b", 40)))
		}
	} else if strings.HasSuffix(req.URL.Path, "/files") {
		_, _ = fmt.Fprint(w, `[{"filename":"file.txt","status":"modified","additions":1,"deletions":1}]`)
	} else if strings.HasSuffix(req.URL.Path, "/check-runs") {
		_, _ = fmt.Fprint(w, `{"total_count":0,"check_runs":[]}`)
	} else if strings.HasSuffix(req.URL.Path, "/status") {
		_, _ = fmt.Fprint(w, `{"total_count":0,"statuses":[]}`)
	} else {
		_, _ = fmt.Fprint(w, `[]`)
	}
}

func differentialHandler(w http.ResponseWriter, req *http.Request) {
	if req.URL.Query().Get("download") == "true" {
		if req.URL.Query().Get("id") != "99" {
			http.Error(w, "diff was not pinned", http.StatusBadRequest)
			return
		}
		patch := strings.ReplaceAll(testPatch, "diff --git a/file.txt b/file.txt", "Index: file.txt\n===================================================================")
		patch = strings.ReplaceAll(strings.ReplaceAll(patch, "a/file.txt", "file.txt"), "b/file.txt", "file.txt")
		_, _ = fmt.Fprint(w, patch)
		return
	}
	_, _ = fmt.Fprint(w, `<html><head><title>D42 Fix a bug</title></head><body><h2>Summary</h2><p>Untrusted review text.</p><input name="id" value="99" checked="checked"><script>do not include script</script></body></html>`)
}

func TestParseIdentifier(t *testing.T) {
	for _, input := range []string{
		"D42", "PR #42", "#42", "https://reviews.freebsd.org/D42",
		"https://reviews.freebsd.org/D42?id=99&download=true",
		"https://github.com/freebsd/freebsd-src/pull/42",
		"https://github.com/freebsd/freebsd-src/pull/42/files",
		"https://github.com/freebsd/freebsd-src/pull/42.diff",
		"https://github.com/freebsd/freebsd-src/pull/42.patch",
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := parseIdentifier(input); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, input := range []string{
		"42", "D0", "D42; touch /tmp/bad", "PR #42 && echo bad", "D４２",
		"http://reviews.freebsd.org/D42", "https://reviews.freebsd.org.evil/D42",
		"https://reviews.freebsd.org/D42?id=abc", "https://user@reviews.freebsd.org/D42",
		"https://reviews.freebsd.org:443/D42", "https://github.com/other/repo/pull/42",
		"https://github.com/freebsd/freebsd-src/issues/42",
		"https://github.com/freebsd/freebsd-src/pull/42/../../issues/1",
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := parseIdentifier(input); err == nil {
				t.Fatal("accepted invalid input")
			}
		})
	}
}

func TestPreparationPreservesDirtyCheckout(t *testing.T) {
	for _, service := range []string{"github", "differential"} {
		t.Run(service, func(t *testing.T) {
			handler := githubHandler
			if service == "differential" {
				handler = differentialHandler
			}
			cfg := testConfig(t, handler)
			if service == "differential" {
				cfg.Identifier = "D42"
			}
			file := filepath.Join(cfg.Repo, "file.txt")
			putFile(t, file, strings.ReplaceAll(originalFile, "original", "staged"))
			testGit(t, cfg.Repo, "add", "file.txt")
			putFile(t, file, strings.ReplaceAll(originalFile, "original", "unstaged"))
			putFile(t, filepath.Join(cfg.Repo, "untracked.txt"), "keep me\n")
			status := testGit(t, cfg.Repo, "status", "--porcelain=v1", "-z")
			index, err := os.ReadFile(filepath.Join(cfg.Repo, ".git", "index"))
			if err != nil {
				t.Fatal(err)
			}
			head := testGit(t, cfg.Repo, "rev-parse", "HEAD")
			manifest, err := Run(context.Background(), cfg)
			if err != nil {
				t.Fatalf("prepare: %v; manifest=%+v", err, manifest)
			}
			if manifest.State != "ready" || manifest.Applicability != "applies" || manifest.Workspace == "" || !manifest.Baseline.Dirty {
				t.Fatalf("unexpected manifest: %+v", manifest)
			}
			cleanupArtifacts(t, manifest)
			proposed, err := os.ReadFile(filepath.Join(manifest.Workspace, "file.txt"))
			if err != nil {
				t.Fatal(err)
			}
			want := strings.ReplaceAll(strings.ReplaceAll(originalFile, "original", "unstaged"), "before", "after")
			if string(proposed) != want {
				t.Fatalf("workspace lost local edits: %q", proposed)
			}
			unchanged, _ := os.ReadFile(file)
			if string(unchanged) != strings.ReplaceAll(originalFile, "original", "unstaged") {
				t.Fatal("modified checkout file")
			}
			afterIndex, _ := os.ReadFile(filepath.Join(cfg.Repo, ".git", "index"))
			if !bytes.Equal(index, afterIndex) || !bytes.Equal(status, testGit(t, cfg.Repo, "status", "--porcelain=v1", "-z")) || !bytes.Equal(head, testGit(t, cfg.Repo, "rev-parse", "HEAD")) {
				t.Fatal("checkout index, status, or HEAD changed")
			}
			if service == "differential" && (manifest.Strip != 0 || manifest.Review.DiffID != "99") {
				t.Fatalf("Differential pin/strip incorrect: %+v", manifest)
			}
			encoded, _ := json.Marshal(manifest)
			if bytes.Contains(encoded, []byte("Untrusted review text")) || bytes.Contains(encoded, []byte("@@ -1")) {
				t.Fatal("verbose source content leaked into compact manifest")
			}
		})
	}
}

func TestGitHubVersionChanges(t *testing.T) {
	var mu sync.Mutex
	metadataRequests, diffRequests := 0, 0
	cfg := testConfig(t, func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if req.URL.Path == "/repos/freebsd/freebsd-src/pulls/42" {
			if req.Header.Get("Accept") == "application/vnd.github.diff" {
				diffRequests++
				_, _ = fmt.Fprint(w, testPatch)
			} else {
				metadataRequests++
				head := strings.Repeat("b", 40)
				if metadataRequests > 1 {
					head = strings.Repeat("c", 40)
				}
				_, _ = fmt.Fprint(w, githubMetadata(head))
			}
			return
		}
		githubHandler(w, req)
	})
	m, err := Run(context.Background(), cfg)
	if m != nil {
		cleanupArtifacts(t, m)
	}
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if diffRequests != 2 || m.Review.Head != strings.Repeat("c", 40) {
		t.Fatalf("did not refetch changed proposal: diffs=%d manifest=%+v", diffRequests, m)
	}
}

func TestFailuresRetainArtifacts(t *testing.T) {
	cases := []struct {
		name, replacement, want string
	}{
		{"challenge", "<html>Login</html>", "not a unified diff"},
		{"wrong file", strings.ReplaceAll(testPatch, "file.txt", "other.txt"), "does not match GitHub metadata"},
		{"unsafe path", strings.ReplaceAll(testPatch, "file.txt", "../../outside.txt"), "unsafe"},
		{"binary stub", "diff --git a/file.txt b/file.txt\nindex aaaaaaa..bbbbbbb 100644\nBinary files a/file.txt and b/file.txt differ\n", "binary changes"},
		{"empty", "", "empty"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			cfg := testConfig(t, func(w http.ResponseWriter, req *http.Request) {
				if req.Header.Get("Accept") == "application/vnd.github.diff" {
					_, _ = fmt.Fprint(w, item.replacement)
					return
				}
				githubHandler(w, req)
			})
			m, err := Run(context.Background(), cfg)
			if m != nil {
				cleanupArtifacts(t, m)
			}
			if err == nil || !strings.Contains(err.Error(), item.want) {
				t.Fatalf("want %q, got %v", item.want, err)
			}
			if m == nil || m.State != "error" || m.Error == "" {
				t.Fatalf("missing error manifest: %+v", m)
			}
			if _, err := os.Stat(m.ManifestPath); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestConflictsAreReviewable(t *testing.T) {
	cfg := testConfig(t, githubHandler)
	putFile(t, filepath.Join(cfg.Repo, "file.txt"), "different\n")
	m, err := Run(context.Background(), cfg)
	if m != nil {
		cleanupArtifacts(t, m)
	}
	if err != nil || m.Applicability != "conflicts" || m.Workspace != "" || m.Diagnostics == "" {
		t.Fatalf("conflict should retain review artifacts: %+v, %v", m, err)
	}
}

func TestOutputDirectoryRestrictions(t *testing.T) {
	cfg := testConfig(t, githubHandler)
	for _, output := range []string{filepath.Join(cfg.Repo, "artifacts"), cfg.Repo} {
		cfg.Output = output
		if _, err := Run(context.Background(), cfg); err == nil {
			t.Fatalf("accepted output inside checkout: %s", output)
		}
	}
	parent := t.TempDir()
	link := filepath.Join(parent, "repo-link")
	if err := os.Symlink(cfg.Repo, link); err != nil {
		t.Fatal(err)
	}
	cfg.Output = filepath.Join(link, "artifacts")
	if _, err := Run(context.Background(), cfg); err == nil {
		t.Fatal("accepted output through a symlink into checkout")
	}
	cfg.Output = parent
	if _, err := Run(context.Background(), cfg); err == nil {
		t.Fatal("accepted an existing output directory")
	}
}

func TestRelativeTemporaryDirectory(t *testing.T) {
	current, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	current, err = filepath.EvalSymlinks(current)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", ".")
	if _, err := createOutput("", current); err == nil {
		t.Fatal("relative TMPDIR allowed artifacts inside checkout")
	}
}

func TestGitHubRejectedTokenFallback(t *testing.T) {
	const token = "rejected-test-token"
	var authenticated, anonymous atomic.Int32
	cfg := testConfig(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "" {
			authenticated.Add(1)
			if req.Header.Get("Authorization") != "Bearer "+token {
				t.Error("unexpected authentication header")
			}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = fmt.Fprint(w, token) // Rejected response bodies must not enter diagnostics.
			return
		}
		anonymous.Add(1)
		githubHandler(w, req)
	})
	cfg.GitHubToken = token
	var progress []string
	cfg.Progress = func(message string) { progress = append(progress, message) }
	m, err := Run(context.Background(), cfg)
	if m != nil {
		cleanupArtifacts(t, m)
	}
	if err != nil {
		t.Fatal(err)
	}
	if m.State != "ready" || authenticated.Load() != 1 || anonymous.Load() < 2 {
		t.Fatalf("fallback did not persist: state=%s, authenticated=%d, anonymous=%d", m.State, authenticated.Load(), anonymous.Load())
	}
	if len(m.Warnings) != 1 || !strings.Contains(m.Warnings[0], "HTTP 401") || !strings.Contains(m.Warnings[0], "anonymously") {
		t.Fatalf("missing token warning: %v", m.Warnings)
	}
	if strings.Count(strings.Join(progress, "\n"), m.Warnings[0]) != 1 {
		t.Fatalf("expected one progress warning: %v", progress)
	}
	manifest, err := os.ReadFile(m.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(manifest, []byte("HTTP 401")) || bytes.Contains(manifest, []byte(token)) || strings.Contains(strings.Join(progress, "\n"), token) {
		t.Fatal("token warning was not saved or exposed the credential")
	}
}

func TestHTTPAuthenticationRetryLimits(t *testing.T) {
	for _, tc := range []struct {
		name, endpoint, token string
		statuses              []int
		authenticated         []bool
	}{
		{"valid token", githubAPI + "/pulls/42", "valid-test-token", []int{200}, []bool{true}},
		{"anonymous unauthorized", githubAPI + "/pulls/42", "", []int{401}, []bool{false}},
		{"forbidden", githubAPI + "/pulls/42", "test-token", []int{403}, []bool{true}},
		{"rate limited", githubAPI + "/pulls/42", "test-token", []int{429}, []bool{true}},
		{"retry unauthorized", githubAPI + "/pulls/42", "test-token", []int{401, 401}, []bool{true, false}},
		{"retry forbidden", githubAPI + "/pulls/42", "test-token", []int{401, 403}, []bool{true, false}},
		{"other repository", "https://api.github.com/repos/other/private/pulls/42", "test-token", []int{401}, []bool{true}},
		{"Differential unauthorized", "https://reviews.freebsd.org/D42", "test-token", []int{401}, []bool{false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			cfg := testConfig(t, func(w http.ResponseWriter, req *http.Request) {
				i := int(requests.Add(1)) - 1
				if i >= len(tc.statuses) {
					t.Error("unexpected retry")
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				if (req.Header.Get("Authorization") != "") != tc.authenticated[i] {
					t.Error("unexpected authentication state")
				}
				if req.Method != http.MethodGet || req.Header.Get("Accept") != "application/vnd.github.diff" {
					t.Error("request method or media type changed")
				}
				w.WriteHeader(tc.statuses[i])
			})
			cfg.GitHubToken = tc.token
			r := &runner{ctx: context.Background(), cfg: cfg, m: &Manifest{}}
			_, _, err := r.get(tc.endpoint, "application/vnd.github.diff")
			if requests.Load() != int32(len(tc.statuses)) {
				t.Fatalf("expected %d requests, got %d", len(tc.statuses), requests.Load())
			}
			status := tc.statuses[len(tc.statuses)-1]
			if status == http.StatusOK {
				if err != nil || r.cfg.GitHubToken != tc.token {
					t.Fatalf("valid token was not preserved: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("HTTP %d", status)) {
				t.Fatalf("missing final HTTP error: %v", err)
			}
			if (len(r.m.Warnings) == 1) != (len(tc.statuses) == 2) {
				t.Fatalf("unexpected warnings: %v", r.m.Warnings)
			}
		})
	}
}

func TestReadOnlyHTTPAndLimits(t *testing.T) {
	for _, scenario := range []string{"rate limit", "oversized", "redirect"} {
		t.Run(scenario, func(t *testing.T) {
			var requests atomic.Int32
			cfg := testConfig(t, func(w http.ResponseWriter, req *http.Request) {
				requests.Add(1)
				if req.Method != http.MethodGet {
					t.Error("unexpected HTTP mutation")
				}
				if req.Host == "reviews.freebsd.org" && req.Header.Get("Authorization") != "" {
					t.Error("GitHub token sent to Differential")
				}
				switch scenario {
				case "rate limit":
					w.WriteHeader(http.StatusForbidden)
				case "oversized":
					_, _ = fmt.Fprint(w, strings.Repeat("a", 2000))
				case "redirect":
					http.Redirect(w, req, "https://evil.example/steal", http.StatusFound)
				}
			})
			cfg.Identifier, cfg.GitHubToken, cfg.MaxBytes = "D42", "secret", 1024
			m, err := Run(context.Background(), cfg)
			if m != nil {
				cleanupArtifacts(t, m)
			}
			if err == nil || requests.Load() != 1 {
				t.Fatalf("expected one failed request; requests=%d, error=%v", requests.Load(), err)
			}
		})
	}
}

func TestPagination(t *testing.T) {
	cfg := testConfig(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Query().Get("page") == "1" {
			w.Header().Set("Link", `<https://evil.example/ignored>; rel="next"`)
			_, _ = fmt.Fprint(w, `[{"id":1}]`)
		} else {
			_, _ = fmt.Fprint(w, `[{"id":2}]`)
		}
	})
	r := &runner{ctx: context.Background(), cfg: cfg}
	items, err := r.list(githubAPI+"/pulls/42/comments", 3)
	if err != nil || len(items) != 2 {
		t.Fatalf("pagination: %v, %v", items, err)
	}
}

func TestSafePathsAndNumstat(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "a/../../escape", ".git/config", "dir/.git/HEAD", "a/./b", "a//b", "back\\slash", "file\nname", "."} {
		if safePath(name) == nil {
			t.Errorf("accepted unsafe path %q", name)
		}
	}
	files, err := parseNumstat([]byte("1\t2\t\x00old name\x00new name\x00"))
	if err != nil || !reflect.DeepEqual(files, []changedFile{{Path: "new name", OldPath: "old name", Additions: "1", Deletions: "2"}}) {
		t.Fatalf("rename statistics: %+v, %v", files, err)
	}
}

func TestPatchOperations(t *testing.T) {
	for _, item := range []struct {
		name, patch, target, want string
		removed                   string
	}{
		{"rename", "diff --git a/file.txt b/renamed.txt\nsimilarity index 100%\nrename from file.txt\nrename to renamed.txt\n", "renamed.txt", originalFile, "file.txt"},
		{"copy", "diff --git a/file.txt b/copied.txt\nsimilarity index 100%\ncopy from file.txt\ncopy to copied.txt\n", "copied.txt", originalFile, ""},
		{"new file with spaces", "diff --git a/new file.txt b/new file.txt\nnew file mode 100644\n--- /dev/null\n+++ b/new file.txt\n@@ -0,0 +1 @@\n+created\n", "new file.txt", "created\n", ""},
		{"delete", "diff --git a/file.txt b/file.txt\ndeleted file mode 100644\n--- a/file.txt\n+++ /dev/null\n@@ -1,10 +0,0 @@\n-" + strings.ReplaceAll(strings.TrimSuffix(originalFile, "\n"), "\n", "\n-") + "\n", "", "", "file.txt"},
	} {
		t.Run(item.name, func(t *testing.T) {
			cfg := testConfig(t, func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Query().Get("download") == "true" {
					_, _ = fmt.Fprint(w, item.patch)
					return
				}
				differentialHandler(w, req)
			})
			cfg.Identifier = "D42"
			m, err := Run(context.Background(), cfg)
			if m != nil {
				cleanupArtifacts(t, m)
			}
			if err != nil || m.Workspace == "" {
				t.Fatalf("operation failed: %+v, %v", m, err)
			}
			if item.target != "" {
				data, err := os.ReadFile(filepath.Join(m.Workspace, item.target))
				if err != nil || string(data) != item.want {
					t.Fatalf("wrong proposed file: %q, %v", data, err)
				}
			}
			if item.removed != "" {
				if _, err := os.Stat(filepath.Join(m.Workspace, item.removed)); !os.IsNotExist(err) {
					t.Fatalf("removed file remains: %v", err)
				}
			}
			if data, err := os.ReadFile(filepath.Join(cfg.Repo, "file.txt")); err != nil || string(data) != originalFile {
				t.Fatal("original checkout changed")
			}
			loaded, err := LoadManifest(m.ManifestPath, cfg.MaxBytes)
			if err != nil {
				t.Fatal(err)
			}
			if err := VerifyBaseline(context.Background(), loaded, cfg.Timeout, cfg.MaxBytes); err != nil {
				t.Fatalf("proposed %s workspace failed hash verification: %v", item.name, err)
			}
		})
	}
}

func TestBinaryPayload(t *testing.T) {
	var patch string
	cfg := testConfig(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Query().Get("download") == "true" {
			_, _ = fmt.Fprint(w, patch)
			return
		}
		differentialHandler(w, req)
	})
	cfg.Identifier = "D42"
	binary := "\x00binary\x00payload\xff\n"
	file := filepath.Join(cfg.Repo, "file.txt")
	putFile(t, file, binary)
	patch = string(testGit(t, cfg.Repo, "diff", "--binary", "HEAD"))
	putFile(t, file, originalFile)
	m, err := Run(context.Background(), cfg)
	if m != nil {
		cleanupArtifacts(t, m)
	}
	if err != nil || m.Workspace == "" {
		t.Fatalf("binary operation failed: %+v, %v", m, err)
	}
	data, err := os.ReadFile(filepath.Join(m.Workspace, "file.txt"))
	if err != nil || string(data) != binary {
		t.Fatalf("wrong binary payload: %q, %v", data, err)
	}
}

func TestMissingContextIsExplicit(t *testing.T) {
	cfg := testConfig(t, func(w http.ResponseWriter, req *http.Request) {
		if strings.HasSuffix(req.URL.Path, "/reviews") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		githubHandler(w, req)
	})
	m, err := Run(context.Background(), cfg)
	if m != nil {
		cleanupArtifacts(t, m)
	}
	if err != nil || m.State != "ready" || len(m.Warnings) != 1 || !strings.Contains(m.Warnings[0], "reviews.json") {
		t.Fatalf("missing context was not reported: %+v, %v", m, err)
	}
}

func TestGitOutputLimit(t *testing.T) {
	repo := fixtureRepo(t)
	r := &runner{ctx: context.Background(), cfg: Config{Timeout: time.Second, MaxBytes: 4}}
	if _, err := r.git(repo, "show", "HEAD:file.txt"); err == nil || !strings.Contains(err.Error(), "exceeds max-bytes") {
		t.Fatalf("unbounded Git output: %v", err)
	}
}

func TestIndexChangesDuringFetch(t *testing.T) {
	var repo string
	var changeOnce sync.Once
	worktree := strings.ReplaceAll(originalFile, "original", "worktree")
	cfg := testConfig(t, func(w http.ResponseWriter, req *http.Request) {
		changeOnce.Do(func() {
			// Simulate another process changing the index while leaving the
			// working-tree content and porcelain status categories unchanged.
			file := filepath.Join(repo, "file.txt")
			if err := os.WriteFile(file, []byte(strings.ReplaceAll(originalFile, "original", "second staged version")), 0600); err != nil {
				t.Error(err)
			}
			cmd := exec.Command("git", "-C", repo, "add", "file.txt")
			if data, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("change fixture index: %v: %s", err, data)
			}
			if err := os.WriteFile(file, []byte(worktree), 0600); err != nil {
				t.Error(err)
			}
		})
		githubHandler(w, req)
	})
	repo = cfg.Repo
	putFile(t, filepath.Join(repo, "file.txt"), strings.ReplaceAll(originalFile, "original", "first staged version"))
	testGit(t, repo, "add", "file.txt")
	putFile(t, filepath.Join(repo, "file.txt"), worktree)
	m, err := Run(context.Background(), cfg)
	if m != nil {
		cleanupArtifacts(t, m)
	}
	if err == nil || !strings.Contains(err.Error(), "checkout changed") || m.State != "error" {
		t.Fatalf("missed changing index: %+v, %v", m, err)
	}
}

func TestHistoricalDifferential(t *testing.T) {
	for _, diffID := range []string{"99", "98"} {
		t.Run(diffID, func(t *testing.T) {
			cfg := testConfig(t, differentialHandler)
			cfg.Identifier = "https://reviews.freebsd.org/D42?id=" + diffID
			m, err := Run(context.Background(), cfg)
			if m != nil {
				cleanupArtifacts(t, m)
			}
			if diffID == "99" {
				if err != nil || m.Review.DiffID != diffID {
					t.Fatalf("historical diff failed: %+v, %v", m, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "historical diff") {
				t.Fatalf("silently substituted another diff: %+v, %v", m, err)
			}
		})
	}
}

func TestSnapshotDisabled(t *testing.T) {
	cfg := testConfig(t, githubHandler)
	cfg.Snapshot = false
	m, err := Run(context.Background(), cfg)
	if m != nil {
		cleanupArtifacts(t, m)
	}
	if err != nil || m.Applicability != "applies" || m.Workspace != "" {
		t.Fatalf("snapshot=false failed: %+v, %v", m, err)
	}
	if _, err := os.Stat(filepath.Join(m.Directory, "proposed")); !os.IsNotExist(err) {
		t.Fatalf("unexpected workspace: %v", err)
	}
}

func TestPreparationProgress(t *testing.T) {
	cfg := testConfig(t, githubHandler)
	var messages []string
	cfg.Progress = func(message string) { messages = append(messages, message) }
	m, err := Run(context.Background(), cfg)
	if m != nil {
		cleanupArtifacts(t, m)
	}
	if err != nil {
		t.Fatal(err)
	}
	progress := strings.Join(messages, "\n")
	for _, phase := range []string{"baseline", "Fetching GitHub PR #42", "review context", "applicability", "isolated workspace", "Preparation complete"} {
		if !strings.Contains(progress, phase) {
			t.Errorf("missing progress for %s: %s", phase, progress)
		}
	}
	if strings.Contains(progress, "@@ -1") {
		t.Fatal("progress included patch contents")
	}
}

// Live tests are opt-in because they depend on mutable services, network access,
// and a real FreeBSD source checkout. Offline tests never contact the network.
func TestLiveServices(t *testing.T) {
	repo := os.Getenv("FREEBSD_REVIEW_LIVE_REPO")
	if repo == "" {
		t.Skip("set FREEBSD_REVIEW_LIVE_REPO to a FreeBSD checkout to test both live services")
	}
	for _, id := range []string{"D53200", "https://github.com/freebsd/freebsd-src/pull/2533"} {
		t.Run(id, func(t *testing.T) {
			m, err := Run(context.Background(), Config{Identifier: id, Repo: repo, Timeout: 60 * time.Second, MaxBytes: 64 << 20, Snapshot: true, GitHubToken: os.Getenv("GITHUB_TOKEN")})
			if m != nil {
				t.Logf("artifacts: %s; applicability: %s", m.Directory, m.Applicability)
			}
			if err != nil {
				t.Fatal(err)
			}
			if m.State != "ready" || m.FileCount == 0 || m.SHA256 == "" {
				t.Fatalf("incomplete live result: %+v", m)
			}
			patch, err := os.ReadFile(m.Patch)
			if err != nil || fmt.Sprintf("%x", sha256.Sum256(patch)) != m.SHA256 {
				t.Fatalf("patch hash mismatch: %v", err)
			}
		})
	}
}
