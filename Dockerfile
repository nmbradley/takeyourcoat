# golang:1.27-alpine
FROM golang:1.27-alpine@sha256:f92b6ef800e499660581efdabdf25d9d817a9d124eaf900924f0504e7e27e12d AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /takeyourcoat .

# alpine:3.23
FROM alpine:3.23@sha256:85fe1e81d6758c208f3e1eed4338a1997e19d4be002d4dd32d3100c9a8c010a0
LABEL org.opencontainers.image.source="https://github.com/nmbradley/takeyourcoat" \
      org.opencontainers.image.description="Minimal self-hosted Knocknoc replacement: email-verified, time-limited IP allowlisting for anything behind Caddy" \
      org.opencontainers.image.licenses="MIT"
RUN mkdir /data && chown 65532:65532 /data
COPY --from=build /takeyourcoat /takeyourcoat
# Runs unprivileged and writes only to /data.
USER 65532:65532
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/takeyourcoat"]
