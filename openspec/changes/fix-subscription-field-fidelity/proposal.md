## Why

Node address-family preferences disappear from Shadowrocket subscriptions, while complete Surge profiles default to disabling IPv6. Independently maintained subscription adapters can also silently omit sing-box transport and TLS fields, producing unusable nodes without explaining the loss.

## What Changes

- Audit inbound-to-endpoint extraction and client exporters for currently configured transports, TLS, credentials and address-family preferences.
- Add a Shadowrocket-specific subscription format that preserves supported structured node fields and keeps generic URI subscriptions compatible.
- Enable IPv6 in built-in Surge profiles and update the deployed published profiles through the documented API.
- Add regression tests for field preservation and explicit unsupported capabilities; release and deploy the verified master.

## Capabilities

### New Capabilities

- `subscription-field-fidelity`: Preserve supported node semantics across client subscriptions and expose unsupported conversions.

### Modified Capabilities

None.

## Impact

Master subscription dispatch, subgen adapters, API/client import links, built-in profile defaults, tests and operator documentation. No node core or credential rotation is required. Published routing profiles and the sbmaster binary will be backed up before changes.
