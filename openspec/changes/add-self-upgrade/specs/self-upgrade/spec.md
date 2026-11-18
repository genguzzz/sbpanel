## ADDED Requirements

### Requirement: Release check
The panel SHALL check the latest GitHub release periodically (every 6 hours unless turned off) and on demand, and SHALL report whether the panel and each node run an older release.

#### Scenario: New release
- **WHEN** a release newer than the running panel is published
- **THEN** the panel shows it in Settings → Updates and the top bar and records one `update_available` event for that version

#### Scenario: Development build
- **WHEN** the panel runs a build that is not a release tag
- **THEN** it is not reported as outdated, and the latest release is still offered for installation

### Requirement: Upgrade with automatic rollback
An upgrade SHALL keep the previous binary, install the new one atomically, restart the service and wait for it to become healthy; otherwise it SHALL restore the previous binary and restart it.

#### Scenario: Healthy upgrade
- **WHEN** the new binary reports healthy within the timeout and is still running shortly after
- **THEN** the result is `upgraded` and the previous binary is kept as `<binary>.prev`

#### Scenario: Unhealthy upgrade
- **WHEN** the new binary doesn't report healthy in time, or exits right after starting
- **THEN** the previous binary is reinstalled and restarted, and the result is `rolled_back` with the reason and the service log

### Requirement: Node upgrades through the panel
Nodes SHALL receive the new binary from the master over their authenticated connection and SHALL verify its sha256 before installing it.

#### Scenario: Corrupted transfer
- **WHEN** the received binary's hash or size differs from what the master announced
- **THEN** the node does not install it and reports `failed`
