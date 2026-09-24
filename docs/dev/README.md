# takeyourcoat developer guide

takeyourcoat is a single Go `package main` with no dependencies outside the
standard library. It serves a four-route web portal, sends a magic link over
SMTP, and on confirmation runs `ipset add`. Operators should read
[the operator guide](../user/README.md) instead.

## Pages

| Page | What it covers |
|------|----------------|
| [Architecture](architecture.md) | Files and functions, the request lifecycle of each route, middleware, embedded CSS |
| [Security design](security-design.md) | Each control and the function that implements it |
| [Configuration internals](configuration-internals.md) | `loadConfig`, `applyEnv`, `validate`, adding a setting |
| [Testing](testing.md) | Test layout, the `runIpset` fake, handler tests, adding a test |
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

- **Only ever run the app inside Docker, and never with `NET_ADMIN` on a
  development machine.** The binary's one side effect is changing the kernel's
  ipset state. On a laptop that is at best an error and at worst a change to
  your own host's firewall sets. Tests cover the behaviour without it.
- **Tests never exec the real `ipset`.** `TestMain` in `ipset_test.go` replaces
  the package variable `runIpset` with a function that always fails, and tests
  that need a working ipset install a recording fake. See
  [Testing](testing.md).
- **Standard library only.** Adding a module dependency changes the security
  posture of a `NET_ADMIN` container; see [Decisions](decisions.md).
- **No secrets in logs.** Never log the `Config` struct or the SMTP password.
- Placeholders only in code, tests and docs: `hello.example.com`,
  `jellyfin.example.com`, `alice@example.com`, and `203.0.113.0/24` or
  `198.51.100.0/24` for public test addresses.
