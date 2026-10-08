# syntax=docker/dockerfile:1
FROM alpine:3.21
ARG TARGETOS
ARG TARGETARCH
# The optional mounted bundle is used only in managed development environments.
RUN --mount=type=secret,id=proxy_ca \
    if [ -s /run/secrets/proxy_ca ]; then SSL_CERT_FILE=/run/secrets/proxy_ca apk add --no-cache ca-certificates tzdata; \
    else apk add --no-cache ca-certificates tzdata; fi
WORKDIR /app
COPY --chmod=755 komari-agent-${TARGETOS}-${TARGETARCH} /app/komari-agent
COPY LICENSE LICENSE.komari-agent NOTICE ATTRIBUTION.md /usr/share/licenses/komari-mcp-bridge/
RUN touch /.komari-agent-container
ENTRYPOINT ["/app/komari-agent"]
CMD ["--help"]
