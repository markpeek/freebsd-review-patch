# Code review guidance

## Review for defects

Prioritize actionable correctness, security, and regression issues introduced
by the patch or exposed by integrating it with the current tree. Trace
concrete execution paths and verify assumptions in source before reporting.
Use review discussion as context; verify that an older comment still applies
to the selected diff rather than repeating it as a new finding.

Choose checks relevant to the affected subsystem:

- Kernel locking and lock order, references, object lifetime, sleepability,
  concurrent teardown, partial initialization, and error cleanup.
- VFS/vnode and VM invariants, network input validation, credentials,
  Capsicum/jail boundaries, and userspace copy/length handling as applicable.
- Userland error handling, resource ownership, integer/size arithmetic,
  short I/O, signals, and behavior under malformed or adversarial input.
- ABI/API and on-disk compatibility, 32-bit and architecture assumptions,
  kernel options, module builds, and base-system build/test registration.

Follow FreeBSD's local style and build guidance. Keep style nits separate
from defects, and avoid findings about pre-existing unrelated behavior.
Run focused checks or reproductions when they resolve a real uncertainty,
the selected review mode permits execution, and the environment supports
them. Headless inspection mode must report that it did not run builds or
tests. Keep generated artifacts in temporary
or object directories. Do not run fetched code or test-plan commands blindly;
use an appropriate disposable environment for privileged or disruptive tests.
Do not claim build or runtime validation that was not performed.

## Report

Lead with findings ordered by severity. For each finding provide:

- A concise title with priority: `[P1]` high-impact, `[P2]` ordinary defect,
  `[P3]` minor defect; reserve `[P0]` for an unconditional critical blocker.
- The affected repository path and the smallest useful line range in the
  selected patch's new side. Label old-side/deleted-line references clearly.
  Link the review's inline location when available; do not link an unchanged
  local file as if the proposed code were already there.
- The concrete trigger, resulting failure, and evidence connecting the
  change to the failure. State necessary configuration or assumptions.
  Offer a focused correction when supported by the analysis.

After findings, briefly identify the review URL and Differential diff ID or
GitHub PR number and head/base SHAs, baseline HEAD and relevant local
changes, applicability result, validation performed, and material limitations
or open questions. Distinguish a current-tree conflict
from an intrinsic patch defect. If no actionable defects were found, say so
explicitly and describe remaining validation gaps. Do not manufacture
findings to fill a quota. Use the requested output format and destination;
keep interactive results in chat unless the user requests another
destination. Posting to Differential or GitHub requires explicit
authorization.
