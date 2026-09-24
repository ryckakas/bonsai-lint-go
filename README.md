# bonsai-lint for the Go toolchain

[bonsai-lint](https://bonsai.kauneckas.dev) is a cognitive complexity linter for Go, PHP,
JavaScript, TypeScript and Vue. This module lets a Go project install and pin it with Go's own
tools:

```bash
go install bonsai.kauneckas.dev/bonsai-lint@latest      # then: bonsai-lint ./...
```

```bash
go get -tool bonsai.kauneckas.dev/bonsai-lint@latest    # Go 1.24+, pinned in go.mod
go tool bonsai-lint .
```

`go run bonsai.kauneckas.dev/bonsai-lint@latest .` works too. Everything after the command name
goes straight to bonsai-lint; see its [documentation](https://bonsai.kauneckas.dev) for the flags
and configuration.

## How it works

bonsai-lint is written in Rust. This module is a small launcher with no dependencies, so
`go get -tool` adds exactly one line to your `go.mod`.
- **The first run of a version** downloads that release's prebuilt archive for your platform from
  GitHub Releases. It checks the archive against the sha256 recorded in this module's source for
  that version, and unpacks only the binary. One line on stderr says so.
- **Later runs** start the cached binary directly. There is no network access and no extra
  output, and stdout is only ever bonsai-lint's own.
- **The binary replaces the launcher process** on macOS and Linux, so its exit code, signals and
  output are exactly those of bonsai-lint. On Windows the launcher runs it and passes on its exit
  code.

The module version is always the bonsai-lint version: `@v0.3.0` runs bonsai-lint 0.3.0. Each
version here is created by bonsai-lint's release pipeline, which writes that release's checksums
into `release.go` and tags this repository. Go's checksum database therefore also vouches for the
binary you run.

## Platforms

| Go platform | Release binary |
| --- | --- |
| `darwin/arm64`, `darwin/amd64` | macOS |
| `linux/amd64`, `linux/arm64` | Linux with glibc 2.35 or newer (Ubuntu 22.04, Debian 12, RHEL 9 and newer) |
| `windows/amd64` | Windows |

- **Other platforms fail with an explanation.** That includes musl Linux such as Alpine, glibc
  older than 2.35, and Windows on ARM. Install bonsai-lint from source with
  `cargo install bonsai-lint` there, and point `BONSAI_LINT_BINARY` at the result.
- **These gaps are known.** musl, Windows on ARM and an older glibc floor are on the
  [roadmap](https://github.com/ryckakas/bonsai-lint/blob/main/ROADMAP.md).

## Settings

| Variable | Effect |
| --- | --- |
| `BONSAI_LINT_BINARY` | Run this binary instead of downloading one. For offline machines, or a build of your own. |
| `BONSAI_LINT_DOWNLOAD_URL` | Download from this base URL instead of the GitHub release, e.g. an internal mirror holding the same archive names. Archives are still checked against the recorded checksums. dist's shell and PowerShell installers read the same variable. |
| `BONSAI_LINT_CACHE` | Cache directory. The default is `bonsai-lint` inside the user cache directory. |

The default cache directory is:
- `~/Library/Caches/bonsai-lint` on macOS;
- `$XDG_CACHE_HOME/bonsai-lint` or `~/.cache/bonsai-lint` on Linux;
- `%LocalAppData%\bonsai-lint` on Windows.

Each version stays in its own directory, because different projects can pin different versions.
Delete old ones whenever you like. In CI, cache that directory, or set `BONSAI_LINT_CACHE` to a
cached path, to skip the download on each run.

## Development

```bash
go vet ./... && go test ./...
```

On `main`, `release.go` is a placeholder, so a launcher built from a checkout has nothing to
download. Run it with `BONSAI_LINT_BINARY`, or let the tests cover it.
- **Release commits are machine-written.** The `release vX.Y.Z` commits and tags come from
  bonsai-lint's release pipeline. `internal/generate` writes `release.go` from that release's
  `dist-manifest.json`.
- **What gets tagged:** at each bonsai-lint release, whatever is on `main` is tagged together with
  that release's `release.go`. Land launcher changes only when they are ready to ship.

MIT licensed, like bonsai-lint.
