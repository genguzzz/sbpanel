# Subscription conversion and review notes

The client connection model is `subgen.Endpoint`. The master defines `subEntry`
from that type; do not duplicate the fields. `entryFromInbound` maps sing-box
server settings to that client model; it must not copy private keys, certificate
paths, listen sockets, masquerade, routing or server-only settings into clients.

| Input / meaning | sing-box | Mihomo | Surge export | Shadowrocket structured list |
| --- | --- | --- | --- | --- |
| Node `client_ip` | domain_resolver.strategy | ip-version | ip-version | ip-version |
| TLS hostname | tls.server_name | servername/sni | sni | servername/sni |
| Verification override | tls.insecure | skip-cert-verify | skip-cert-verify | skip-cert-verify |
| ALPN | tls.alpn | alpn | alpn | alpn |
| REALITY public data | tls.reality | reality-opts | excluded (Surge 5 baseline) | reality-opts |
| WS path / headers | transport | ws-opts | ws-path / ws-headers | ws-opts |
| WS early data | transport | ws-opts | omitted (plain upgrade still accepted by server) | ws-opts |
| Hysteria2 salamander | obfs.type/password | obfs + obfs-password | salamander-password | obfs + obfs-password |
| uTLS fingerprint | tls.utls.fingerprint | client-fingerprint | — | client-fingerprint (never `fingerprint`, which is a certificate pin) |
| TUIC ALPN | tls.alpn | alpn | alpn (defaults to h3) | alpn |

`format=shadowrocket` is a **node list**, not a routing-profile generator. Its
`proxies:` plus JSON object items follows Sub-Store's Shadowrocket producer:
https://github.com/sub-store-org/Sub-Store/blob/master/backend/src/core/proxy-utils/producers/shadowrocket.js

The native Shadowrocket client is closed source. Schema/dispatch regression tests
verify generated fields, but actual per-node `ip-version` interpretation and
connection establishment still require an on-device test before release.
Changing auto-detected Shadowrocket responses from Base64 links is a compatibility
change reviewers must assess; explicit `format=base64` and `format=links` remain.

Generic VMess share URIs have no verified per-node address-preference extension.
Do not invent a query field to claim that preference is supported. The VMess URI
path now retains `allowInsecure`; Hysteria2 links retain obfuscation and ALPN.
Other unmodeled features (multi-value HTTP headers, custom HTTP methods, ECH,
additional client tuning fields) still require further audit; this patch does not
claim to convert every sing-box option. Unsupported endpoints filtered by the
Shadowrocket exporter are reported in a Base64 JSON `Subscription-Warnings`
header (and comments when the list is nonempty). The existing full-profile
preview reports its conversion warnings.

Full profiles use `ipv6=true` by default for Surge; an explicit administrator
option still takes precedence. This is independent of a node's IPv6 preference.
The greencloud node has client_ip empty (client default); zouter retains ipv6.

## Pending reviewer decisions

- Confirm Shadowrocket structured per-node `ip-version=ipv6-prefer` works on the
  user's current build; compare to the original VMess log.
- Auto-detection now sends Shadowrocket the structured list (same behaviour as
  Sub-Store's UA detection); `format=base64` remains the opt-out.
- Extend the mandatory-field inventory to every advanced inbound schema and
  surface unsupported inputs before enabling those features in the panel.
- Review Surge baseline version requirements: ALPN needs Surge iOS 5.20 / Mac 6.7; Hysteria2 salamander-password needs Mac 6.4.3+ (verify iOS availability separately).

## Field matrix and how to add a protocol field

`internal/subgen/fieldspec.go` is the source of truth for how each
`Endpoint` field reaches sing-box, Mihomo, Shadowrocket and Surge. None of these
clients publishes a schema (no `.proto` or JSON Schema exists upstream), so the
matrix is kept in Go and enforced by tests:

- `TestFieldSpecCoversEndpoint`: every `Endpoint` field needs a row, and every
  row needs a decision for every client (`Keys`, `Implicit` or `Not` + reason).
- `TestFieldSpecCarried`: renders protocol fixtures (`fieldFixtures`) through
  every exporter and fails when a field marked as carried is missing, including
  individual custom headers.
- `TestFieldFixturesSingboxStrict`: the sing-box output is parsed by sing-box
  itself, which rejects unknown keys, then `box.New` builds it.
- `TestIPv6PreferOverridesGlobalOff`: a node with `client_ip=ipv6` switches on
  the profile's IPv6 DNS/general option (reported as a warning), because a
  global off switch would otherwise turn the preference back into IPv4.

To support a new field: add it to `Endpoint`, extract it in
`entryFromInbound` (master), run the tests (they fail and name the missing
row), add the row with each client's key or reason, emit it in each exporter,
populate it in `fieldFixtures`, and if a client cannot carry a field the node
needs to work, reject the node in `Supported` with the reason instead of
dropping the field silently.
