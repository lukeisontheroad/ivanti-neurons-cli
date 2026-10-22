# ivanti-neurons-cli

Go CLI for the Ivanti Neurons for MDM (formerly MobileIron Cloud) REST API.
Single static binary, cobra command tree, no SDK dependency — the API client
is hand-rolled on `net/http`. Unofficial; not affiliated with Ivanti.

## Commands

```sh
make build          # build bin/ivanti-neurons (version via ldflags)
make test           # go test ./...
make vet            # go vet ./...
make lint           # golangci-lint run (config: .golangci.yml, v2 format)
go test ./internal/api/    # single package
```

## Architecture

- `cmd/ivanti-neurons/main.go` — entrypoint; `version` injected via `-ldflags -X main.version=`
- `internal/cmd/` — one file per command group: `root`, `helpers` (shared
  plumbing), `auth`, `api` (raw escape hatch), `docs`, then the API areas
  `device`, `device_actions`, `user`, `group` (user + device groups), `app`,
  `tenant` (tenant, space, config, cert, connector, attribute, oidc, dep),
  `bulk`. Commands call the generic client methods with literal API paths;
  there is no per-endpoint wrapper layer.
- `internal/api/` — transport only: `client.go` (`Raw`/`Do`/`Call`: Basic or
  OAuth2 bearer auth, lazy + reactive token refresh, 429 backoff, envelope
  unwrapping, error mapping, multipart), `search.go` (paging), `tenant.go`
  (tenant metadata, space-name resolution).
- `internal/config/` — `~/.config/ivanti-neurons/config.yml` (0600),
  multi-instance. Resolution order: `IVANTI_URL` + (`IVANTI_USERNAME` +
  `IVANTI_PASSWORD` | `IVANTI_CLIENT_ID` + `IVANTI_CLIENT_SECRET`) env →
  `--instance` flag → `IVANTI_INSTANCE` env → `default` key in config.
  Default device/content space ids and the OAuth2 access token are cached
  per instance (`config.Update`, best-effort).
- `internal/output/` — tabwriter tables, key/value lists, JSON printer, and
  `Format` (epoch-millisecond timestamp fields render as UTC dates).

Tests use `httptest` fixtures, no live tenant required: `internal/api` for
transport, `internal/cmd/harness_test.go` (`newMock`, `m.on`, `m.run`,
`m.last`) for command-level tests, one `<group>_test.go` per command group.

## Ivanti Neurons for MDM API facts (verified against the API guide)

Guide: https://help.ivanti.com/mi/help/en_us/CLD/8x/api/default.htm (an
OpenAPI 3 export of a subset lives at `.../CLD/8x/api/test.json`). Calls
not in the guide are "unpublished and unsupported" per Ivanti.

- Auth: HTTP Basic with a local user holding the "API" role plus the
  functional roles (Device/User/App & Content/System Management or their
  Read Only variants). Basic auth fails when the user has MFA; then use an
  OIDC client: `POST /oauth2/token` with `grant_type=client_credentials`
  and Basic `client_id:client_secret` → bearer token (~1h). API users
  bound to a GDPR profile get obfuscated PII fields.
- `GET /api/v1/metadata/tenant` works for any authenticated user — used as
  the credential check and for `defaultDmPartitionId` /
  `defaultCmPartitionId`.
- Responses use `{"errors": ..., "result": ...}`; errors are
  `{"fieldErrors": [{code, field, message}], "globalErrors": [{code,
  message}]}`. `Call` returns `result`; some endpoints (custom attributes)
  have no envelope and come back whole. `-o json` prints `result`; list
  commands print the merged `searchResults` array.
- Device space = API "device partition" = `dmPartitionId`; most device,
  group, config and app calls require it. Content space = `cmPartitionId`
  (app store: reviews, delete, in-house lists). App API operations apply
  to the default space only.
- Paging: `rows` max 500 (HTTP 400 above), `start` offset. Database-backed
  lists (user/device groups) may return only 50 rows per page regardless,
  so `Search` stops on `totalCount`, not on short pages. Device and account
  searches must not use `start` ≥ 10000: sort by id and pass
  `searchAfterValues[0]=<last id>` (`Search` switches automatically when
  sorting by `id`). Sort: `sortFields[0].name` / `sortFields[0].order`.
- Filters (`fq`): `field='value'` clauses joined by AND/OR; custom device
  attributes `CFDEVICE[name]`, LDAP attributes `CFLDAP[name]`; numeric
  custom attributes need a `_#` suffix on the name. The guide also uses a
  second form for some fields (`UID EQ 'x'`, `EMAILADDRESS EQ 'x'`) —
  `device list` typed filters follow whichever form the guide shows per
  field. Relative time: `lastCheckin<'hh:mm:ss'`,
  `lastRegistrationTime<'d:hh:mm:ss'` ("within the last ..."). fq values
  are not escaped; a `'` inside a value breaks the filter.
- App-inventory device search is `GET /api/v1/device?type=APP_INVENTORY`
  with app fields in `fq` (bundleId, version) and `platformType`.
- Write parameters: device actions and most PUT/POST calls bind query (or
  form) parameters, not JSON — send them as `query` url.Values, repeating
  keys for lists (`ids=1&ids=2`). JSON bodies only where the guide shows
  them (custom attributes, app install v2, ...).
- Destructive device actions have a viability check (`GET` or `PUT` with
  `checkViability=true&ids=...`) returning the rejected devices; wipe,
  retire and relinquish run it before acting. Device delete requires the
  device not to be ACTIVE or WIPE_PENDING.
- Rate limit: per-device app inventory (`GET /api/v1/app/inventory`) is
  capped at 30 calls/minute/user; `Raw` retries 429 twice (1s/3s).
- Timestamps are epoch milliseconds.

### Observed server behaviour

Confirmed against a real tenant (all read-only commands, the GET
viability checks, and the writes listed below):

- Unauthenticated API calls answer `302` to `/login.html`; the client does
  not follow redirects and reports rejected credentials.
- Viability checks return `{totalCount, searchResults, offset, limit}` with
  `actionExceptionCodes` per rejected device (e.g. `PLATFORM_NOT_SUPPORTED`,
  `DEVICE_NOT_SUPERVISED`, `OS_VERSION_NOT_SUPPORTED`).
- `fq` accepts both `EMAILADDRESS EQ 'x'` (quoted) and unquoted values, and
  parenthesised `(a) AND b` combinations. App type filters work only as
  `fq=appType='IN_HOUSE'`; `facetFq` is silently ignored on `/api/v1/app`.
- `POST /api/v1/account/search` rejects a bodiless POST with HTTP 415; it
  needs a JSON body (`{}` works).
- `GET /api/v1/app/itunes/{id}` requires `countryCode` (HTTP 400 otherwise).
- `GET /api/v1/app/googleplay` (search) returns HTTP 500 with either
  `cmPartitionId` or `dmPartitionId`, exactly as documented — server side.
- `GET /api/v1/app/distributionfilters/{dmPartitionId}` returns 404 (no
  variant found); the documented call is not served on this release.
- Writes verified with self-cleaning test objects: device force check-in,
  message and lock with message (form body); user invite; custom attribute definitions CRUD (types DEVICE,
  ACCOUNT, APP; app custom attributes 400 "DATA-100" unless the key is
  defined as APP); device/account/app attribute set/update/remove incl. bulk;
  user create/update/group membership/PIN/enrollment URL/delete; user group
  create/members/delete; bulk enrollment create/upload/add/edit/update/
  set-expiry/set-user/refresh-token/device delete/delete.
- The API omits null fields from responses (no key is ever `null`), so a
  field missing from a lookup is empty server-side.
- `GET /api/v1/account?uid=` answers 403 (not false) for an unknown UID.
- User group `q` matches the exact name only.
- `PUT /api/v1/bulkenrollment/profile` takes `{id, name, description}` and
  clears an omitted description; `PUT .../{id}/devices` replaces the whole
  record. Both commands load and resend current values.
- Bulk CSV header: `imei,manufacturer,serial_number,custom_attributes,username`.
- Refreshing a bulk token resets its expiry to now + 7 days.
- `PUT .../{id}/qrcode` accepts any body tried and changes nothing visible:
  `bulk configure` remains unverified.
- App attribute PATCH without `forceOverwrite` leaves existing values
  unchanged.
- App configurations: one list per type (`GET .../version/{vid}/config?type=`
  — `type` is required), the types of a version from `.../config/types`.
  The lists omit the assignments; `GET .../version/{vid}/config/{id}` (not
  in the guide) returns them (`groupIds`, `accountIds`, `deviceIds`,
  `ruleGroupIds`). `PUT .../config/{id}` replaces the assignments too — a
  body without them unassigns the configuration — and rejects
  `settings.configuration.uuid` unless null. `app config set/update` build
  the body from the single GET (`appConfigPutBody`) and refuse to write when
  the assignments are unknown.
- Android managed configurations (`ANDROID_FOR_WORK`) keep their keys in
  `settings.configuration.restrictions`: key → {type BOOL|STRING|INTEGER|
  CHOICE|MULTISELECT|…, enabled (= pushed), value, title, entry[],
  entryValue[]}. CHOICE values are the `entryValue` strings.
- Version-scoped app endpoints answer 403 for an app id; `appVersionArg`
  resolves app ids to their latest version instead. Generally the API
  answers 403 (not 404) for many ids that do not exist.
- Undocumented calls the CLI uses: `GET /api/v1/account/{id}` (account by
  id) and `GET /api/v1/app/version/{vid}/config/{id}` (configuration with
  assignments); both answer `result: null` for an unknown id.
- Account `fq` ignores `id=`; `ACCOUNTGROUP='<name>'` lists group members;
  device `fq` takes `ownerId=` and `lastCheckin>'d:hh:mm:ss'` (older than).

### Doc quirks resolved (unconfirmed unless listed above)

The guide has copy-paste errors; where its URI, parameter table and curl
example disagree, the CLI follows the curl example. Notable decisions:

- Parameter tables label most ids "Request body" or "Path" even when the
  example sends them as query/form values — the CLI sends query values.
- Custom attributes: device POST replaces all, PATCH merges
  (`forceOverwrite`); bulk app/account PATCH nests `{"attrs":{"attrs":{}}}`;
  delete-by-key bodies are `{"ids":[...],"attrKeys":[...]}` with string ids
  for accounts. The three account custom-attribute pages have titles that
  contradict their examples (`user attrs` follows the examples).
- `PUT /api/v1/account` clears every editable field not sent, and there is
  no GET by id: `user update` looks the account up by UID and resends it
  (omitted fields are null, see above). Account fields and passwords go in
  a form body, never the URL.
- Delete viability checks (`user delete`, `group delete`) fail closed: any
  flagged or unreadable result stops the delete, even with `--yes`.
- `POST /api/v1/rule_group/{id}/devices` replaces the static membership;
  `device-group members add` reads the group first (refuses or confirms
  when the members cannot be read). Device groups cannot be deleted via API.
- Viability checks return a search page of the *ineligible* devices (empty
  = all fine); the relinquish check is a PUT returning the eligible count.
  Any other shape fails closed. Swagger instead documents
  `GET /api/v1/device/wipe?checkLimits=true` returning a boolean; if a live
  tenant rejects `checkViability` on wipe, `device wipe` refuses (safe) and
  this needs revisiting.
- Device action parameters go in a form body (as the guide's curl
  examples), not the query string: keeps PINs out of access logs and long
  id lists under URL limits. Checks and deletes still use the query string
  (about 600 ids per call before typical 8 KB URL limits).
- Version-level app custom-attribute PATCH drops attributes not named in
  the call (guide); only the device/account PATCH merges.
  `/message` uses `deviceIds` + `dmPartitionId`; other actions use `ids`.
  restart / scheduleOSUpdate return a 1/0 success flag, not a count.
- OIDC: the older DeepLink page is corrupt; `/api/v1/oidc/client` and
  `/api/v1/oidc/secret-reset` come from the newer page.
- `POST /c/i/dep/custom.mobileconfig` needs the `x-apple-aspen-deviceinfo`
  header (`api.Client.Header`). `GET /api/v1/depServer/{virtualServerId}/profiles`
  takes the DEP server id, not a profile id.
- Bulk enrollment: create/upload are multipart (`csv-data`);
  `qrcode/generate` returns the Android provisioning JSON as a string, not
  an image.
- `/api/v1/ldapserver/{id}/sync` returns the LDAP server including its bind
  password in plain text — table output prints only a message.
- App store reviews (`/c/api/v1/appstore/...`) reject Basic auth. Google
  Play/iTunes/review calls take `cmPartitionId` per the guide (swagger says
  `dmPartitionId`). `GET /api/v1/applist` wraps its page in an array and is
  not paged by the CLI. "Delete an App" deletes one version.

## Conventions

- New commands: constructor `newXxxCmd() *cobra.Command` in
  `internal/cmd/`, registered in `root.go` (or under its group). Always
  support `-o json`; `newClient()` validates `--output` before any request,
  so every API command must create its client before printing anything.
- Use the shared helpers in `helpers.go` instead of parallel logic:
  `spaceID`/`contentSpaceID`, `params`, `parseID`/`parseIDs` (validate ids
  before they reach a URL), `isGUID`, `jsonNumbers`, `confirm`,
  `render`/`renderItems`/`report`, `searchOpts` + `runSearch`,
  `fieldValues` (query) / `jsonBody` (`--data` + `-f`) / `readData`,
  `parseAttrs`, `saveFile` (downloads: stdout or a new 0600 file, never
  overwriting).
- Destructive commands call `confirm()` (honors `--yes`; refuses without a
  terminal). Validate id arguments before putting them into URL paths.
- Write to `cmd.OutOrStdout()`/`cmd.ErrOrStderr()`, never `os.Stdout` —
  tests capture output that way. Informational asides go to stderr so
  `-o json | jq` stays clean.
- Errors: return them; root command has `SilenceUsage` — no manual usage dumps.
- `docs` tags every command [read]/[write]/[destructive] from the
  `docsReadOnly`/`docsDestructive` lists in `docs.go` (default [write]); add
  new read-only or destructive commands there (a test checks the paths).
  Write help for an LLM reader: name the command that produces each id,
  give a next step in every error, no maintainer notes in help texts.
- `internal/cmd/coverage_test.go` maps every documented endpoint to the
  command that calls it and checks the request (and that `-o yaml` fails
  before any request). A new or changed endpoint must be reflected there.

## Commits

Conventional Commits, enforced by commitlint in CI and consumed by
release-please for versioning + CHANGELOG (see CONTRIBUTING.md).
`feat:` = minor, `fix:`/`perf:` = patch, `!`/`BREAKING CHANGE:` = major;
`docs:`/`test:`/`ci:`/`chore:`/`refactor:` don't bump. Imperative, lowercase,
no trailing period; optional scope = command or package name
(`feat(device): ...`). Write descriptions for the changelog reader.

## Release

Fully automated: release-please watches `main`, maintains a release PR with
the next semver version + CHANGELOG.md from conventional commits; merging it
creates the `v*` tag, which triggers goreleaser
(`.github/workflows/release.yml`): darwin/linux/windows binaries + Homebrew
cask pushed to `lukeisontheroad/homebrew-tap` (needs `HOMEBREW_TAP_TOKEN`
secret — PAT with repo scope on the tap repo). Never tag manually.
