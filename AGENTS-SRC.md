# Repository guidance

<!-- Installed as AGENTS.md in a FreeBSD source checkout. -->

This repository is the FreeBSD base system source tree, not the ports tree.
Keep changes focused on the requested subsystem and follow nearby code and
build conventions. Read any applicable nested `AGENTS.md` before editing.
Preserve unrelated changes, including untracked files.

## Sources of guidance

- `README.md` describes the source layout.
- `CONTRIBUTING.md` describes contribution, review, and submission rules.
- `UPDATING` records compatibility and upgrade requirements. Read relevant
  entries before changing build, installation, or upgrade behavior.
- `share/man/man7/build.7` documents the build system; `tests/README` and
  `share/man/man7/tests.7` describe testing.
- `sys/README.md` describes kernel organization and configurations.
- Prefer documentation in this checkout when installed manual pages describe
  a different FreeBSD revision.

## Source layout

- `sys/`: kernel, drivers, filesystems, networking, architecture support,
  and modules. Public kernel interfaces generally live in `sys/sys/`.
- `bin/`, `sbin/`, `usr.bin/`, `usr.sbin/`, `libexec/`: base utilities and
  daemons, usually with their manual pages and tests nearby.
- `lib/` and `include/`: libraries and installed headers.
- `stand/`: boot loaders; `etc/`: system configuration templates.
- `share/`: shared resources, manual pages, and BSD make infrastructure.
- `tests/`: test suite infrastructure and cross-subsystem tests. Also look
  for tests alongside the affected component and under `tools/test/`.
- `tools/`: development utilities and additional tests; `release/` and
  `packages/`: release and base package build machinery.
- `contrib/`, `crypto/`, and imported subtrees under `sys/` and `cddl/`:
  check upstream ownership and local integration rules before editing.

## Implementation and style

- Follow `style(9)` (`share/man/man9/style.9`) for native C code and the
  surrounding subsystem's conventions. Use tabs for indentation and keep
  lines near 80 columns where practical. Avoid unrelated formatting churn.
- Follow `style.Makefile(5)`, `style.mdoc(5)`, and `style.lua(9)` for those
  file types. Base shell scripts use FreeBSD `/bin/sh`; avoid Bash extensions
  and GNU-specific utility assumptions.
- Preserve copyright notices, SPDX identifiers, and imported code style.
  Follow `CONTRIBUTING.md` for changes that belong upstream.
- Change generator inputs rather than only generated outputs. Inspect the
  relevant Makefiles and regenerate derived files with the documented tools.
- Preserve userland ABI, kernel interfaces, on-disk formats, and documented
  behavior unless the task requires a change. Inspect compatibility paths,
  architecture assumptions, and callers when changing shared interfaces.
- For kernel work, check locking, reference ownership, object lifetime,
  sleepability, error cleanup, and teardown paths. Account for concurrent
  operations and partial initialization rather than only the success path.
- Update relevant manual pages when interfaces or behavior change. Use
  one sentence per source line in mdoc and follow the repository's rules
  for document dates.

## Building and validation

Use FreeBSD's BSD make infrastructure, not GNU make. Check the host OS,
architecture, available toolchain, and local build configuration first.
On non-FreeBSD hosts, consult `tools/build/make.py` and its prerequisites;
do not assume native build commands work unchanged.

Choose validation that exercises the change. Start with the affected
component and relevant tests, then broaden for shared headers, libraries,
kernel interfaces, or build infrastructure changes. Documentation-only
changes do not require a world or kernel build.

Typical commands on a configured FreeBSD build host, from the source root:

```sh
# Build a userland component using its existing Makefile.
make -C usr.bin/example obj
make -C usr.bin/example all

# Broader builds when warranted; choose a suitable job count.
make -j4 buildworld
make -j4 buildkernel KERNCONF=GENERIC

# Run the relevant installed test subtree and inspect its results.
kyua test -k /usr/tests/usr.bin/example/Kyuafile
kyua report

# Check the working diff for whitespace errors.
git diff --check
```

Replace `example` with the affected component. For cross builds, specify
the appropriate `TARGET` and `TARGET_ARCH`; select a suitable `KERNCONF`
for kernel work. Keep build products in object directories and temporary
artifacts outside the source tree. Do not run `universe` or `tinderbox`
as a routine check for a small change.

- Inspect a component's test Makefile before using `make check`. Ensure
  tests exercise newly built binaries or the changed kernel; installed
  `/usr/tests` may otherwise exercise an older system.
- Add regression coverage for behavior changes where practical, using
  existing ATF/Kyua conventions and test registration. Include failure and
  cleanup paths when relevant.
- Run `tools/build/checkstyle9.pl` on relevant source diffs as described
  in `CONTRIBUTING.md`; review findings against the style guide.
- For manual page changes, run `mandoc -Tlint` and, when available, `igor`.
  For shell changes, run an appropriate syntax check such as `sh -n`.
- Use a disposable VM or other suitable isolated test environment for
  kernel panic reproductions, destructive filesystem tests, and tests that
  alter system networking or storage.
- Treat installation, module loading, rebooting, and changes to the running
  host as separate from building. Perform them only within the task's
  authorized scope; use a staging `DESTDIR` when appropriate.
- Report what was tested, the relevant configuration, and results. State
  unavailable checks and remaining limitations explicitly; a successful
  compile alone does not establish runtime correctness.
