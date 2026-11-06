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
- [freebsd-review](cmd/freebsd-review) is an optional Go
  helper that prepares artifacts and optionally runs headless code reviews.

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

### Optional review helper

The skill works with or without the helper. To build it, use Go 1.25 or
later from this repository's root:

```sh
make build
mkdir -p ~/.local/bin
install -m 755 bin/freebsd-review ~/.local/bin/
```

Ensure `~/.local/bin` is on PATH in your agent's environment. Alternatively,
place the binary at `scripts/freebsd-review` inside the installed
skill directory. Codex and Claude Code can use the same binary on the same
OS and architecture.

The resulting binary uses Git and the system's HTTPS certificate trust
store at runtime. It does not require Go, curl, Python, or the GitHub CLI
after installation. Dependencies for flag parsing (`pflag`) and HTML
parsing (`golang.org/x/net/html`) are compiled into the binary.

Headless review is optional and additionally requires the selected `codex`
or `claude` CLI on PATH, authenticated using that CLI's supported login or
environment configuration. No model SDK is linked into the Go binary.

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
freebsd-review --repo /path/to/freebsd-src D53200
freebsd-review --repo /path/to/freebsd-src 'PR #2533'
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
`GH_TOKEN` when authentication or higher GitHub API limits are needed
(`GITHUB_TOKEN` takes precedence). Tokens are sent only to `api.github.com`
and are not saved to artifacts. If GitHub rejects a token with HTTP 401,
the helper retries once anonymously and uses anonymous requests for the
rest of the run, recording a warning. Anonymous API rate limits apply;
HTTP 403 and other errors are reported without this retry. To ignore both
token variables explicitly, run:

```sh
env -u GITHUB_TOKEN -u GH_TOKEN freebsd-review --repo /path/to/freebsd-src 'PR #2527'
```

Preparation-only runs print one JSON manifest containing artifact paths, version
identifiers, changed-file count, applicability, and warnings. Detailed
artifacts are saved in a new temporary directory:

- `manifest.json`: the saved JSON manifest, with preparation state and errors.
- `review.patch`, `files.json`, and `stat.txt`: the diff and file statistics.
- `status.z` and `local.patch`: NUL-delimited Git porcelain status and
  the tracked working-tree changes against HEAD. `staged.patch` and
  `unstaged.patch` also record the index and working-tree deltas separately.
- `context/`: review metadata and discussion; GitHub also includes commits
  and check results. Differential includes HTML and extracted text.
- `apply-check.txt`: applicability diagnostics.
- `proposed/`: a sparse workspace of affected files with the patch applied,
  when applicable and reconstruction succeeds. It preserves the affected
  files' current local contents and is not a full build workspace. The
  manifest's `workspace_files` records each file's SHA-256 and mode;
  symlink hashes cover the link target text without following the link.
  Git metadata is excluded. Without a workspace, this field is null.

Use `--output /path/to/new-directory` to choose an artifact location outside
the checkout. The directory must not already exist. `--snapshot=false`
skips workspace reconstruction. `--timeout` defaults to 60 seconds per
request/command, and `--max-bytes` defaults to 64 MiB per HTTP response,
paginated API list, Git command output, or combined source snapshot.
Run `freebsd-review --help` for all options.

A patch conflict is a successful preparation with
`"applicability":"conflicts"`; inspect its diagnostics before drawing
conclusions. Retrieval or validation errors exit nonzero and retain an
error manifest when the artifact directory has been created. Missing
discussion, incomplete check results, or unavailable workspace
reconstruction appear as warnings. Binary diffs without payloads and
unsupported retrieval cases require the manual fallback. The agent still
reads the relevant code, investigates defects, and runs appropriate tests
in the normal interactive workflow.

Artifacts remain available after the command exits; remove their directory
when the review is finished.

## Optional headless review

Add `--reviewer codex` or `--reviewer claude` to prepare artifacts and then
run a non-interactive code review:

```sh
freebsd-review --repo /path/to/freebsd-src --reviewer codex D53200
freebsd-review --repo /path/to/freebsd-src --reviewer claude 'PR #2533'
```

Headless reviews print the Markdown report to stdout by default, including
headings, findings, evidence, validation, and limitations. Add `--json` to
print the compact manifest instead, with paths to the saved Markdown report
and structured findings:

```sh
freebsd-review --repo /path/to/freebsd-src --reviewer codex --json 'PR #2533'
```

Progress goes to stderr, including preparation stages, artifact locations,
and a status update every 15 seconds while a reviewer is running. Use `-q`
or `--quiet` to suppress progress; results and errors remain visible. For
example, to capture a report without progress messages:

```sh
freebsd-review --repo /path/to/freebsd-src --reviewer claude -q 'PR #2533' > review.md
```

Output selection affects presentation only. Both backends still produce
validated structured findings internally, and `manifest.json`,
`findings.json`, and `review.md` are saved regardless of `--json`. On failure,
default review mode leaves stdout empty and writes errors and artifact paths
to stderr; `--json` also prints the error manifest when available.

Preparation-only behavior remains the default (`--reviewer none`). To
review existing artifacts without another download, provide their saved
manifest instead of an identifier:

```sh
freebsd-review --manifest /tmp/freebsd-review-example/manifest.json --reviewer codex
```

`--manifest` uses the recorded source checkout and cannot be combined with
an identifier, `--repo`, `--output`, or `--snapshot`. The helper verifies the
source baseline, patch hash, changed-file metadata, and recorded workspace
content hashes and modes before review, and checks them again afterward.
Changed, added, or removed workspace files fail verification. If the checkout
or workspace has changed, prepare again. Older manifests with a workspace
but no recorded content hashes also require preparation again.

Use `--model MODEL` to select a model supported by the chosen CLI; otherwise
the CLI's default applies. `--review-timeout` defaults to 30 minutes and is
separate from the preparation request/command timeout. The helper cancels
the reviewer on timeout or interruption; on Unix it kills its process group,
including descendants. Reviewer stdout and stderr share the `--max-bytes`
limit, and the final structured response is also size-limited.

Codex runs through `codex exec` with a read-only sandbox, approvals disabled,
ephemeral sessions, and user configuration and user/project execution rules
disabled (`--ignore-user-config --ignore-rules`). It uses existing CLI
authentication; use `--model` for an explicit model selection. Claude runs
through `claude --print` with only `Read`, `Grep`, and `Glob` tools available,
permission prompts denied, hooks disabled, empty MCP configuration, and
user/project settings and skill discovery disabled. Unsupported CLI flags
or unavailable sandbox facilities fail explicitly; the helper never retries
with permission or sandbox bypass flags.

Reviewer processes receive only an allowlisted environment (execution,
proxy, locale, and Anthropic, OpenAI, Codex, AWS, Google, and Azure
variables for CLI authentication); `GITHUB_TOKEN` and `GH_TOKEN` are never
passed. Codex's read-only sandbox restricts writes, not reads, so the
reviewer can read files your account can read. Review untrusted patches
only where that is acceptable.

Both reviewers receive the prepared manifest and the same
[review guidance](skills/freebsd-review-patch/references/review-guidance.md)
used by the skill, embedded at build time. They inspect the original source
and proposed files without launching another reviewer. Headless reviews do
not execute builds or tests or post results to either service. A sparse
workspace remains unsuitable for builds. Model inference still consumes
tokens and may incur costs; moving it into a child process does not guarantee
lower total token usage.

Each attempt creates a separate `analysis-<reviewer>-<random>/` directory:

- `prompt.txt` and `schema.json`: the exact request and findings schema.
- `events.jsonl` and `stderr.log`: raw CLI output and diagnostics. Claude's
  event file contains its single JSON result envelope.
- `response.json`: Codex's raw final structured response, when produced.
- `findings.json`: the validated common findings format.
- `review.md`: a rendered report with review version, baseline,
  applicability, findings, validation, limitations, and preparation warnings.

The saved manifest's optional `analysis` object records reviewer, requested model,
timestamps, artifact paths, state, and finding count. Findings must include
priority, a changed repository path, a line range inside a patch hunk on the
stated side, description, and evidence. Structural validation does not prove the findings are correct.

A successful review exits zero even when it reports defects. An empty
findings array is a completed review with zero findings. Authentication
failures, timeouts, nonzero CLI exits, and malformed or missing structured
results exit nonzero and set an explicit analysis error state. Preparation
stays `ready`, and its artifacts can be reused. Earlier attempts remain on
disk when rerunning against the same manifest, and the manifest lists them
in `previous_analyses`.

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

The live retrieval tests use Differential `D53200` and GitHub PR `2533`.
They retain their artifact directories and print the paths for inspection. They need
network access and may be affected by service availability or rate limits.

Headless adapter tests use fake executables and do not contact model APIs.
To make an authenticated, potentially billable headless call against a small
temporary regression fixture, explicitly opt in:

```sh
FREEBSD_REVIEW_LIVE_REVIEWER=codex go test ./internal/review -run TestLiveHeadless -count=1 -v
FREEBSD_REVIEW_LIVE_REVIEWER=claude go test ./internal/review -run TestLiveHeadless -count=1 -v
```

Set `FREEBSD_REVIEW_LIVE_MODEL` to test a particular model. Live headless
tests have a two-minute review timeout and are excluded from `make test`
and `make ci`.
