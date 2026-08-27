# SpecRoster Coverage Collector

Per-test coverage collection for [SpecRoster](https://specroster.com). One
binary, eight test runners, **zero third-party dependencies** — pure Go
standard library, so there is no supply chain to audit but this repository.

This is the component that runs inside *your* CI and reads *your* code. That
is exactly why it is open: you should be able to read it.

## What it does

For each test in your suite it records which source files and lines that test
executed, and writes two artifacts:

- a **coverage document** — `test ID → {file: [lines]}`
- the **collected test list**, plus per-test durations

Your nightly workflow uploads those to SpecRoster, which inverts them into the
reverse index that pull-request selection resolves against.

```
specroster-collect -runner dotnet -o coverage.json -collected collected.txt
```

| `-runner` | Coverage source | Granularity |
|---|---|---|
| `pytest` | coverage.py dynamic contexts | per test |
| `gotest` | Go coverage profiles | per test |
| `dotnet` | coverlet (msbuild or collector) | per test |
| `jest` | Istanbul | per spec file |
| `junit` | JaCoCo (Maven) | per test |
| `rspec` | SimpleCov | per spec file |
| `cargo` | cargo-llvm-cov | per test |
| `phpunit` | Clover | per test |

Python is the only ecosystem where per-test attribution comes free from a
single instrumented run. Everywhere else the collector invokes each test
separately under that ecosystem's coverage tool, so collection cost scales
with test *count*, not suite duration. Budget the job accordingly.

## What is not here

**Selection is not in this repository.** Deciding *which* tests to run — the
reverse index, the always-run floor, budget-fill, ranking, failure
attribution — happens server-side and is not open source. The collector only
ever *produces* coverage; it never consumes a selection to decide anything.

Nothing here transmits your source code. The collector writes local files.
Uploading is the [action's](https://github.com/SpecRoster/actions) job, and
what it uploads is coverage maps, test lists, and JUnit results.

## Layout

```
cmd/specroster-collect   the binary; -runner picks a handler
collect/<runner>         per-ecosystem collection: drive the native coverage
                         tool, normalize its output
runner/<ecosystem>       adapter: file classification, coverage/test-list
                         parsing, native↔canonical ID normalization
covtypes                 the coverage vocabulary shared with the server
testid                   canonical test-ID normalization
```

## Build

```
go build ./cmd/specroster-collect
```

Go 1.26+, no module dependencies. Prebuilt binaries for
linux/darwin/windows × amd64/arm64 ship on
[Releases](https://github.com/SpecRoster/Collector/releases) with
`SHA256SUMS`; the `coverage` action downloads and verifies one automatically,
so you do not normally install this by hand.

## Tests

```
go test ./runner/... ./covtypes/... ./testid/...   # fast, no toolchains
go test -timeout 30m ./...                          # includes collect/
```

The `collect/` packages drive real sample projects, so a cold run does
`npm install` / `bundle install` / `dotnet build` and can exceed Go's default
10-minute timeout. Those packages need node, ruby, .NET, PHP and Rust
toolchains respectively; the rest need nothing.

## License

Apache License 2.0 — see [LICENSE](LICENSE) and [NOTICE](NOTICE).
