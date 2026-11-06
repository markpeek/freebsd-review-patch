# FreeBSD patch review

Repository guidance and a Codex skill for reviewing Differential reviews
from reviews.freebsd.org and GitHub pull requests in `freebsd/freebsd-src`
against a local FreeBSD source checkout.

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

Copy the complete skill directory, including its `agents` subdirectory,
into `~/.codex/skills`:

```sh
mkdir -p ~/.codex/skills
cp -R skills/freebsd-review-patch ~/.codex/skills/
```

The installed skill should be at
`~/.codex/skills/freebsd-review-patch/SKILL.md`.

## Usage

Work from your FreeBSD source checkout and ask Codex to review a
Differential review:

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

The skill reports findings in chat and preserves the checkout. Posting
comments or reviews, approving, merging, or changing a review's status
requires a separate request.
