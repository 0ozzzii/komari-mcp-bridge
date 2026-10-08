# syntax=docker/dockerfile:1
FROM alpine:3.21

WORKDIR /app

# Docker buildx 会在构建时自动填充这些变量
ARG TARGETOS
ARG TARGETARCH

RUN --mount=type=secret,id=proxy_ca \
    if [ -s /run/secrets/proxy_ca ]; then SSL_CERT_FILE=/run/secrets/proxy_ca apk add --no-cache ca-certificates curl tzdata; \
    else apk add --no-cache ca-certificates curl tzdata; fi

COPY --chmod=755 komari-${TARGETOS}-${TARGETARCH} /app/komari
COPY LICENSE LICENSE.komari-agent NOTICE ATTRIBUTION.md /usr/share/licenses/komari-mcp-bridge/

ENV GIN_MODE=release
ENV KOMARI_LISTEN=0.0.0.0:25774
ENV GODEBUG=disablethp=1

EXPOSE 25774

CMD ["/app/komari", "server"]
