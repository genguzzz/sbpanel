## ADDED Requirements

### Requirement: Preserve node address family in supported client formats
The system SHALL export per-node IPv4/IPv6 preference for Surge, Mihomo, sing-box and structured Shadowrocket subscriptions without replacing hostname TLS identities.

#### Scenario: Shadowrocket subscription
- **WHEN** a Shadowrocket client requests nodes with client_ip ipv6
- **THEN** the response is a structured node list with IPv6 preference, original hostname, port, TLS identity and transport details

#### Scenario: Legacy format requested explicitly
- **WHEN** format=base64 or format=links is explicitly requested
- **THEN** the system SHALL return the existing generic URI format

### Requirement: Preserve connection-critical fields
The system SHALL preserve TLS identity, verification mode, ALPN, supported transport headers, WebSocket early data and Hysteria2 obfuscation parameters through endpoint extraction and supported client conversion.

#### Scenario: Hysteria2 salamander
- **WHEN** an inbound requires salamander obfuscation
- **THEN** supported exporters SHALL include its obfuscation type and password

#### Scenario: Unsupported transport combination
- **WHEN** a target client cannot represent an endpoint's required transport
- **THEN** the exporter SHALL exclude it with a reason rather than generate a different connection

### Requirement: IPv6-capable built-in Surge profiles
Built-in Surge profiles SHALL enable IPv6 by default while respecting explicit administrator overrides.

#### Scenario: Default profile
- **WHEN** a profile has no explicit ipv6 option override
- **THEN** generated Surge General contains ipv6 = true
