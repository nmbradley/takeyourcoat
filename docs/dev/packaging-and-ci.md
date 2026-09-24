# Packaging and CI

[Developer guide](README.md) > Packaging and CI

## Dockerfile

```dockerfile
# golang:1.27-alpine
FROM golang:1.27-alpine@sha256:8a5910f3... AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /takeyourcoat .

# alpine:3.23
FROM alpine:3.23@sha256:85fe1e81...
LABEL org.opencontainers.image.source=... description=... licenses="MIT"
RUN apk add --no-cache ipset
COPY --from=build /takeyourcoat /takeyourcoat
# Runs as root on purpose. The compose file drops every capability except
# NET_ADMIN and sets no-new-privileges. Under that flag the kernel refuses to
# grant capabilities on exec, so a non-root user plus setcap on ipset would
# always fail with "Operation not permitted". Root holds NET_ADMIN directly.
EXPOSE 8080
ENTRYPOINT ["/takeyourcoat"]
```

(Digests shortened here; see the file for the full values.)

### Stage 1: `build`

| Step | Why |
|------|-----|
| `golang:1.27-alpine@sha256:...` | Go 1.27 to match `go.mod`. Pinned by digest; the comment above records the human-readable tag for Dependabot and reviewers. |
| `COPY . .` | No `go mod download` step: there are no dependencies. |
| `CGO_ENABLED=0` | Fully static binary, no libc dependency in the runtime stage. |
| `-trimpath` | Removes local file system paths from the binary. |
| `-ldflags="-s -w"` | Strips the symbol table and DWARF debug info to shrink the binary. |

### Stage 2: runtime

| Step | Why |
|------|-----|
| `alpine:3.23@sha256:...` | Small base that ships an `ipset` package. Pinned by digest. |
| `LABEL org.opencontainers.image.*` | Source repository, description and licence, so GHCR links the package to the repo. |
| `apk add --no-cache ipset` | `ipset` is the only command the app runs. Nothing else is installed. |
| `COPY --from=build /takeyourcoat /takeyourcoat` | Only the binary crosses stages; no Go toolchain or source in the final image. |
| (no `USER` line) | The process runs as root inside the container on purpose; see below. The comment in the Dockerfile records why. |
| `EXPOSE 8080` | Documentation only; under `network_mode: host` it has no effect. |
| `ENTRYPOINT ["/takeyourcoat"]` | Exec form, so the binary is PID 1 and receives SIGTERM directly for graceful shutdown. |

### Why root with one capability

ipset needs `NET_ADMIN`. The compose file sets `cap_drop: [ALL]`,
`cap_add: [NET_ADMIN]` and `no-new-privileges:true`. Under `no_new_privs` the
kernel never adds capabilities on `execve`, so the usual pattern of a non-root
user plus `setcap cap_net_admin+ep` on `/usr/sbin/ipset` cannot work: ipset
would run without the capability and fail with "Operation not permitted".

So the image has no `USER` line. Root in this container holds exactly one
capability. Verified inside the running container: `/proc/self/status` shows
`Uid` 0 with `CapEff` and `CapBnd` both `0000000000001000`
(`CAP_NET_ADMIN` only). ipset, executed by the app, inherits `NET_ADMIN`
directly. Without `CAP_DAC_OVERRIDE` and the rest, this root cannot bypass file
permissions, load modules, change ownership or bind privileged ports.

The trade-off, compared with a working setcap design, is that the Go process
itself holds `NET_ADMIN`, not only the ipset child. That is accepted and
recorded in [decisions](decisions.md#11-root-in-the-container-rather-than-non-root-plus-setcap).
The root filesystem is read-only at run time, so nothing can replace the
ipset binary, and the app only ever runs it with a fixed argv (see
[security design](security-design.md#fixed-argv-exec-no-shell)).

### Image size

The final image is the Alpine base plus `ipset` (and its libraries) and one
stripped static binary: about 18 MB. Check a local build with:

```sh
docker build -t takeyourcoat:dev .
docker image ls takeyourcoat:dev
```

### `.dockerignore`

```text
.git
.github
README.md
PLAN.md
.env*
docker-compose*.yml
*_test.go
```

Keeps secrets (`.env`, the real compose file) and history out of the build
context, and keeps tests out of the image build. The `docs/` directory is not
excluded; it enters the build context but contains no Go files, so it does not
affect the binary.

## Workflows

Both workflows set `permissions: contents: read` at the top level. Every
third-party action is pinned to a full commit SHA with the version in a
trailing comment, and `actions/checkout` runs with
`persist-credentials: false` so the token is not left in `.git/config`.

### `ci.yml`

| | |
|-|-|
| Triggers | `push` to `main`; every `pull_request` |
| Permissions | `contents: read` only |
| Steps | checkout; `setup-go` with `go-version-file: go.mod`; `go vet ./...`; `go test -race ./...`; `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`; `setup-buildx`; `build-push-action` with `push: false` |

The Docker build proves the image builds; nothing is pushed. `govulncheck` is
fetched at `@latest` on every run, the one unpinned tool in CI.

### `release.yml`

| | |
|-|-|
| Triggers | `push` of a tag matching `v*` only. Never `pull_request` or `pull_request_target`, so a fork cannot produce an image. |
| Job `test` | Same vet, race test and govulncheck steps as CI (no Docker build). |
| Job `publish` | `needs: test`. Adds `packages: write` for this job only. checkout; `docker/login-action` to `ghcr.io` with `GITHUB_TOKEN`; `setup-qemu`; `setup-buildx`; `metadata-action`; `build-push-action` for `linux/amd64,linux/arm64` with `push: true` and `provenance: false`. |

Image name: `ghcr.io/${{ github.repository }}`, that is
`ghcr.io/nmbradley/takeyourcoat`. Labels come from `metadata-action`
(including the source commit revision) in addition to the Dockerfile `LABEL`s.

### Dependabot

`.github/dependabot.yml` checks weekly for three ecosystems at `/`:

| Ecosystem | Updates |
|-----------|---------|
| `docker` | The digest-pinned `FROM` lines in the Dockerfile |
| `github-actions` | The SHA pins (and version comments) in both workflows |
| `gomod` | `go.mod` (no dependencies today) |

## Release process

1. Make sure `ci` is green on `main`.
2. Tag and push:

   ```sh
   git tag -a v0.2.0 -m v0.2.0
   git push origin v0.2.0
   ```

3. `release.yml` runs the tests, then builds both architectures and pushes.

For tag `v0.2.0`, `metadata-action` produces:

| Image tag | Source |
|-----------|--------|
| `0.2.0` | `type=semver,pattern={{version}}` (no leading `v`) |
| `0.2` | `type=semver,pattern={{major}}.{{minor}}` |
| `latest` | `type=raw,value=latest` |

Git tags keep the `v`; image tags drop it, so git tag `v0.1.0` is image
`0.1.0`, which is what the repository's `docker-compose.yml` pins
([deployment](../user/deployment.md#pinning-the-image)). Tags that are not
valid semver after the `v` produce no version tags.

To find the digest to give operators:

```sh
docker buildx imagetools inspect ghcr.io/nmbradley/takeyourcoat:0.2.0
```

## Updating pinned digests by hand

Normally Dependabot opens the PRs. To do it yourself:

**Base images.** Resolve the current digest for the tag and replace it in the
`FROM` line, keeping the comment in sync:

```sh
docker buildx imagetools inspect golang:1.27-alpine --format '{{json .Manifest.Digest}}'
docker buildx imagetools inspect alpine:3.23 --format '{{json .Manifest.Digest}}'
```

**Actions.** Resolve the commit for a release tag. For annotated tags use the
`^{}` line, which is the commit the tag points to:

```sh
git ls-remote https://github.com/actions/checkout 'refs/tags/v7.0.1*'
```

Replace the SHA after `uses: owner/repo@` and update the `# vX.Y.Z` comment.
Change the same action in both workflows together.

**Vendored CSS.** Not a digest, but pinned the same way: see
[architecture](architecture.md#embedded-static-file).
