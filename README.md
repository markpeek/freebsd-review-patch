# FreeBSD patch review

Repository guidance and a skill for Codex and Claude Code for reviewing
Differential reviews from reviews.freebsd.org and GitHub pull requests in
`freebsd/freebsd-src` against a local FreeBSD source checkout.

- [AGENTS.md](AGENTS.md) provides guidance for working in the FreeBSD base
  system source tree.
- [freebsd-review-patch](skills/freebsd-review-patch/SKILL.md) describes how
  to fetch and review patches from either source and report actionable
  findings.

## Installation

Run these commands from this repository's root. Replace
`/path/to/freebsd-src` with the root of your FreeBSD source checkout.

Copy `AGENTS.md` into the FreeBSD source tree:

```sh
cp -i AGENTS.md /path/to/freebsd-src/AGENTS.md
```

If the checkout already has an `AGENTS.md`, merge the guidance you need
into that file before replacing it.

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
