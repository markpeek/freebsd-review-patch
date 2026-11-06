# FreeBSD patch review

Repository guidance and a skill for Codex and Claude Code for reviewing
Differential reviews from reviews.freebsd.org and GitHub pull requests in
`freebsd/freebsd-src` against a local FreeBSD source checkout.

- [AGENTS.md](AGENTS.md) provides guidance for working in this repository.
- [AGENTS-SRC.md](AGENTS-SRC.md) provides guidance for working in the
  FreeBSD base system source tree, where it is installed as `AGENTS.md`.
- [freebsd-review-patch](skills/freebsd-review-patch/SKILL.md) describes how
  to fetch and review patches from either source and report actionable
  findings.
- [freebsd-review-prepare](cmd/freebsd-review-prepare) is an optional Go
  helper that prepares review artifacts and returns a compact JSON manifest.

## Installation

Run these commands from this repository's root. Replace
`/path/to/freebsd-src` with the root of your FreeBSD source checkout.

Copy `AGENTS-SRC.md` into the FreeBSD source tree as `AGENTS.md`:

```sh
cp -i AGENTS-SRC.md /path/to/freebsd-src/AGENTS.md
```

If the checkout already has an `AGENTS.md`, merge the relevant guidance
from `AGENTS-SRC.md` into that file.

### Codex

Copy the complete skill directory, including its `agents` subdirectory,
into `~/.codex/skills`:

```sh
mkdir -p ~/.codex/skills
cp -R skills/freebsd-review-patch ~/.codex/skills/
```

The installed skill should be at
`~/.codex/skills/freebsd-review-patch/SKILL.md`.

### Claude Code

Copy the same skill directory into `~/.claude/skills`:

```sh
mkdir -p ~/.claude/skills
cp -R skills/freebsd-review-patch ~/.claude/skills/
```

The installed skill should be at
`~/.claude/skills/freebsd-review-patch/SKILL.md`.

Claude Code uses the metadata and instructions in `SKILL.md`. The included
`agents/openai.yaml` provides OpenAI-specific interface metadata and can
remain in the copied directory.

### Optional preparation helper

The skill works with or without the helper. To build it, use Go 1.25 or
later from this repository's root:

```sh
make build
mkdir -p ~/.local/bin
install -m 755 bin/freebsd-review-prepare ~/.local/bin/
```

Ensure `~/.local/bin` is on PATH in your agent's environment. Alternatively,
place the binary at `scripts/freebsd-review-prepare` inside the installed
skill directory. Codex and Claude Code can use the same binary on the same
OS and architecture.

The resulting binary uses Git and the system's HTTPS certificate trust
store at runtime. It does not require Go, curl, Python, or the GitHub CLI
after installation. Dependencies for flag parsing (`pflag`) and HTML
parsing (`golang.org/x/net/html`) are compiled into the binary.

## Usage

Work from your FreeBSD source checkout.

### Codex

Ask Codex to review a Differential review:

```text
Use $freebsd-review-patch to review D12345 against the current FreeBSD source tree.
```

Replace `D12345` with the review number or its
`https://reviews.freebsd.org/D12345` URL.

For a GitHub pull request in `freebsd/freebsd-src`:

```text
Use $freebsd-review-patch to review PR #12345 against the current FreeBSD source tree.
```

Replace `PR #12345` with the pull request number, retaining the `PR #`
prefix, or use its `https://github.com/freebsd/freebsd-src/pull/12345` URL.

### Claude Code

Invoke the skill with a Differential review:

```text
/freebsd-review-patch D12345
```

For a GitHub pull request in `freebsd/freebsd-src`:

```text
/freebsd-review-patch PR #12345
```

Replace the example identifiers with your review or pull request number,
or supply the full review/PR URL. The skill's `argument-hint` shows these
accepted inputs during command autocomplete. See the
[Claude Code skill documentation](https://code.claude.com/docs/en/skills)
for more about installation and invocation.

The skill reports findings in chat and preserves the checkout. Posting
comments or reviews, approving, merging, or changing a review's status
requires a separate request.

## Preparation helper

Run the helper directly against a FreeBSD source checkout:

```sh
freebsd-review-prepare --repo /path/to/freebsd-src D53200
freebsd-review-prepare --repo /path/to/freebsd-src 'PR #2533'
```

Full review/PR URLs are also accepted. A Differential URL with
`?id=164628` selects that historical diff. GitHub preparation uses the
current PR version, recording and rechecking its base/head SHAs. For
historical GitHub versions, use the skill's manual workflow.

The helper validates the identifier, records the checkout baseline,
downloads the complete diff and context, computes its SHA-256, and checks
applicability without modifying the checkout or index. It checks GitHub's
diff against the paginated file metadata and pins Differential downloads
to a numeric diff ID. HTTP requests are read-only. Set `GITHUB_TOKEN` or
`GH_TOKEN` when authentication or higher GitHub API limits are needed;
tokens are sent only to `api.github.com` and are not saved to artifacts.

Standard output is one JSON manifest containing artifact paths, version
identifiers, changed-file count, applicability, and warnings. Detailed
artifacts are saved in a new temporary directory:

- `manifest.json`: the same manifest, with preparation state and errors.
- `review.patch`, `files.json`, and `stat.txt`: the diff and file statistics.
- `status.z` and `local.patch`: NUL-delimited Git porcelain status and
  the tracked working-tree changes against HEAD. `staged.patch` and
  `unstaged.patch` also record the index and working-tree deltas separately.
- `context/`: review metadata and discussion; GitHub also includes commits
  and check results. Differential includes HTML and extracted text.
- `apply-check.txt`: applicability diagnostics.
- `proposed/`: a sparse workspace of affected files with the patch applied,
  when applicable and reconstruction succeeds. It preserves the affected
  files' current local contents and is not a full build workspace.

Use `--output /path/to/new-directory` to choose an artifact location outside
the checkout. The directory must not already exist. `--snapshot=false`
skips workspace reconstruction. `--timeout` defaults to 60 seconds per
request/command, and `--max-bytes` defaults to 64 MiB per HTTP response,
paginated API list, Git command output, or combined source snapshot.
Run `freebsd-review-prepare --help` for all options.

A patch conflict is a successful preparation with
`"applicability":"conflicts"`; inspect its diagnostics before drawing
conclusions. Retrieval or validation errors exit nonzero and retain an
error manifest when the artifact directory has been created. Missing
discussion, incomplete check results, or unavailable workspace
reconstruction appear as warnings. Binary diffs without payloads and
unsupported retrieval cases require the manual fallback. The agent still
reads the relevant code, investigates defects, and runs appropriate tests.

Artifacts remain available after the command exits; remove their directory
when the review is finished.

## Testing

Offline tests use temporary Git repositories and local HTTP fixtures for
both services, including version changes, pagination, invalid downloads,
unsafe paths, conflicts, and preservation of local edits and the index:

```sh
make test
make ci
```

`make ci` checks formatting and module files, verifies downloaded modules,
runs `go vet`, runs the test suite with the race detector, runs
`golangci-lint`, and builds the static binary. It requires
[golangci-lint v2](https://golangci-lint.run/docs/welcome/install/)
built with a Go version that supports this project's Go sources. Race
tests also require CGO and a C compiler on a platform supported by Go's
race detector. Live service tests are excluded from `make test` and
`make ci`.

Use `make fmt` to format Go sources. Individual checks are available as
`make fmt-check`, `make mod-check`, `make vet`, `make test-race`, and
`make lint`. The Makefile works with FreeBSD make and GNU make. Override
`GO`, `GOFMT`, or `GOLANGCI_LINT` to use a different tool executable.

To exercise both real services against an existing FreeBSD source checkout:

```sh
FREEBSD_REVIEW_LIVE_REPO=/path/to/freebsd-src go test ./internal/prepare -run TestLiveServices -count=1 -v
```

The live tests use Differential `D53200` and GitHub PR `2533`. They retain
their artifact directories and print the paths for inspection. They need
network access and may be affected by service availability or rate limits.
