package prepare

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
)

// WorkspaceFile records regular-file contents or a symlink's target text.
// Mode also detects changes to file type and executable permissions.
type WorkspaceFile struct {
	SHA256 string      `json:"sha256"`
	Mode   fs.FileMode `json:"mode"`
}

// workspaceFiles inventories the entire sparse tree, excluding only the
// helper-created Git metadata. Symlinks are hashed without following them.
func workspaceFiles(ctx context.Context, dir string, maxBytes int64) (map[string]WorkspaceFile, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	files := make(map[string]WorkspaceFile)
	remaining := maxBytes
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == ".git" {
			if !entry.IsDir() {
				return errors.New("proposed workspace Git metadata is not a directory")
			}
			return fs.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		if err := safePath(name); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		hash := sha256.New()
		var n int64
		switch {
		case info.Mode().IsRegular():
			file, err := root.Open(name)
			if err != nil {
				return err
			}
			n, err = io.Copy(hash, io.LimitReader(file, remaining+1))
			closeErr := file.Close()
			if err != nil || closeErr != nil {
				return errors.Join(err, closeErr)
			}
		case info.Mode()&os.ModeSymlink != 0:
			target, err := root.Readlink(name)
			if err != nil {
				return err
			}
			n = int64(len(target))
			_, _ = io.WriteString(hash, target)
		default:
			return fmt.Errorf("unsupported workspace file type: %s", name)
		}
		remaining -= n
		if remaining < 0 {
			return errors.New("proposed workspace contents exceed max-bytes")
		}
		files[name] = WorkspaceFile{SHA256: fmt.Sprintf("%x", hash.Sum(nil)), Mode: info.Mode().Type() | info.Mode().Perm()}
		return nil
	})
	return files, err
}

func validateWorkspaceFiles(m *Manifest) error {
	if m.Workspace == "" {
		if m.WorkspaceFiles != nil {
			return errors.New("workspace hashes provided without a proposed workspace")
		}
		return nil
	}
	if m.WorkspaceFiles == nil {
		return errors.New("proposed workspace content hashes are missing; prepare the review again")
	}
	for name, file := range m.WorkspaceFiles {
		if err := safePath(name); err != nil {
			return err
		}
		if !sha256Hex.MatchString(file.SHA256) ||
			(file.Mode.Type() != 0 && file.Mode.Type() != os.ModeSymlink) || file.Mode != file.Mode.Type()|file.Mode.Perm() {
			return fmt.Errorf("invalid workspace content hash metadata for %s", name)
		}
	}
	return nil
}

func verifyWorkspace(ctx context.Context, m *Manifest, maxBytes int64) error {
	if err := validateWorkspaceFiles(m); err != nil {
		return err
	}
	if m.Workspace == "" {
		return nil
	}
	resolved, err := filepath.EvalSymlinks(m.Workspace)
	if err != nil || resolved != m.Workspace {
		return errors.New("invalid proposed workspace path")
	}
	files, err := workspaceFiles(ctx, m.Workspace, maxBytes)
	if err != nil {
		return fmt.Errorf("verify proposed workspace: %w", err)
	}
	if !maps.Equal(files, m.WorkspaceFiles) {
		return errors.New("proposed workspace does not match recorded content hashes; prepare the review again")
	}
	return nil
}
