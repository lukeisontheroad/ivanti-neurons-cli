---
name: ivanti-neurons
description: >
  Work with Ivanti Neurons for MDM (formerly MobileIron Cloud) via the
  ivanti-neurons CLI: search and inspect devices, run device actions (check
  in, lock, lost mode, retire, wipe), manage users, user and device groups,
  apps, configurations, certificates, bulk enrollment and tenant settings.
  Use when the user asks about their managed devices, MDM fleet,
  MobileIron/Ivanti tenant, enrollment, or wants to act on devices or users
  from the terminal.
---

# Operating the ivanti-neurons CLI

## Setup check

Run `ivanti-neurons auth status`. If it fails, the user must authenticate
first: `ivanti-neurons auth login` (interactive), or set `IVANTI_URL` plus
`IVANTI_USERNAME`/`IVANTI_PASSWORD` (or `IVANTI_CLIENT_ID`/
`IVANTI_CLIENT_SECRET`). Do not create API users or OIDC clients yourself.

## Reference

Run `ivanti-neurons docs` once — it prints the complete command reference
(all commands, flags, filter syntax) in a single page designed for LLM
consumption. Prefer it over multiple `--help` calls.

## Rules

- Use `-o json` whenever you consume output programmatically; tables
  truncate long values. List commands print one JSON array; other commands
  print the API's `result` object.
- Calls are scoped to a device space; the tenant default is used unless
  `--space <id|name>` is given (`ivanti-neurons space list`).
- Find devices with `device list` typed filters (`--platform`, `--status`,
  `--user`, `--email`, `--attr`, `--checked-in-within`) before hand-writing
  `--fq` expressions. `--limit 0` fetches everything (paged automatically).
- Read operations are safe to run freely. Anything that changes the tenant
  (create/update/delete, group membership, configuration assignment, app
  install, attribute changes) affects real users and devices — confirm with
  the user first unless they asked for that exact change.
- Device actions reach real phones. `wipe` (factory reset), `retire`,
  `relinquish` and `delete` are irreversible: always confirm the exact
  device ids with the user, run the action with `--check` first, and only
  then pass `--yes`. Never wipe or retire on your own initiative.
- `message`, `lock`, `lost mode` and PIN/invite commands notify end users —
  confirm the text and recipients first.
- `ivanti-neurons api <method> <path>` reaches any endpoint when no
  dedicated command exists.
