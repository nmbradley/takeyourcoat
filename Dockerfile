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
RUN apk add --no-cache ipset libcap \
 && setcap cap_net_admin+ep /usr/sbin/ipset \
 && adduser -D -H -u 65532 -s /sbin/nologin tyc
COPY --from=build /takeyourcoat /takeyourcoat
USER tyc
EXPOSE 8080
ENTRYPOINT ["/takeyourcoat"]
