## Why

Upgrading meant SSHing into every machine and running `install.sh upgrade`, which swaps the binary and restarts without checking the new version works. A bad release takes a node (or the panel) down until someone notices. Operators coming from 3x-ui expect the panel to tell them about new versions and upgrade from the browser.

## What Changes

- The master checks the latest GitHub release every 6 hours (switchable, optional download mirror), shows it in the top bar and Settings → Updates, and raises an `update_available` notification once per version.
- Settings → Updates upgrades or rolls back the panel and any nodes. Nodes receive the sbnode binary from the master over their existing mTLS gRPC connection (`FetchUpgrade`), verified against the release's `checksums.txt` by the master and by sha256 on the node.
- A shared `internal/updater` swaps binaries outside the service sandbox, keeps `<bin>.prev`, restarts the service, waits for a health signal and rolls back automatically on timeout or a crash right after start. Results become events / notifications (`panel_upgraded`, `panel_upgrade_failed`, `node_upgraded`, `node_upgrade_failed`).
- `sbmaster upgrade` / `sbnode upgrade [--version] [--rollback] [--mirror]` use the same machinery from a shell.

## Capabilities

### New Capabilities

- `self-upgrade`: release checks, panel and node upgrades with verification, health check and automatic rollback.

### Modified Capabilities

None.

## Impact

New package `internal/updater`; master `update.go`, routes `/api/update*` (read / settings:write / admin scopes), node `upgrade.go`, proto additions (`FetchUpgrade`, `Upgrade`, `UpgradeStatus`, `Hello.features`), Settings → Updates UI and top-bar pill. Nodes and panels older than this release can't be upgraded from the panel until upgraded once with install.sh. Disk: about 3× the binary size during an upgrade, plus two cached releases on the master.
