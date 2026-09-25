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
RUN mkdir /data && chown 65532:65532 /data
COPY --from=build /takeyourcoat /takeyourcoat
# Runs unprivileged and writes only to /data.
USER 65532:65532
VOLUME /data
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
| `alpine:3.23@sha256:...` | Small base. Pinned by digest. Nothing is installed on top of it. |
| `LABEL org.opencontainers.image.*` | Source repository, description and licence, so GHCR links the package to the repo. |
| `RUN mkdir /data && chown 65532:65532 /data` | The one writable directory, owned by the runtime user. A new named volume mounted at `/data` copies this ownership on first use, so the app can write its state file without any setup on the host. |
| `COPY --from=build /takeyourcoat /takeyourcoat` | Only the binary crosses stages; no Go toolchain or source in the final image. |
| `USER 65532:65532` | Unprivileged. 65532 is the conventional "nonroot" UID, not used by anything in Alpine. The compose file needs no `user:` line. |
| `VOLUME /data` | Marks the state directory, so even a bare `docker run` gets a volume there and `--read-only` still leaves it writable. |
| `EXPOSE 8080` | Documentation only. The compose file publishes no port for the app; Caddy reaches it on the compose network. |
| `ENTRYPOINT ["/takeyourcoat"]` | Exec form, so the binary is PID 1 and receives SIGTERM directly for graceful shutdown. |

### Why no privilege at all

v0.1 needed `NET_ADMIN` in the host network namespace to run `ipset`, and ran
as root because `no-new-privileges` blocks file capabilities on exec. v0.2
enforces in the app through Caddy `forward_auth`
([decisions](decisions.md#12-enforce-in-the-app-via-caddy-forward_auth-instead-of-ipsetiptables)),
so the process only binds port 8080 (above 1024), makes outbound SMTP
connections and writes files in a directory it owns. None of that needs a
capability, so the compose file runs it with `cap_drop: [ALL]`, nothing added,
`no-new-privileges`, `read_only: true` and no host networking.

### Compose file and Caddyfile

The repository's `docker-compose.yml` now has two services, because Caddy is
part of the design:

| Piece | Role |
|-------|------|
| `caddy` (`caddy:2@sha256:...`, version in a comment) | Fixed address `172.28.0.10` on `web`; publishes 80, 443 and 443/udp; mounts `./Caddyfile` read-only; keeps certificates on `caddy-data` and config on `caddy-config`. |
| `takeyourcoat` | No published port; `TYC_LISTEN: 0.0.0.0:8080`; `TYC_TRUSTED_PROXIES: 172.28.0.10` (Caddy only; the /24 would include the host's gateway `172.28.0.1`); state on `tyc-data:/data`. |
| `networks.web` | Bridge network pinned to `172.28.0.0/24`, so Caddy's fixed address is valid and known in advance. |

`Caddyfile` (placeholders) has two sites: `hello.example.com` proxies to
`takeyourcoat:8080`; `app.example.com` runs
`forward_auth takeyourcoat:8080 { uri /check }` and then proxies to the
backend, `10.0.0.2:8080`. Operators edit the hostnames and the backend address
(the address and port their backend listens on). Both
files are validated without starting anything:

```sh
docker compose -f docker-compose.yml config
docker run --rm -v "$PWD/Caddyfile:/etc/caddy/Caddyfile:ro" caddy:2 caddy validate --config /etc/caddy/Caddyfile
```

### Image size

The final image is the Alpine base plus one stripped static binary, a few MB
smaller than v0.1, which also carried `ipset` and its libraries. Check a
local build with:

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
Caddyfile
docs/
*_test.go
config.json
*.local.*
```

Keeps secrets and deployment details (`.env`, `config.json`, `*.local.*` files, the
real compose file, the Caddyfile with real hostnames) and history out of the build context, keeps
tests out of the image build, and leaves out `docs/`, which the binary never
needed.

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
| Steps | checkout; `setup-go` with `go-version-file: go.mod`; `go vet ./...`; `go test -race ./...`; `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...`; `setup-buildx`; `build-push-action` with `push: false` |

The Docker build proves the image builds; nothing is pushed. `govulncheck` is
pinned to `v1.8.0`; bump it by hand in both workflows (Dependabot does not see
`go run` versions).

### `release.yml`

| | |
|-|-|
| Triggers | `push` of a tag matching `v*` only. Never `pull_request` or `pull_request_target`, so a fork cannot produce an image. |
| Job `test` | Same vet, race test and govulncheck (`@v1.8.0`) steps as CI (no Docker build). |
| Job `publish` | `needs: test`. Adds `packages: write` for this job only. checkout; `docker/login-action` to `ghcr.io` with `GITHUB_TOKEN`; `setup-qemu`; `setup-buildx`; `metadata-action`; `build-push-action` for `linux/amd64,linux/arm64` with `push: true` and `provenance: false`. |

Image name: `ghcr.io/${{ github.repository }}`, that is
`ghcr.io/nmbradley/takeyourcoat`. Labels come from `metadata-action`
(including the source commit revision) in addition to the Dockerfile `LABEL`s.

### Dependabot

`.github/dependabot.yml` checks weekly for four ecosystems at `/`:

| Ecosystem | Updates |
|-----------|---------|
| `docker` | The digest-pinned `FROM` lines in the Dockerfile |
| `docker-compose` | The digest-pinned `caddy:2@sha256:...` image in `docker-compose.yml` |
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

No `latest` tag is published, so nothing can pull a moving "newest" image by
accident.

Git tags keep the `v`; image tags drop it, so git tag `v0.2.0` is image
`0.2.0`, which is what the repository's `docker-compose.yml` pins
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
