# Changelog

Each release lists what changed for people running sbpanel. The release notes on GitHub are built
from the release's section here (`tools/dist`); a tag without a section fails to release.

## Unreleased

## v0.4.6 - 2026-11-19

### New
- Usage reset can follow a fixed day of the month (1–31; short months use their last day) or a
  node's monthly reset day, so a user's quota renews with the machine's billing cycle. The node's
  day is set under Nodes → Edit → Monthly reset day. The options read: Never, Daily, Mondays,
  Monthly on sign-up day, Monthly on a set day, Same as a node.
- Settings → Updates has a "View on GitHub" button for the latest release, next to its notes.

### Fixes
- Node upgrades no longer stay on "Restarting…" after the node is back: the new sbnode now reports
  the result once its upgrade helper has confirmed it, and the panel settles the status of nodes
  still on older versions.
- The header's update pill says when only nodes are behind ("2 nodes can upgrade to v0.4.5")
  instead of reading as if the panel were out of date.

### Changes
- Docker: one image, `ghcr.io/genguzzz/sbpanel:latest`. It downloads the newest release on every
  start, so releases no longer publish version-tagged images. Upgrade by restarting the container;
  `SBP_VERSION=vX.Y.Z` pins a release. The Add node Docker command and Settings → Updates say so.
- Traffic: "Clear all" in the filter bar is a × icon, and the bar fades at the edge that scrolls
  off on phones.

## v0.4.5 - 2026-11-18

### New
- Passkey sign-in: register passkeys under Settings → Account & security and sign in with one instead of a
  password and TOTP code. Failed attempts are rate-limited per client.
- Traffic: filter dimensions as a row of pills that open a picker, so picking a value no longer
  makes the page jump. On wide screens a toggle switches back to the side panel; phones get the
  row, scrollable sideways.
- Traffic: click an entry in the trend chart's legend to filter by it.

### Fixes
- Inbounds: a certificate imported by file path under Domains & certificates can be selected when
  creating an inbound.
- Inbounds: editing an inbound no longer complains that its own tag is taken; tags are unique per
  node.
- Inbounds: the DNS check lists every resolved address (IPv4 first) and says which node it points
  to, and names an address family the domain is missing.
- Phones: pages no longer shrink after some actions (the header was wider than the screen), and
  focusing an input no longer zooms in.

### Changes
- Clearer wording across the panel, e.g. "Technical details" → "View detailed logs".

## v0.4.4 - 2026-11-18

### Changes
- Dependency updates: Go modules, web packages, GitHub Actions, and the Docker base image
  (Alpine 3.22 → 3.24).
