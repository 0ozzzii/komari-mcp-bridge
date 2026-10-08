# syntax=docker/dockerfile:1
FROM alpine:3.21
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=secret,id=proxy_ca \
    if [ -s /run/secrets/proxy_ca ]; then SSL_CERT_FILE=/run/secrets/proxy_ca apk add --no-cache ca-certificates; \
    else apk add --no-cache ca-certificates; fi
COPY --chmod=755 komari-mcp-${TARGETOS}-${TARGETARCH} /komari-mcp
COPY LICENSE LICENSE.komari-agent NOTICE ATTRIBUTION.md /usr/share/licenses/komari-mcp-bridge/
RUN mkdir -m 0700 /state && chown 65532:65532 /state
USER 65532:65532
ENV BRIDGE_STATE_DIR=/state
EXPOSE 8967 8968
ENTRYPOINT ["/komari-mcp"]
CMD ["serve"]
