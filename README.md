# ivanti-neurons-cli

[![CI](https://github.com/lukeisontheroad/ivanti-neurons-cli/actions/workflows/test.yml/badge.svg)](https://github.com/lukeisontheroad/ivanti-neurons-cli/actions/workflows/test.yml)
[![Release](https://img.shields.io/github/v/release/lukeisontheroad/ivanti-neurons-cli)](https://github.com/lukeisontheroad/ivanti-neurons-cli/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE.md)

`ivanti-neurons` is a command-line client for
[Ivanti Neurons for MDM](https://www.ivanti.com/products/ivanti-neurons-for-mdm)
(formerly MobileIron Cloud): a single static binary, no runtime
dependencies, configured via a config file or environment variables. It
covers every call in Ivanti's published API guide.

```
$ ivanti-neurons device list --platform ios --checked-in-within 7d
ID     PRETTYMODEL  PLATFORMTYPE  PLATFORMVERSION  UID               REGISTRATIONSTATE  LASTCHECKIN
22184  iPhone 15    IOS           18.6             jane@example.com  ACTIVE             2026-07-01 08:00
22191  iPad Air     IOS           18.6             max@example.com   ACTIVE             2026-06-30 08:00
```

## Install

From source (Go ≥1.26):

```sh
go install github.com/lukeisontheroad/ivanti-neurons-cli/cmd/ivanti-neurons@latest
```

Homebrew (once the tap is published):

```sh
brew install lukeisontheroad/tap/ivanti-neurons
```

Or grab a binary from the [releases page](https://github.com/lukeisontheroad/ivanti-neurons-cli/releases).

## Authentication

Create an API user in the admin portal (**Admin → Users → Add → Single
User**) and give it the **API** role plus the roles for what you want to do:
Device Management, User Management, App & Content Management, System
Management, or their Read Only variants. Then:

```sh
ivanti-neurons auth login
```

This validates the credentials, detects the tenant's default device space,
and writes `~/.config/ivanti-neurons/config.yml` (mode 0600). The URL is
your tenant's admin portal host and defaults to the EU data center,
`https://eu1.mobileiron.com` (press Enter at the prompt); US tenants use
e.g. `https://na1.mobileiron.com`. Pasting the login page URL
(`https://eu1.mobileiron.com/login.html#!/`) works too.

API users with **MFA** cannot use Basic auth. Use an OIDC client (OAuth2
client credentials) instead:

```sh
ivanti-neurons oidc create <account-guid>   # as a Basic-auth admin, or in the admin portal
ivanti-neurons auth login --client-id ... --client-secret ...
```

Multiple tenants are supported:

```sh
ivanti-neurons auth login --name us --url https://na1.mobileiron.com --username api@example.com
ivanti-neurons device list --instance us
```

For CI or one-off use, skip the config file entirely:

```sh
export IVANTI_URL=https://eu1.mobileiron.com
export IVANTI_USERNAME=api@example.com IVANTI_PASSWORD=...
# or: export IVANTI_CLIENT_ID=... IVANTI_CLIENT_SECRET=...
```

Resolution order: `IVANTI_URL` + credentials env vars → `--instance` flag →
`IVANTI_INSTANCE` env var → `default` entry in the config file.

### Device spaces

Most calls are scoped to a device space (`dmPartitionId` in the API). The
tenant's default space is used unless you pass `--space <id|name>` or set
`IVANTI_SPACE`. `ivanti-neurons space list` shows all spaces.

## Usage

Every command has `--help`; `ivanti-neurons docs` prints the whole reference
in one page. Highlights:

### Devices

```sh
ivanti-neurons device list --platform ios --status active
ivanti-neurons device list --email jane@example.com
ivanti-neurons device list --attr cityId=SF --fq "CFLDAP[department]='Sales'"
ivanti-neurons device list --limit 0 -o json > devices.json   # all devices, paged automatically
ivanti-neurons device with-app com.example.app --platform IOS  # devices with an app installed
ivanti-neurons device view 22184                                # numeric id or GUID
ivanti-neurons device config list 22184
ivanti-neurons device compliance status 22184
ivanti-neurons device attrs set 22184 cityId=SF
ivanti-neurons device assign 22184 --to 10542407
```

### Device actions

```sh
ivanti-neurons device checkin 22184 22190
ivanti-neurons device lock 22184 --message "Call IT"
ivanti-neurons device message 22184 --push "Please update iOS"
ivanti-neurons device os-update 22184 --version 18.6 --check     # viability check only
ivanti-neurons device enable-lost-mode 22184 --message "Lost phone" --phone "+41 44 000 00 00"
ivanti-neurons device retire 22184                               # checks viability, then asks
ivanti-neurons device wipe 22184 --yes                           # irreversible
```

Wipe, retire, relinquish and logout run the API's viability check first and
refuse if any device is not eligible; they then ask for confirmation
(`--yes` in scripts). `--check` on any action only runs the check.

### Users and groups

```sh
ivanti-neurons user list -q jane
ivanti-neurons user view jane@example.com
ivanti-neurons user create --email jane@example.com --first-name Jane --last-name Doe --send-invite
ivanti-neurons user pin generate 10542407
ivanti-neurons user add-to-group 10542407 --group-id 11124
ivanti-neurons group list
ivanti-neurons device-group list
ivanti-neurons device-group devices 47808
ivanti-neurons device-group members add 47808 22184
```

### Apps and configurations

```sh
ivanti-neurons app list --type IN_HOUSE
ivanti-neurons app store search itunes "Microsoft Outlook"
ivanti-neurons app inventory device 22184
ivanti-neurons app install devices com.example.app <device-guid> --async
ivanti-neurons app config list 16005                       # every configuration type; app id or version id
ivanti-neurons app config get 16005 276183                # managed config: the keys devices get
ivanti-neurons app config set 16005 276183 HomepageLocation=https://intranet.example ProxySettings=
ivanti-neurons app task list
ivanti-neurons config list
ivanti-neurons config assign 35140 38002
```

### Tenant, certificates, enrollment

```sh
ivanti-neurons tenant license
ivanti-neurons space list
ivanti-neurons cert revocation-pending "AWS CA" --out pending.csv
ivanti-neurons bulk list
ivanti-neurons bulk create devices.csv --name "Warehouse scanners"
ivanti-neurons dep profiles 1234
ivanti-neurons oidc create <account-guid>          # OAuth2 client for MFA API users
```

### Raw API escape hatch

Any endpoint, with authentication handled for you:

```sh
ivanti-neurons api GET /api/v1/metadata/tenant
ivanti-neurons api GET /api/v1/device -f dmPartitionId=23000 -f rows=10
ivanti-neurons api PUT /api/v1/device/forceCheckin -f ids=22062 -f ids=22196
ivanti-neurons api PATCH /api/v1/device/22000/customattributes --data '{"attrs":{"cityId":["SF"]}}'
```

### Scripting

Every command supports `-o json`, printing the raw API objects (list
commands print one JSON array of all fetched results):

```sh
ivanti-neurons device list --limit 0 -o json | jq -r '.[] | select(.complianceState == false) | .id'
ivanti-neurons user list -o json | jq length
```

Global flags: `--instance <name>`, `--space <id|name>`, `-o/--output
table|json`, `-y/--yes` (skip confirmations), `--verbose` (log HTTP requests
to stderr).

## Development

```sh
make build   # build bin/ivanti-neurons
make test    # go test ./...
make vet     # go vet ./...
make lint    # golangci-lint run
```

Contributions welcome — see [CONTRIBUTING.md](CONTRIBUTING.md) for the commit
convention (Conventional Commits, drives automated changelog and semver
releases) and workflow.

Releases are fully automated: merging the release-please PR on `main` tags a
release; [goreleaser](https://goreleaser.com) then builds binaries and updates
the Homebrew package in `lukeisontheroad/homebrew-tap` (requires the
`HOMEBREW_TAP_TOKEN` repo secret).

## License

[MIT](LICENSE.md)

## Trademark notice

This is an independent community project. It is not affiliated with,
endorsed by, or sponsored by Ivanti, Inc. "Ivanti", "Ivanti Neurons" and
"MobileIron" are trademarks of Ivanti, Inc.; they are used here only to
describe interoperability with the Ivanti Neurons for MDM product.
