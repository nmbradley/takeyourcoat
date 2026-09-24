# golang:1.27-alpine
FROM golang:1.27-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /takeyourcoat .

# alpine:3.23
FROM alpine:3.23@sha256:85fe1e81d6758c208f3e1eed4338a1997e19d4be002d4dd32d3100c9a8c010a0
LABEL org.opencontainers.image.source="https://github.com/nmbradley/takeyourcoat" \
      org.opencontainers.image.description="Captive portal that whitelists a household IPv4 in ipset" \
      org.opencontainers.image.licenses="MIT"
RUN apk add --no-cache ipset
COPY --from=build /takeyourcoat /takeyourcoat
# Runs as root on purpose. The compose file drops every capability except
# NET_ADMIN and sets no-new-privileges. Under that flag the kernel refuses to
# grant capabilities on exec, so a non-root user plus setcap on ipset would
# always fail with "Operation not permitted". Root holds NET_ADMIN directly.
EXPOSE 8080
ENTRYPOINT ["/takeyourcoat"]
