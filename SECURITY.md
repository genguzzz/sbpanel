# Security policy

## Reporting a vulnerability

Please don't open a public issue for security problems. Report them privately through
[GitHub's private vulnerability reporting](https://github.com/genguzzz/sbpanel/security/advisories/new)
(Security → Report a vulnerability). Include the version (`sbmaster --version` / `sbnode version`),
what an attacker needs (network position, an account, an API key scope), and steps to reproduce.

You'll get an answer within a few days. Fixes ship as a patch release; the advisory is published
once the release is out, crediting you unless you prefer otherwise.

## Supported versions

Only the latest release gets security fixes. Upgrade from Settings → Updates, `install.sh upgrade`,
or by pulling the new image tag.

## Verifying releases

Release archives are listed with their SHA-256 in `checksums.txt` (the installer, the panel's
updater and the Docker entrypoint all check it). Archives and images also carry GitHub build
provenance attestations:

```bash
gh attestation verify sbpanel_v0.4.2_linux_amd64.tar.gz --repo genguzzz/sbpanel
gh attestation verify oci://ghcr.io/genguzzz/sbpanel:v0.4.2 --repo genguzzz/sbpanel
```

## Scope notes

- Nodes trust the master they enrolled with (mTLS with a pinned CA) and run what it sends, as root.
  Protect the panel accordingly: put the web UI behind HTTPS, use strong admin passwords and 2FA,
  and give API keys the narrowest scope.
- The subscription endpoints are public by design; their tokens are the secret.
