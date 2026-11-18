# sbpanel

**English** | [简体中文](README.zh-CN.md)

A proxy management panel built on **sing-box**. The master and the nodes are deployed separately, and every connection is counted exactly, so traffic analytics are built in (no separate Neko Master needed).

![Overview](docs/img/overview.png)

<table>
  <tr>
    <td><img src="docs/img/users.png" alt="User details drawer"></td>
    <td><img src="docs/img/traffic-dark.png" alt="Traffic analytics, dark mode"></td>
  </tr>
  <tr>
    <td align="center">Users: quota, expiry, devices, per-user traffic</td>
    <td align="center">Traffic analytics down to user × domain × source IP</td>
  </tr>
  <tr>
    <td><img src="docs/img/routing.png" alt="Routing rule editor"></td>
    <td><img src="docs/img/subpage.png" alt="Themed subscription page"></td>
  </tr>
  <tr>
    <td align="center">One routing model → Clash / sing-box / Surge configs</td>
    <td align="center">Themeable subscription page for end users</td>
  </tr>
  <tr>
    <td><img src="docs/img/certs.png" alt="Domains and certificates"></td>
    <td><img src="docs/img/settings-api.png" alt="API keys and MCP"></td>
  </tr>
  <tr>
    <td align="center">Domains, certificates and connectivity tests</td>
    <td align="center">Scoped API keys and a built-in MCP server</td>
  </tr>
</table>

> The UI is currently in Chinese. Design notes are in [docs/DESIGN.md](docs/DESIGN.md).

## Features

| Area | What you get |
|---|---|
| **Nodes** | One-line enrollment (one-time token + CSR, certificate signed by the panel's own CA, mTLS over TLS 1.3). Nodes dial the master, so they can sit behind NAT. Automatic certificate renewal, revocation on delete. CPU, memory, live throughput and monthly quota per node (80% / 100% alerts). |
| **Core** | sing-box 1.14 embedded as a Go library. Changing only users is a **hot update** (vless / vmess / trojan / shadowsocks) that keeps existing connections alive; other inbound changes rebuild only that inbound; skeleton changes are validated first and swapped blue-green, so a bad config never takes a node down. |
| **Inbounds** | VLESS (REALITY / Vision, WS, gRPC, HTTPUpgrade), VMess, Trojan, Shadowsocks 2022, Hysteria2, TUIC v5, AnyTLS. One-click REALITY key pairs. PROXY protocol listeners for nginx SNI routing on a shared port 443. |
| **Users** | Traffic quota, expiry, reset cycles (daily / weekly / monthly / rolling), **speed limits** shared across all of a user's connections, **device limits** by source IP (enforced locally on each node and corrected across nodes by the master), batch actions with undo, key rotation, kick. |
| **Traffic limits** | Presets of an extra cap on a set of inbounds, e.g. “home lines 50 GB a month” over every relay reaching them, attached to individual users (user form, bulk, or from the preset) with an optional custom size; users without one are not capped. A user over it loses only those inbounds (connections there are closed, subscriptions hide them) until it resets with their own cycle or on a fixed day of the month. Usage per inbound in the user drawer and on the subscription page. |
| **Groups** | Many-to-many mapping between users and inbounds; the default group picks up new users and inbounds automatically. |
| **Subscriptions** | `/s/<token>` (3x-ui `/sub/<subId>` links keep working). Returns sing-box JSON, Clash/mihomo YAML, Surge or base64 URIs based on the User-Agent. A browser gets a themeable self-service page (usage, QR codes, one-click import). Per-host overrides for address, port, SNI, Host and path (CDN, relays, multiple domains). Access log. |
| **Routing profiles** | Define rules and policy groups once, generate per-client configs (Clash/mihomo, sing-box, Surge) with client-specific templates. Visual editor plus raw JSON, preview and diff before publishing. Presets: standard split, global, bypass mainland, minimal. |
| **Traffic analytics** | Exact bytes by user × node × inbound × outbound × domain × source IP × port × rule. Any range in the last 30 days, down to the minute; 1 min / 5 min / 15 min / 1 h / 1 day buckets. Live connection list with disconnect. |
| **Domains & certificates** | Guided setup: connect a Cloudflare API token to create DNS records and issue certificates via DNS-01 (wildcards supported, no port 80 needed), or import PEM files, or use certbot certificates already on a node. Renewal swaps certificate files without rebuilding inbounds. |
| **Connectivity tests** | The master tests each inbound stage by stage (DNS → TCP/QUIC → TLS → proxy) with a hidden probe user, and tells you which side failed: a closed port, an SNI mismatch, the inbound rejecting the client, or an unreachable upstream behind the inbound. |
| **API & agents** | Scoped API keys (read / users:write / nodes:write / settings:write / admin) with expiry, IP allowlists and rate limits. OpenAPI 3.1 spec with an in-panel docs page. Built-in MCP server (`POST /mcp`) so Claude Code and other agents can query and manage users. |
| **Upgrades** | The panel checks GitHub releases and shows new versions in the top bar and as a notification. One click upgrades the panel, or any nodes (they receive the verified binary from the panel, so they need no GitHub access). The old binary is kept as `.prev`; if the new one doesn't report healthy (or crashes right after), it is put back and restarted automatically. Manual rollback from the same page, or `sbmaster upgrade` / `sbnode upgrade [--version vX.Y.Z] [--rollback]` from a shell. |
| **Operations** | TOTP two-factor login, audit log, Telegram / webhook notifications, one-click backup, `sbmaster reset-admin`, migration from 3x-ui (API JSON or `x-ui.db`). |
| **UI** | React 19, react-aria-components, Tailwind v4, ECharts. Four palettes (Graphite, Rosé Pine, Catppuccin, Nord) in light and dark, custom accent colour, ⌘K command palette, `g` + letter navigation, mobile layout. |

### Traffic is never lost or double-counted
Each node counts every connection and summarises it every 10 seconds. Reports are **written to a local bbolt spool before they are sent**; the master deduplicates by `(node, seq)` and adds them up in the same transaction, and only then does the node delete them. While the master is down, reports queue on the node (and are merged into coarser windows instead of being dropped if the queue grows too large). Resending never counts twice.

## Install

Supports Debian / Ubuntu, RHEL / CentOS 7+ / Rocky / Alma / Fedora, Alpine (OpenRC), Arch and openSUSE on amd64 and arm64. Release archives are verified with SHA256.

**Panel (master)**
```bash
curl -fsSL https://raw.githubusercontent.com/genguzzz/sbpanel/main/install.sh | sh -s -- master \
  --web 127.0.0.1:8080 --grpc :9443 --public-grpc panel.example.com:9443
```
The installer prints the initial admin password. The web UI only listens on localhost by default; put nginx or Caddy with HTTPS in front of it. Nodes connect to the gRPC port (9443 by default), so open it in your firewall.

**Node**: copy the command from the panel (Nodes → Add node). It looks like this:
```bash
curl -fsSL https://raw.githubusercontent.com/genguzzz/sbpanel/main/install.sh | sh -s -- node \
  --master panel.example.com:9443 --token <one-time-token> --ca-sha256 <ca-fingerprint>
```

**Upgrade / uninstall / status**
```bash
curl -fsSL https://raw.githubusercontent.com/genguzzz/sbpanel/main/install.sh | sh -s -- upgrade
curl -fsSL https://raw.githubusercontent.com/genguzzz/sbpanel/main/install.sh | sh -s -- uninstall node --purge
curl -fsSL https://raw.githubusercontent.com/genguzzz/sbpanel/main/install.sh | sh -s -- status
```
If GitHub is slow to reach, add `--mirror https://<your-github-proxy>/`. Use `--version vX.Y.Z` to install a specific release.

From v0.4.0 on you can also upgrade from the panel (Settings → Updates) or on the machine itself, with health checks and automatic rollback:
```bash
sbmaster upgrade                 # latest release; --version vX.Y.Z, --rollback, --mirror URL
sbnode upgrade
```

**Admin account recovery**
```bash
sbmaster reset-admin --user admin --password -            # reads the new password from stdin
sbmaster reset-admin --user alice --rename admin --password -   # also renames the account
```

**Migrating from 3x-ui**
- Users only: `sbmaster import-3xui --users-only --json export.json`. Usernames, UUIDs, passwords, quotas, expiry and subIds are kept, so existing `/sub/<subId>` links keep working.
- Users and inbounds: `--node <ID> --db /etc/x-ui/x-ui.db`.
- Add `--dry-run` to either to preview.

## API and AI agents

Create a key under Settings → API & agents (it is shown once) and pick its scopes:

```bash
curl -H "Authorization: Bearer sbp_…" https://panel.example.com/api/overview
claude mcp add --transport http sbpanel https://panel.example.com/mcp --header "Authorization: Bearer sbp_…"
```

The spec is served at `GET /api/openapi.json` (browsable on the in-panel API docs page). Scopes, rate limits, MCP tools and other client setups are documented in [docs/API.md](docs/API.md). Certificates are covered in [docs/CERTS.md](docs/CERTS.md), subscription page themes in [docs/SUB-THEMES.md](docs/SUB-THEMES.md).

## Build and test

```bash
make web          # frontend (pnpm)
make master node  # binaries for this machine
make linux        # static linux amd64/arm64 binaries (CGO=0)
make test         # unit + end-to-end tests
make test-race    # with the race detector
```

Requires Go 1.25+ (go.mod fetches the toolchain) and pnpm.

The end-to-end tests run real sing-box clients with configs taken from the users' own subscriptions. Among other things they cover:
- every inbound variant, with per-inbound billing checked;
- 100 MiB over REALITY with master and node byte counts matching exactly;
- master outages with no lost or duplicated traffic;
- hot updates that don't drop other users' connections;
- speed limits, device limits, expiry and renewal;
- template publishing with automatic fallback on a bad config;
- certificate revocation;
- the connectivity probe, including an unreachable upstream;
- API keys (scopes, expiry, IP allowlist, rate limit) and the MCP endpoint;
- 3x-ui migration with old clients and subscription links still working.

## Local demo

```bash
make web master node && go build -tags with_quic,with_utls -ldflags=-checklinkname=0 -o bin/devclient ./tools/devclient
scripts/dev-demo.sh start     # master + node + users + a VLESS REALITY inbound + two clients
scripts/dev-demo.sh traffic   # browse a set of sites through both users
scripts/dev-demo.sh stop
```

The screenshots above were taken from this demo.

## License

sbpanel links sing-box (GPL-3.0-or-later) as a library and includes modified sing-box sources in `internal/node/hot/`, so **binaries are distributed under GPL-3.0-or-later**.
