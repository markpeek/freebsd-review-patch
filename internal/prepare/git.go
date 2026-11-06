package prepare

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

type changedFile struct {
	Path      string `json:"path"`
	OldPath   string `json:"old_path,omitempty"`
	Additions string `json:"additions"`
	Deletions string `json:"deletions"`
}

// boundedBuffer prevents a large checkout or command diagnostic from filling
// memory. Returning an error also causes os/exec's output copier to stop.
type boundedBuffer struct {
	buffer bytes.Buffer
	limit  int64
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if int64(b.buffer.Len())+int64(len(p)) > b.limit {
		return 0, errors.New("git output exceeds max-bytes")
	}
	return b.buffer.Write(p)
}

func (r *runner) git(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(r.ctx, r.cfg.Timeout)
	defer cancel()
	options := []string{"--no-optional-locks", "-c", "core.fsmonitor=false", "-C", dir}
	cmd := exec.CommandContext(ctx, "git", append(options, args...)...)
	// Ambient Git overrides could direct a supposedly read-only invocation at
	// a different worktree/index or inject configuration from the environment.
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") && !strings.HasPrefix(entry, "LC_ALL=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
	stdout, stderr := &boundedBuffer{limit: r.cfg.MaxBytes}, &boundedBuffer{limit: r.cfg.MaxBytes}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), ctx.Err())
		}
		detail := strings.TrimSpace(stderr.buffer.String())
		// Keep model-facing errors bounded even when Git reports many paths.
		// Save the full diagnostic whenever an artifact directory is available.
		if len(detail) > 2048 {
			if r.m != nil && r.m.Directory != "" {
				log := filepath.Join(r.m.Directory, "git-errors.txt")
				out, saveErr := os.OpenFile(log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
				if saveErr == nil {
					_, saveErr = fmt.Fprintf(out, "git %s\n%s\n", strings.Join(args, " "), detail)
					saveErr = errors.Join(saveErr, out.Close())
				}
				if saveErr != nil {
					return nil, fmt.Errorf("save Git diagnostics: %w", saveErr)
				}
				detail = detail[:2048] + "\nFull diagnostics: " + log
			} else {
				detail = detail[:2048] + "\n(diagnostic truncated)"
			}
		}
		return nil, fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, detail)
	}
	return stdout.buffer.Bytes(), nil
}

type baselineData struct {
	info                              Baseline
	status, changes, staged, unstaged []byte
}

func (b baselineData) same(other baselineData) bool {
	return b.info.Root == other.info.Root && b.info.Head == other.info.Head && b.info.Branch == other.info.Branch &&
		bytes.Equal(b.status, other.status) && bytes.Equal(b.changes, other.changes) &&
		bytes.Equal(b.staged, other.staged) && bytes.Equal(b.unstaged, other.unstaged)
}

func (r *runner) baseline() (baselineData, error) {
	root, err := r.git(r.cfg.Repo, "rev-parse", "--show-toplevel")
	if err != nil {
		return baselineData{}, err
	}
	repo, err := filepath.EvalSymlinks(strings.TrimSpace(string(root)))
	if err != nil {
		return baselineData{}, err
	}
	head, err := r.git(repo, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return baselineData{}, err
	}
	branch, err := r.git(repo, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return baselineData{}, err
	}
	status, err := r.git(repo, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return baselineData{}, err
	}
	changes, err := r.git(repo, "diff", "--no-ext-diff", "--no-textconv", "--binary", "HEAD", "--")
	if err != nil {
		return baselineData{}, err
	}
	staged, err := r.git(repo, "diff", "--no-ext-diff", "--no-textconv", "--binary", "--cached", "HEAD", "--")
	if err != nil {
		return baselineData{}, err
	}
	unstaged, err := r.git(repo, "diff", "--no-ext-diff", "--no-textconv", "--binary", "--")
	if err != nil {
		return baselineData{}, err
	}
	return baselineData{
		info:   Baseline{Root: repo, Head: strings.TrimSpace(string(head)), Branch: strings.TrimSpace(string(branch)), Dirty: len(status) != 0},
		status: status, changes: changes, staged: staged, unstaged: unstaged,
	}, nil
}

func safePath(name string) error {
	if name == "" || name == "." || path.IsAbs(name) || path.Clean(name) != name || strings.Contains(name, "\\") || !utf8.ValidString(name) {
		return fmt.Errorf("unsafe patch path %q", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." || strings.EqualFold(part, ".git") {
			return fmt.Errorf("unsafe patch path %q", name)
		}
	}
	for _, char := range name {
		if char < 32 || char == 127 {
			return fmt.Errorf("unsupported control character in patch path %q", name)
		}
	}
	return nil
}

func parseNumstat(data []byte) ([]changedFile, error) {
	parts := bytes.Split(data, []byte{0})
	var files []changedFile
	seen := make(map[string]bool)
	for i := 0; i < len(parts)-1; i++ {
		fields := strings.SplitN(string(parts[i]), "\t", 3)
		if len(fields) != 3 {
			return nil, errors.New("malformed git numstat output")
		}
		file := changedFile{Path: fields[2], Additions: fields[0], Deletions: fields[1]}
		if file.Path == "" {
			if i+2 >= len(parts)-1 {
				return nil, errors.New("malformed rename in git numstat output")
			}
			file.OldPath, file.Path = string(parts[i+1]), string(parts[i+2])
			i += 2
			if err := safePath(file.OldPath); err != nil {
				return nil, err
			}
		}
		if err := safePath(file.Path); err != nil {
			return nil, err
		}
		if seen[file.Path] {
			return nil, fmt.Errorf("duplicate patch path %q", file.Path)
		}
		seen[file.Path] = true
		for _, count := range []string{file.Additions, file.Deletions} {
			if count != "-" {
				if n, err := strconv.Atoi(count); err != nil || n < 0 {
					return nil, errors.New("invalid git numstat count")
				}
			}
		}
		files = append(files, file)
	}
	if len(files) == 0 || len(parts[len(parts)-1]) != 0 {
		return nil, errors.New("diff contains no files or has malformed statistics")
	}
	return files, nil
}

func (r *runner) checkPatch() error {
	patch, err := os.ReadFile(r.m.Patch)
	if err != nil {
		return err
	}
	if bytes.HasPrefix(bytes.TrimSpace(patch), []byte("diff --git ")) {
		r.m.Strip = 1
	}
	strip := "-p" + strconv.Itoa(r.m.Strip)
	stat, err := r.git(r.m.Baseline.Root, "apply", strip, "--numstat", "-z", "--", r.m.Patch)
	if err != nil {
		return err
	}
	files, err := parseNumstat(stat)
	if err != nil {
		return err
	}
	// Metadata also names rename/copy sources. Validate all of them before
	// copying anything into the isolated workspace.
	paths := make(map[string]bool)
	for _, file := range files {
		paths[file.Path] = true
		if file.OldPath != "" {
			paths[file.OldPath] = true
		}
	}
	for _, line := range strings.Split(string(patch), "\n") {
		if !strings.HasPrefix(line, "rename from ") && !strings.HasPrefix(line, "copy from ") {
			continue
		}
		name := strings.TrimPrefix(strings.TrimPrefix(line, "rename from "), "copy from ")
		if strings.HasPrefix(name, `"`) {
			name, err = strconv.Unquote(name)
			if err != nil {
				return fmt.Errorf("invalid quoted patch path: %w", err)
			}
		}
		if err := safePath(name); err != nil {
			return err
		}
		paths[name] = true
	}
	if r.expected != nil {
		if len(files) != len(r.expected) {
			return errors.New("diff file count does not match GitHub metadata")
		}
		byName := make(map[string]changedFile)
		for _, f := range files {
			byName[f.Path] = f
		}
		for _, remote := range r.expected {
			if err := safePath(remote.Filename); err != nil {
				return err
			}
			if remote.PreviousFilename != "" {
				if err := safePath(remote.PreviousFilename); err != nil {
					return err
				}
				paths[remote.PreviousFilename] = true
			}
			file, found := byName[remote.Filename]
			if !found || (file.Additions != "-" && (file.Additions != strconv.Itoa(remote.Additions) || file.Deletions != strconv.Itoa(remote.Deletions))) {
				return fmt.Errorf("diff does not match GitHub metadata for %q", remote.Filename)
			}
		}
	}
	r.m.Files, r.m.FileCount = filepath.Join(r.m.Directory, "files.json"), len(files)
	if err := writeJSON(r.m.Files, files); err != nil {
		return err
	}
	stat, err = r.git(r.m.Baseline.Root, "apply", strip, "--stat", "--", r.m.Patch)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(r.m.Directory, "stat.txt"), stat, 0600); err != nil {
		return err
	}
	if bytes.Contains(patch, []byte("\nBinary files ")) || bytes.Contains(patch, []byte("\nFiles ")) {
		return errors.New("diff contains binary changes without payloads; reconstruct a binary diff from commits manually")
	}
	r.m.Diagnostics = filepath.Join(r.m.Directory, "apply-check.txt")
	_, checkErr := r.git(r.m.Baseline.Root, "apply", strip, "--check", "--whitespace=nowarn", "--", r.m.Patch)
	if checkErr != nil {
		if errors.Is(checkErr, context.Canceled) || errors.Is(checkErr, context.DeadlineExceeded) {
			return checkErr
		}
		r.m.Applicability = "conflicts"
		log := checkErr.Error() + "\n"
		_, reverseErr := r.git(r.m.Baseline.Root, "apply", strip, "--reverse", "--check", "--whitespace=nowarn", "--", r.m.Patch)
		if reverseErr == nil {
			log += "Reverse check succeeds. Investigate whether the change is already present.\n"
			r.m.Warnings = append(r.m.Warnings, "reverse applicability check succeeds; this alone does not establish whether the patch landed")
		} else {
			log += "Reverse check:\n" + reverseErr.Error() + "\n"
		}
		return os.WriteFile(r.m.Diagnostics, []byte(log), 0600)
	}
	r.m.Applicability = "applies"
	if err := os.WriteFile(r.m.Diagnostics, []byte("Forward applicability check passed.\n"), 0600); err != nil {
		return err
	}
	if r.cfg.Snapshot {
		if err := r.snapshot(paths, strip); err != nil {
			r.m.Warnings = append(r.m.Warnings, "workspace unavailable: "+err.Error())
		}
	}
	return nil
}

func (r *runner) snapshot(paths map[string]bool, strip string) error {
	dir := filepath.Join(r.m.Directory, "proposed")
	if err := os.Mkdir(dir, 0700); err != nil {
		return err
	}
	root, err := os.OpenRoot(r.m.Baseline.Root)
	if err != nil {
		return err
	}
	// Read-only handle closes cannot affect the copied file contents.
	defer func() { _ = root.Close() }()
	var names []string
	for name := range paths {
		names = append(names, name)
	}
	sort.Strings(names)
	var copied int64
	for _, name := range names {
		// Reject symlinks rather than dereferencing or reproducing links into
		// another tree. OpenRoot also confines reads if a path races this check.
		parts := strings.Split(name, "/")
		missing := false
		for i := range parts {
			info, err := root.Lstat(strings.Join(parts[:i+1], "/"))
			if errors.Is(err, os.ErrNotExist) {
				missing = true
				break
			}
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlink at %q requires manual reconstruction", name)
			}
		}
		if missing {
			continue
		}
		source, err := root.Open(name)
		if err != nil {
			return err
		}
		info, err := source.Stat()
		if err != nil || !info.Mode().IsRegular() {
			_ = source.Close()
			return fmt.Errorf("%q is not a readable regular file", name)
		}
		copied += info.Size()
		if copied > r.cfg.MaxBytes {
			_ = source.Close()
			return errors.New("affected source files exceed max-bytes; use --snapshot=false or increase the limit")
		}
		target := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			_ = source.Close()
			return err
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
		if err != nil {
			_ = source.Close()
			return err
		}
		n, copyErr := io.Copy(out, io.LimitReader(source, info.Size()+1))
		closeErr := out.Close()
		_ = source.Close()
		if copyErr != nil || closeErr != nil {
			return errors.Join(copyErr, closeErr)
		}
		if n != info.Size() {
			return fmt.Errorf("%q changed size during snapshot", name)
		}
	}
	if _, err := r.git(dir, "-c", "init.templateDir=", "init", "-q", "--initial-branch=main"); err != nil {
		return err
	}
	if _, err := r.git(dir, "apply", strip, "--whitespace=nowarn", "--", r.m.Patch); err != nil {
		return err
	}
	r.m.Workspace = dir
	return nil
}
