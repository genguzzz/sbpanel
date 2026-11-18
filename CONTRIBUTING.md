# Contributing

Pull requests are welcome. For anything larger than a fix, open an issue first so we can agree on
the approach.

## Build and test

Go (version from `go.mod`, the `toolchain` line is fetched automatically), Node 22+ and pnpm 10:

```bash
cd web && pnpm install --frozen-lockfile && pnpm build && cd ..
go vet -tags with_quic,with_utls,with_acme ./...
go test -tags with_quic,with_utls,with_acme -count=1 ./...
```

`scripts/dev-demo.sh start` runs a local panel and node with demo data (see the README).

If you change the protocol presets in `web/src/lib/presets.ts`, regenerate the fixture the
end-to-end test uses: `node --experimental-strip-types web/scripts/preset-fixture.ts`.
If you change `proto/`, regenerate `internal/pb` (`make proto`).

## Pull requests

- One topic per pull request, with a test for behaviour changes and bug fixes.
- The `ci` check must pass. The `security` checks (CodeQL, govulncheck, dependency review) run too;
  new findings need a fix or an explanation.
- A maintainer reviews and merges. Workflows from first-time contributors run after approval.
- Commit messages: `Area: what changed` in the first line, the why in the body.

## Security

Report vulnerabilities privately, see [SECURITY.md](SECURITY.md).
