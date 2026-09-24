# takeyourcoat developer guide

takeyourcoat is a single Go `package main` with no dependencies outside the
standard library. It serves a small web portal, sends a magic link over
SMTP, and on confirmation adds the client's IPv4 address to an allowlist kept
in a JSON file. Caddy gates Jellyfin by calling the app's `GET /check`
through `forward_auth`. Operators should read
[the operator guide](../user/README.md) instead.

## Pages

| Page | What it covers |
|------|----------------|
| [Architecture](architecture.md) | Files and functions, the request lifecycle of each route, middleware, embedded CSS |
| [Security design](security-design.md) | Each control and the function that implements it |
| [Configuration internals](configuration-internals.md) | `loadConfig`, `applyEnv`, `validate`, adding a setting |
| [Testing](testing.md) | Test layout, the allowlist in tests, handler and `/check` tests, adding a test |
| [Packaging and CI](packaging-and-ci.md) | Dockerfile, workflows, releases, digest updates |
| [Decisions](decisions.md) | Design decisions and the alternatives rejected |

## Build and test locally

`go.mod` declares `go 1.27.0`. With `GOTOOLCHAIN=auto`, an older local `go`
command downloads and uses the 1.27 toolchain automatically.

```sh
GOTOOLCHAIN=auto go vet ./...
GOTOOLCHAIN=auto go test -race ./...
```

`-race` needs cgo and a C compiler (Xcode command line tools on macOS,
`build-essential` on Debian/Ubuntu). Without one, drop `-race`.

To check that the code compiles into a binary (without running it):

```sh
GOTOOLCHAIN=auto go build -o /dev/null .
```

To run the vulnerability check CI runs:

```sh
GOTOOLCHAIN=auto go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

To build the image:

```sh
docker build -t takeyourcoat:dev .
```

## Rules for working on this code

- **Only ever run the app inside Docker.** It needs no privilege, but its
  default state file is `/data/allowlist.json`, and tests cover the behaviour
  without running it.
- **Tests write state only under `t.TempDir()`.** `newTestServer` and
  `newTestAllowlist` point the allowlist at a temporary file. See
  [Testing](testing.md).
- **No `os/exec`.** The app starts no processes; keep it that way so the
  container can stay capability-free. See [Security design](security-design.md#no-shell-no-exec).
- **Standard library only.** Adding a module dependency changes the security
  posture of the process that decides who reaches Jellyfin; see
  [Decisions](decisions.md).
- **No secrets in logs.** Never log the `Config` struct or the SMTP password.
- Placeholders only in code, tests and docs: `hello.example.com`,
  `jellyfin.example.com`, `alice@example.com`, and `203.0.113.0/24` or
  `198.51.100.0/24` for public test addresses.
