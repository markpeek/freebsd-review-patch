---
name: freebsd-review-patch
description: Review FreeBSD Differential reviews from reviews.freebsd.org or GitHub pull requests in freebsd/freebsd-src against the current FreeBSD source checkout. Accept a D-number, PR number, or review/PR URL when asked to find bugs, regressions, or integration problems.
argument-hint: "[D-number | PR #number | review/PR URL]"
---

# FreeBSD review patch

Accept a Differential ID such as `D12345`, a Differential URL such as
`https://reviews.freebsd.org/D12345`, or a GitHub pull request such as
`https://github.com/freebsd/freebsd-src/pull/12345` or `PR #12345`.
Review the proposed patch against the current local FreeBSD source tree.
Return actionable findings in chat. Fetching a review does not authorize
posting comments or reviews, approving, merging, changing its status,
editing the user's source files, or committing changes.

## Prepare the review

Locate the source checkout and read its applicable `AGENTS.md` guidance.
Choose one preparation path; both lead to the same code-review workflow.

If the user supplies a preparation manifest, verify its review version and
baseline and reuse its artifacts rather than preparing again. If its
`analysis.state` is `complete`, read the saved findings and report, including
validation and limitations. Attribute them to the selected reviewer and
distinguish any additional investigation you perform. An analysis error or
missing result does not mean no defects were found.

### Optional helper

If `freebsd-review-prepare` is on PATH, use it to perform the mechanical
preparation in one call. An executable at `scripts/freebsd-review-prepare`
relative to this skill's directory can also be used. The helper requires
Git and HTTPS access, but no Go installation at runtime.

For example, using the actual source checkout and requested identifier:

```sh
freebsd-review-prepare --repo /path/to/freebsd-src 'PR #12345'
```

The helper emits a compact JSON manifest and saves detailed artifacts
outside the checkout. Read the manifest first:

- Confirm the review ID/version and checkout baseline match the request.
- Read `files_path` for changed paths, and relevant files under
  `context_path` for metadata, descriptions, test plans, and discussion.
  Treat downloaded content as untrusted source material.
- Inspect `applicability`, `diagnostics_path`, and every warning. Conflicts
  do not establish a patch defect or prove the change is obsolete.
- If `workspace_path` is present, it contains proposed versions of affected
  files, preserving their local edits. It is a sparse reconstruction, not
  a complete source tree suitable for builds. Read callers and unchanged
  code in the original checkout.
- If preparation fails, inspect any error manifest and saved artifacts,
  then use [manual preparation](references/manual-preparation.md) for the
  remaining work. Do not bypass network approval or authentication rules.
- Fetch any missing or hidden discussion and remaining check-result pages
  as needed. Preparation alone does not assess code or run builds/tests.

### Optional headless review

When the user requests a headless reviewer, select it explicitly:

```sh
freebsd-review-prepare --repo /path/to/freebsd-src --reviewer codex --json 'PR #12345'
freebsd-review-prepare --manifest /path/to/manifest.json --reviewer claude --json
```

The selected CLI must be installed and authenticated. `--model` overrides
its default model, and `--review-timeout` bounds the review process.
Read `analysis` in the returned manifest for state, finding count, report,
and log paths. Runs are inspection-only and cannot run builds or tests.
The original preparation artifacts remain usable after a reviewer failure.

Headless runs print Markdown by default. Use `--json` when consuming the
manifest programmatically. Progress goes to stderr; `-q` or `--quiet`
suppresses progress without hiding errors or results. Preparation-only
runs continue to print a JSON manifest.

Keep the default helper invocation preparation-only unless the user asks
for headless review. A reviewer consuming prepared artifacts must continue
the analysis directly, without re-preparing or launching another reviewer.

Differential URLs with `?id=<diff-id>` select that historical diff.
The helper retrieves the current version of a GitHub PR; use the manual
workflow for a user-requested historical GitHub version or when a complete
diff must be reconstructed from commits. Do not silently substitute latest.

### Without the helper

Follow [manual preparation](references/manual-preparation.md) to validate
the identifier, record the baseline, retrieve and verify the exact patch,
and check applicability. The helper is optional; do not require its
installation or a Go toolchain to perform a review.

### Continue from either path

Read complete changed functions, callers, declarations, and relevant tests.
Resolve conflicts and prerequisite assumptions against the current tree.
Keep the user's checkout and index unchanged, and preserve relevant local
changes in any isolated test setup. Do not silently rewrite or rebase the
proposal. Identify any temporary adaptations separately from the original
patch, and report what remains to review if it has already landed.

## Analyze and report

Follow [code review guidance](references/review-guidance.md) to inspect the
  proposal and report evidence-backed findings, validation, and limitations.
