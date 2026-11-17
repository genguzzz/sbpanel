## 1. Audit and implementation

- [x] 1.1 Inspect live API profiles and protocol fields; document the conversion design.
- [x] 1.2 Unify endpoint definitions and preserve missing TLS, transport and Hysteria2 fields.
- [x] 1.3 Implement explicit and auto-detected structured Shadowrocket subscriptions with compatible import links.
- [x] 1.4 Enable IPv6 in built-in Surge defaults and document format limitations.

## 2. Validation and publication

- [x] 2.1 Add field preservation and subscription dispatch regression tests; run full checks and web build.
- [ ] 2.2 Commit, push and publish a patch release with passing CI.
- [ ] 2.3 Deploy sbmaster with binary rollback; update published profiles through draft/preview/publish API.
- [ ] 2.4 Verify live subscriptions and service health, record device verification limits and archive OpenSpec.

## Review hold

Re-authorized by the user on 2026-11-17 after review: commit and deploy.
