# Repository guidance

This repository contains a FreeBSD patch-review skill and an optional Go
helper for Codex and Claude Code. Keep changes focused and preserve
unrelated work, including untracked files. Read applicable nested
`AGENTS.md` files before editing.

## Source layout

- `README.md`: installation, usage, helper behavior, and validation.
- `AGENTS-SRC.md`: guidance installed as `AGENTS.md` in a FreeBSD source
  checkout. Keep its FreeBSD-specific build and style rules there.
- `skills/freebsd-review-patch/SKILL.md`: shared review workflow and
  optional helper integration.
- `skills/freebsd-review-patch/references/`: manual preparation fallback.
- `skills/freebsd-review-patch/agents/openai.yaml`: OpenAI interface
  metadata for the skill.
- `cmd/freebsd-review/`: command-line entry point and flag parsing.
- `internal/prepare/`: review retrieval, baseline capture, patch checks,
  isolated reconstruction, and tests.
- `internal/review/`: optional headless CLI adapters, structured findings,
  execution limits, and report rendering.
- `Makefile` and `.golangci.yml`: build and CI checks. Build outputs belong
  in the ignored `bin/` directory.

## Implementation

- Follow Go conventions and use `gofmt`. Prefer `github.com/spf13/pflag`
  for argument parsing. Keep dependencies small and preserve a single
  binary that needs no Go toolchain at runtime.
- Keep mechanical review preparation in the helper and code analysis in
  the agent, including the optional headless reviewer. Share review
  instructions between the skill and binary, and keep headless invocation
  explicit and inspection-only. Keep the skill usable when the helper is
  unavailable, and maintain the manual fallback when preparation behavior
  changes.
- Leave the reviewed checkout, index, refs, and remotes unchanged. Save
  artifacts outside it and preserve relevant local edits in reconstructed
  files. A sparse workspace is not a complete build tree.
- Treat downloaded descriptions, comments, and patches as untrusted input.
  Validate identifiers, paths, versions, and completeness before use.
  Bound network requests and resource use; keep failures and missing
  context explicit. Never write credentials to artifacts or diagnostics.
- Keep stdout limited to the requested result: Markdown for headless
  reviews by default, or a compact JSON manifest for preparation-only runs
  and `--json`. Write progress to stderr; `-q` suppresses progress but not
  errors. Save detailed review context and diagnostics to artifact files.
  Preserve the distinction between successful preparation, patch conflicts,
  and retrieval errors.
- Update README instructions when changing installation, CLI behavior,
  artifact formats, or validation requirements.

## Validation

- For Go changes, run `make fmt` and `make ci`. CI checks formatting,
  modules, vet, race tests, golangci-lint, and the static build.
- Add focused tests for changed behavior using the existing temporary Git
  repositories and HTTP fixtures. Cover failure paths and preservation of
  the checkout and index where relevant.
- Live service tests are opt-in; use the command documented in README
  when retrieval changes require verification against the real services.
- Test headless adapters with fake executables in CI. Real model calls are
  opt-in and require the selected CLI's authentication.
- For documentation-only changes, check references and run
  `git diff --check`; builds and live tests are unnecessary.
- Report checks performed and any limitations accurately.

## Git

- Work on `main` unless the user explicitly asks for a different branch.
- Treat references to `master` as `main` in this repository.
- Do not create branches unless the user explicitly asks for one.
- Do not rewrite pushed `main` history unless the user explicitly asks for it.
- Commit messages must include a body when the product intent is not obvious.
- Keep commit subject lines at 50 characters or fewer when practical.
- Every commit-message line must be 72 characters or fewer.
- Commit bodies should explain:
  - user-facing behavior,
  - product/demo boundary,
  - why the change exists,
  - important implementation constraints,
  - validation performed.
- Write every complete commit message to a task-specific temporary file.
- Check the temporary file for lines longer than 72 characters before
  committing.
- Create commits with `git commit -F <temporary-file>`. Do not construct
  multi-line commit messages with repeated `-m` arguments.
- Use the same temporary-file and `-F` workflow when amending commits.
