# Security Policy

## Supported versions

Only the latest release receives security fixes.

## Reporting a vulnerability

Please do not open public issues for security problems.

Report vulnerabilities via
[GitHub private vulnerability reporting](https://github.com/lukeisontheroad/ivanti-neurons-cli/security/advisories/new)
on this repository. You will receive a response within a few days.

## Scope notes

- The CLI stores credentials (API user password, or OIDC client secret plus
  a cached access token) in `~/.config/ivanti-neurons/config.yml` with
  `0600` permissions, or reads them from environment variables. It sends
  them only to the configured tenant (Authorization header, and
  `/oauth2/token` for client credentials).
- Prefer a dedicated API user with the narrowest roles that cover your use
  (e.g. Device Read Only for reporting), or an OIDC client.
- Destructive device commands (`wipe`, `retire`, `relinquish`, deletes)
  require interactive confirmation or an explicit `--yes`, and run the API's
  viability check first.
- `--verbose` logs request URLs (not credentials) to stderr. Query strings
  may contain ids, UIDs or email addresses.
- Dependencies are scanned with govulncheck in CI and updated via Dependabot.
