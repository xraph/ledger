# AGENTS.md

Ledger is a usage-based billing engine you import as a Go library. It covers plans, a feature catalog, subscriptions, metering, entitlements, invoices and coupons, and ships a Forge extension plus a Forge dashboard contributor. The module path is `github.com/xraph/ledger`.

It's one module with no `cmd/` and no binary.

## Commands

Run everything from the repo root. CI has no `go.work`, so set `GOWORK=off` when you build locally and you'll compile exactly what `go.mod` pins.

| What | Command |
|---|---|
| Build | `GOWORK=off go build ./...` |
| Test, as CI runs it | `make test` (`go test -v ./...`) |
| Race tests, as release runs them | `go test -race -count=1 ./...` (or `make test-race`) |
| Vet | `make vet` |
| Lint | `make lint`, but read the lint section first |
| Format | `make fmt` (`gofmt -s -w .`, then `goimports -w -local github.com/xraph/ledger .`) |
| Format, vet and lint together | `make check` |
| Coverage | `make coverage` |
| Docs site | `make docs` or `make docs-build` (pnpm, inside `docs/`) |

`make build`, `make run`, `make dev` and `make install` point at `./cmd/ledger`, which doesn't exist, so they fail. `make all` ends in `build` and fails the same way. Skip them.

`.github/workflows/ci.yml` calls the shared `xraph/workflows/.github/workflows/go-ci.yml@v1` on Go 1.26 (setup-go resolves the newest patch) and adds a Format job and a Docs job. Together they run:

- `make test`, then a race-enabled coverage run (`go test -race -covermode=atomic -coverprofile=coverage.out ./...`), since there is no `test-coverage` target
- golangci-lint, at the version go-ci.yml pins
- `gofmt -l .`, `make vet`, and `go mod tidy` followed by `git diff --exit-code -- go.mod go.sum`, so an untidy `go.mod` fails the build
- gosec (`-exclude=G115`) and govulncheck, both of which fail on findings
- `goimports -l -local github.com/xraph/ledger .`
- `pnpm types:check`, `pnpm lint` and `pnpm build` in `docs/`

## Tests that need a database

Memory and SQLite run the store conformance suite in `store/storetest` on every `go test ./...` with nothing installed. Postgres and Mongo don't. Their tests call `t.Skip` unless you set:

- `LEDGER_TEST_POSTGRES_DSN`
- `LEDGER_TEST_MONGO_URI` (put the database name in the URI; grove's mongo driver refuses a URI without one)

CI sets neither. A green CI run has never touched postgres or mongo, and a skipped suite looks like a pass unless you run with `-v` and look for `SKIP`. So when you change anything under `store/`, start throwaway containers and run the suites for real:

```sh
docker run -d --rm --name ledger-test-pg -p 55432:5432 \
  -e POSTGRES_PASSWORD=ledger -e POSTGRES_DB=ledger_test postgres:17-alpine
docker run -d --rm --name ledger-test-mongo -p 57017:27017 mongo:7

# give postgres a few seconds to accept connections, then:
LEDGER_TEST_POSTGRES_DSN='postgres://postgres:ledger@localhost:55432/ledger_test?sslmode=disable' \
LEDGER_TEST_MONGO_URI='mongodb://localhost:57017/ledger_test' \
GOWORK=off go test -count=1 -v ./store/...

docker rm -f ledger-test-pg ledger-test-mongo
```

Use fresh containers, not ones another project already runs. The postgres harness migrates the database in place and never drops it, so don't aim it at anything you care about. The mongo store does not use transactions, which is why a standalone `mongod` is enough.

## Lint

The config is `.golangci.yml` (golangci-lint v2). By default golangci-lint shows at most three identical messages, which means the count you see locally, and the count CI reports, can be short. Run it uncapped before you call a lint fix finished:

```sh
golangci-lint run --max-same-issues=0 --max-issues-per-linter=0
```

These are the rules that catch people in this repo:

- govet runs `enable-all` (only `fieldalignment` is off), and that includes shadow. `if err := f(); err != nil` inside a function that already has an `err` gets flagged. The obvious fix, `if err = f(); err != nil { return err }`, trips gocritic's `sloppyReassign`. In non-test code, give the inner error its own name: `qtyErr`, `pageErr`, `syncErr`. That applies to `store/storetest/storetest.go` and the rest of `store/storetest`, which are ordinary files and not `_test.go`. Test files can reuse `err` because gocritic doesn't run on them.
- gosec, errcheck, gocritic, revive and staticcheck are all excluded for `_test.go`. A `//nolint:` naming any of them in a test file suppresses nothing, so nolintlint reports it as unused. Write a plain comment there.
- errcheck has `check-blank: true` and `check-type-assertions: true`. `_ = f()` is still a finding. Best-effort calls carry a nolint with a reason, such as `//nolint:errcheck // best-effort cache invalidation`.
- In the postgres and sqlite stores, the rollback defer is always this one line:

  ```go
  defer func() { _ = tx.Rollback() }() //nolint:errcheck // no-op after commit, unactionable otherwise
  ```

- errorlint wants `errors.Is`, `errors.As` and `%w`. errname wants sentinels called `Err...` and error types ending in `Error`.
- goimports runs with `local-prefixes: github.com/xraph/ledger`, so ledger's own imports go in the last group.

Every `//nolint` in the tree names its linter and gives a reason after `//`. Keep it that way.

For govulncheck, match CI's Go. CI always gets the newest 1.26 patch, and an older local toolchain makes govulncheck report stdlib advisories CI never sees. Run `GOTOOLCHAIN=go1.26.N govulncheck ./...` with N set to that newest patch.

## Layout

| Path | What lives there |
|---|---|
| `ledger.go`, `lifecycle*.go`, `*_write.go`, `coupon_apply.go`, `invoice_period.go`, `provider_import.go`, `inspect.go` | The engine, `*ledger.Ledger`: options, metering, entitlements, invoice generation, the lifecycle clock, operator writes, provider sync and import |
| `errors.go` | Sentinel errors and the `IsNotFound`, `IsQuotaError`, `IsRetryable` helpers |
| `id/`, `id.go` | `id.ID`, a TypeID with a prefix per entity (`plan`, `sub`, `inv`, `cpn`...), re-exported at the root |
| `types/`, `exports.go` | `Money` (int64 minor units with checked arithmetic) and `Entity`, re-exported at the root |
| `plan/`, `feature/`, `subscription/`, `meter/`, `entitlement/`, `invoice/`, `coupon/` | Domain models, list options and per-entity store interfaces |
| `store/store.go` | `store.Store`, the one interface every backend implements |
| `store/memory`, `store/postgres`, `store/sqlite`, `store/mongo` | The backends. The last three run on grove and keep their migrations in `migrations.go` |
| `store/storetest/` | The conformance suite (`Run`) and a constructor per backend |
| `plugin/` | Hook interfaces (`OnInit`, `OnPlanCreated`, ...) and the registry |
| `provider/` | The payment provider interface |
| `audit_hook/` (package `audithook`), `observability/` | Plugins for an audit trail and for metrics |
| `extension/` | The Forge extension, its `Config` and its options |
| `extension/contract/` | The `ledger` dashboard contributor: `manifest.yaml` and a typed handler per intent |
| `docs/` | The Fumadocs site |
| `MIGRATION.md` | What changed for users of the removed templ dashboard, and in the engine since |

## Conventions

Errors are package-level sentinels in `errors.go`, all prefixed `ledger:`. The engine wraps them with context (`fmt.Errorf("%w: a tenant is required", ErrInvalidInput)`) and callers test with `errors.Is`. Backends return the specific sentinel for a missing row, such as `ledger.ErrPlanNotFound`, and wrap driver errors with a backend prefix (`ledger/postgres: ...: %w`).

IDs come from the `id` package. Create them with `id.New(prefix)` or the typed helpers like `id.NewUsageEventID()`, and never build the string by hand.

`store.Store` is the contract. If you add or change a method, you change all four backends and add a subtest to `store/storetest`, then you run it against real postgres and mongo as above. Writes that can race the lifecycle clock are single conditional statements that repeat their precondition in the WHERE clause (the filter, on mongo) and return `(bool, error)`: false with no error means no row matched. The engine works in UTC (sqlite compares timestamps as text, so one zone matters) and cuts the times it stores and hands back, like a subscription's periods, to the millisecond, because mongo keeps milliseconds and postgres microseconds. Each backend versions its migrations on its own, so when you add one, give it the next `Version` in that backend's `migrations.go`. In list methods, an empty `appID` means every app.

Both `ledger.New(store, opts...)` and `extension.New(opts...)` use functional options (`type Option func(*Ledger)` and `type Option func(*Extension)`). The extension reads YAML under `extensions.ledger`, falling back to `ledger`, and merges it with the programmatic options. Its store comes from `WithStore`, then a named grove DB (`WithGroveDatabase` or `grove_database`), then the default `*grove.DB` in the container, then memory as a last resort. The extension provides the `*ledger.Ledger` to the container with `vessel.Provide`.

Logging uses `github.com/xraph/go-utils/log`. The engine defaults to `log.NewNoopLogger()`, and the extension hands it the Forge logger.

To add a dashboard intent, you add a line to `extension/contract/manifest.yaml`, bind a handler through the binder, and bump `wantIntents` in `completeness_test.go`. That test fails whenever the manifest and the bindings disagree. The React pages that call these intents live in `packages/plugin-ledger` in forge-dashboard.

Engine tests run on the memory store. When your test depends on time, pin it with `ledger.WithClock`.

## Dependencies

Ledger depends on grove (with `drivers/mongodriver`, `drivers/pgdriver` and `drivers/sqlitedriver`) and forge, plus `go-utils` and `vessel` from xraph. authsome consumes it. See `go.mod` for current versions.

Grove's driver modules are tagged together with grove itself, so bump all four to the same version:

```sh
GOWORK=off go get github.com/xraph/grove@vX.Y.Z \
  github.com/xraph/grove/drivers/mongodriver@vX.Y.Z \
  github.com/xraph/grove/drivers/pgdriver@vX.Y.Z \
  github.com/xraph/grove/drivers/sqlitedriver@vX.Y.Z
GOWORK=off go get github.com/xraph/forge@vX.Y.Z
GOWORK=off go mod tidy
```

Never commit a `go.work`, and never commit a `replace` that points at a sibling checkout. You can use one locally to try an unreleased grove or forge, but take it out before you push. CI has no sibling checkout to resolve it against, and a consumer like authsome ignores the replace entirely and builds against whatever the `require` line names.

## Releasing

Cut a release by dispatching the workflow. Don't push a tag by hand.

```sh
gh run list --workflow ci.yml --branch main --limit 1   # main must be green first
gh workflow run release.yml --ref main -f tag=vX.Y.Z
```

`release.yml` creates the tag on the dispatched commit and pushes it, then runs `go build ./...` and `go test -race -count=1 ./...`, writes notes from the commits since the previous tag, and publishes the GitHub release. The tag goes up before the tests run. If they fail, you're left with a tag and no release. Dispatching the same tag again fails at `git tag`, so fix forward with the next patch version. There is one module, so there is one tag.

Release ledger only once the grove and forge versions it needs are themselves released. After it ships, authsome re-pins with `go get github.com/xraph/ledger@vX.Y.Z`.

## Branches and commits

`main` has no branch protection and no rulesets. We push to it directly. Commit subjects follow conventional commits, usually with a scope, for example `fix(store/sqlite): ...` or `chore(deps): ...`.
