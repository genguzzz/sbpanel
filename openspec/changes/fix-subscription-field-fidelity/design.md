## Context

The master extracts sing-box inbounds into subEntry, duplicates that model in subgen.Endpoint, then exports independent URI, sing-box, Mihomo and Surge representations. Shadowrocket currently receives generic links, which do not encode the node client_ip hint. Live profiles have no explicit Surge ipv6 override and inherit false.

## Goals / Non-Goals

Goals: preserve supported connection-critical fields, export a dedicated structured Shadowrocket node subscription, keep generic links stable, enable IPv6 in default Surge profiles, and document format limitations. Do not rewrite client rule engines or restart node cores.

## Decisions

- Define the master's subEntry from subgen.Endpoint so fields cannot drift between duplicate structs.
- Use Shadowrocket's structured `proxies:` node list, following the Sub-Store Shadowrocket producer's schema transformations. Preserve per-node ip-version; validate output shape and fields in regression tests. Device-side interpretation remains an explicit verification limit until tested in Shadowrocket.
- Preserve Hysteria2 salamander credentials, custom transport headers and WebSocket early-data settings in supported structured exporters. Do not copy arbitrary server JSON into clients: listen settings, private keys and certificate paths are server-only.
- Add capability filtering and tests for transports/protocol combinations a target cannot express rather than silently emitting a plain connection.
- Keep published profiles administrator-owned. Change source defaults and explicitly update live Surge ipv6 options using draft, preview, publish; retain history for rollback.

## Risks / Trade-offs

- Shadowrocket is closed source: matching the maintained Sub-Store schema is evidence of format compatibility, not a device connection test. Keep explicit format=base64 and format=links available.
- Some URI formats cannot express preferences: document this limitation rather than invent query parameters.
- Published templates may contain custom DNS: preserve their complete contents and change only the intended IPv6 option.

## Migration Plan

Run full tagged Go tests and web build; commit and push; tag a patch release and verify release CI; back up the current master binary and deploy only sbmaster. Verify API, subscription metadata and sanitized field summaries on live subscriptions; publish profile option changes through API after previews pass. Roll back binary with sbmaster.prev and restore prior profile history if verification fails.
