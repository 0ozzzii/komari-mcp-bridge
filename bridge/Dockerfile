FROM golang:1.25.0 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /komari-mcp ./cmd/komari-mcp && mkdir -m 0700 /state

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /komari-mcp /komari-mcp
COPY --from=build --chown=65532:65532 /state /state
ENV BRIDGE_STATE_DIR=/state
EXPOSE 8967 8968
ENTRYPOINT ["/komari-mcp"]
CMD ["serve"]
