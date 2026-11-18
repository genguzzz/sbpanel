## 1. Updater

- [x] 1.1 Release check (GitHub API, redirect fallback, mirror), semantic compare, verified download and extraction
- [x] 1.2 Plan / helper: backup to `.prev`, atomic install, restart, marker or stable health, rollback, result with log tail
- [x] 1.3 systemd-run transient unit and OpenRC detached launch; service detection from cgroup
- [x] 1.4 Unit tests: success, rollback (silent / crash / down), rollback failure, version mismatch, manual rollback and roll-forward, lock, fetch and checksum mismatch

## 2. Master and node

- [x] 2.1 Proto: `FetchUpgrade`, `Upgrade`, `UpgradeStatus`, `Hello.features`
- [x] 2.2 Master: check loop, release cache, `/api/update*`, self-upgrade, node fan-out, events, health marker
- [x] 2.3 Node: fetch from master, sha256 check, hand-off, health marker, status on reconnect
- [x] 2.4 `sbmaster upgrade` / `sbnode upgrade` CLI
- [x] 2.5 E2E: master → node upgrade over gRPC with a fake release server

## 3. UI and docs

- [x] 3.1 Settings → Updates, top-bar pill, notification kinds, event labels (en / zh)
- [x] 3.2 README

## 4. Verification

- [x] 4.1 dmit (CentOS 7, systemd 219): forced failure rolls back, upgrade to v0.3.0 (stable), rollback to the dev build (marker), sandbox escape via systemd-run
- [ ] 4.2 Node upgrade on a real node from the panel (restarts sbnode: brief disconnect)
- [x] 4.3 Release v0.4.0 (first version that writes the health marker)
