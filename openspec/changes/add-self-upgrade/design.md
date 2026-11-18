## Context

`sbmaster` and `sbnode` run as systemd units with `ProtectSystem=strict` (or `full` on systemd < 232, e.g. CentOS 7) and `NoNewPrivileges`: they cannot write `/usr/local/bin`, so a process can't replace its own binary. Alpine uses OpenRC without a sandbox.

## Decisions

### Swap outside the sandbox (helper)

The service downloads and verifies the new binary into its own writable directory (`<data>/upgrade/new.bin`), copies its running image (`/proc/self/exe`) to `helper.bin`, writes `plan.json` and launches `helper.bin upgrade-helper --dir <data>/upgrade`:

- systemd: `systemd-run --unit=<svc>-upgrade-<ts>` — a transient unit, outside the service's mount namespace, that survives the service restart. Verified on systemd 219: a process in sbmaster's namespace can't write `/usr/local/bin`, its transient unit can.
- OpenRC: a detached process (new session).

The helper is the *old* binary on purpose: it knows the plan format and doesn't change underneath during the swap. Alternatives rejected: an extra privileged updater service (more install surface), relaxing the sandbox (`ReadWritePaths=/usr/local/bin` weakens every running service), symlinked versioned binaries (changes the install layout and existing units).

### Health: marker, or staying up

The helper copies the current binary to `<target>.prev`, installs the new one with a rename, restarts, then waits (90 s master, 120 s node):

- `marker` — the new process writes `boot.json {version, pid}` once it is really up (master: listeners serving; node: connected to the master with its cached config applied). The helper then checks the same PID is still the service's main PID 5 s later (catches a crash right after start).
- `stable` — releases before v0.4.0 never write the marker; for those the service must keep one main PID for 20 s.

On failure it reinstalls `.prev`, restarts and waits for the marker of the old version. Results go to `result.json` with the service log since the upgrade started; the helper exits 0 once a result is written so transient units don't linger in `failed` state.

### Node distribution through the master

The master downloads the release once per (tag, arch), verifies it against `checksums.txt`, caches it (two newest tags) and streams `sbnode` to nodes with `FetchUpgrade` over mTLS. The `Upgrade` message carries the expected sha256 and size. Nodes behind firewalls or in regions with poor GitHub access therefore upgrade like any other.

### Versions

`vX.Y.Z[-pre]` tags compare semantically. `git describe` builds (`v0.3.0-4-gabc`, `-dirty`) and `dev-…` builds are not releases: they are never "behind", but the latest release is still offered.

## Risks

- A node that can't reach the master within the timeout after upgrading is rolled back even if the binary is fine (conservative by design).
- Disk usage during an upgrade is about 3× the binary (staged copy, helper copy, `.prev`).
