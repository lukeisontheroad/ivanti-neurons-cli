# Contributing

## Commit style

This repo uses [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/).
Commit messages drive the changelog and the semantic version — write them for
the release notes, not for yourself.

```
<type>(<optional scope>): <description>

[optional body]

[optional footer]
```

### Types and their semver effect

| Type | Meaning | Version bump |
|---|---|---|
| `feat` | new user-facing feature | **minor** |
| `fix` | bug fix | **patch** |
| `perf` | performance improvement | patch |
| `refactor` | code change, no behavior change | none |
| `docs` | documentation only | none |
| `test` | tests only | none |
| `build` | build system, dependencies | none |
| `ci` | CI configuration | none |
| `chore` | maintenance, tooling | none |

**Breaking changes**: append `!` after the type (`feat!: ...`) or add a
`BREAKING CHANGE:` footer — either forces a **major** bump.

### Examples

```
feat(device): add --checked-in-within filter to device list
fix(config): respect IVANTI_INSTANCE when --instance is unset
feat!: rename --output values from yaml to json
docs: document token permissions in README
```

Scopes are optional; use the command or package name when it helps
(`device`, `user`, `app`, `auth`, `api`, `config`, `output`).

### Rules

- Imperative mood, lower case, no trailing period: `add X`, not `Added X.`
- The description completes: "this commit will …"
- One logical change per commit; split unrelated changes.

Commits are linted in CI (commitlint, PRs only); non-conforming messages fail
the build.

## Releases

Releasing is automated, no manual version picking:

1. [release-please](https://github.com/googleapis/release-please) watches
   `main`, collects conventional commits, and maintains a release PR with the
   next semver version and a generated `CHANGELOG.md`.
2. Merging that PR creates the `v*` tag.
3. The same workflow then runs goreleaser: binaries, GitHub release (notes grouped by
   commit type), and the Homebrew formula update.

## Development

```sh
make build   # build bin/ivanti-neurons
make test    # go test ./...
make vet     # go vet ./...
make lint    # golangci-lint run
```

All three of vet, test, and lint must pass; CI enforces them plus govulncheck.
