package prepare

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func preparedWorkspace(t *testing.T) (*Manifest, Config) {
	t.Helper()
	cfg := testConfig(t, githubHandler)
	m, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	cleanupArtifacts(t, m)
	loaded, err := LoadManifest(m.ManifestPath, cfg.MaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	return loaded, cfg
}

func TestRecordedWorkspaceHashes(t *testing.T) {
	m, cfg := preparedWorkspace(t)
	want := strings.ReplaceAll(originalFile, "before", "after")
	if got := m.WorkspaceFiles["file.txt"].SHA256; got != fmt.Sprintf("%x", sha256.Sum256([]byte(want))) {
		t.Fatalf("hash does not describe proposed contents: %s", got)
	}
	if len(m.WorkspaceFiles) != 1 {
		t.Fatalf("unexpected workspace inventory: %+v", m.WorkspaceFiles)
	}
	if err := VerifyBaseline(context.Background(), m, cfg.Timeout, cfg.MaxBytes); err != nil {
		t.Fatalf("unchanged workspace rejected: %v", err)
	}
}

func TestWorkspaceChangesRejected(t *testing.T) {
	for _, change := range []string{"modify", "add", "remove", "mode", "symlink", "directory symlink", "git metadata"} {
		t.Run(change, func(t *testing.T) {
			m, cfg := preparedWorkspace(t)
			file := filepath.Join(m.Workspace, "file.txt")
			switch change {
			case "modify":
				// Keep the same size to ensure verification checks contents.
				putFile(t, file, strings.ReplaceAll(originalFile, "before", "other"))
			case "add":
				putFile(t, filepath.Join(m.Workspace, "extra.txt"), "extra\n")
			case "remove":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := os.Chmod(file, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(cfg.Repo, "file.txt"), file); err != nil {
					t.Fatal(err)
				}
			case "directory symlink":
				if err := os.Rename(m.Workspace, m.Workspace+"-moved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(m.Workspace+"-moved", m.Workspace); err != nil {
					t.Fatal(err)
				}
			case "git metadata":
				if err := os.Rename(filepath.Join(m.Workspace, ".git"), filepath.Join(m.Workspace, ".git-moved")); err != nil {
					t.Fatal(err)
				}
				putFile(t, filepath.Join(m.Workspace, ".git"), "unexpected file\n")
			}
			if err := VerifyBaseline(context.Background(), m, cfg.Timeout, cfg.MaxBytes); err == nil {
				t.Fatal("accepted altered proposed workspace")
			}
			if data, err := os.ReadFile(filepath.Join(cfg.Repo, "file.txt")); err != nil || string(data) != originalFile {
				t.Fatal("verification changed the checkout")
			}
		})
	}
}

func TestMissingOrInvalidWorkspaceHashes(t *testing.T) {
	for _, change := range []string{"missing", "invalid hash", "unsafe path", "invalid mode", "no workspace"} {
		t.Run(change, func(t *testing.T) {
			m, cfg := preparedWorkspace(t)
			file := m.WorkspaceFiles["file.txt"]
			switch change {
			case "missing":
				m.WorkspaceFiles = nil
			case "invalid hash":
				file.SHA256 = "invalid"
				m.WorkspaceFiles["file.txt"] = file
			case "unsafe path":
				m.WorkspaceFiles["../outside"] = file
			case "invalid mode":
				file.Mode = os.ModeNamedPipe
				m.WorkspaceFiles["file.txt"] = file
			case "no workspace":
				m.Workspace = ""
			}
			if err := SaveManifest(m); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadManifest(m.ManifestPath, cfg.MaxBytes); err == nil {
				t.Fatal("loaded invalid workspace hash metadata")
			}
			if err := VerifyBaseline(context.Background(), m, cfg.Timeout, cfg.MaxBytes); err == nil {
				t.Fatal("verified invalid workspace hash metadata")
			}
		})
	}
}

func TestWorkspaceSymlinkHashesAndLimits(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	putFile(t, outside, "contents outside the workspace\n")
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	files, err := workspaceFiles(context.Background(), dir, int64(len(outside)))
	if err != nil {
		t.Fatal(err)
	}
	if file := files["link"]; file.SHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(outside))) || file.Mode.Type() != os.ModeSymlink {
		t.Fatalf("wrong symlink hash: %+v", file)
	}
	m := &Manifest{Workspace: dir, WorkspaceFiles: files}
	putFile(t, outside, "changing target contents does not change the symlink\n")
	if err := verifyWorkspace(context.Background(), m, 1<<20); err != nil {
		t.Fatalf("followed symlink outside workspace: %v", err)
	}
	if _, err := workspaceFiles(context.Background(), dir, int64(len(outside)-1)); err == nil {
		t.Fatal("symlink target text exceeded the byte budget")
	}
	putFile(t, filepath.Join(dir, "file"), "12345")
	if _, err := workspaceFiles(context.Background(), dir, int64(len(outside)+4)); err == nil {
		t.Fatal("regular file contents exceeded the combined byte budget")
	}
}
