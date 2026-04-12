FROM golang:1.26 AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/lumenvec ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot

LABEL org.opencontainers.image.title="LumenVec" \
      org.opencontainers.image.description="Vector database with HTTP and gRPC transports" \
      org.opencontainers.image.licenses="MIT"

WORKDIR /app

COPY --from=builder --chown=nonroot:nonroot /out/lumenvec /app/lumenvec
COPY --chown=nonroot:nonroot configs/config.yaml /app/configs/config.yaml
COPY --chown=nonroot:nonroot configs/config.grpc.yaml /app/configs/config.grpc.yaml

EXPOSE 19190
EXPOSE 19191

VOLUME ["/data"]

ENV VECTOR_DB_CONFIG=/app/configs/config.yaml \
    VECTOR_DB_PROTOCOL=http \
    VECTOR_DB_PORT=19190 \
    VECTOR_DB_GRPC_PORT=19191 \
    VECTOR_DB_SNAPSHOT_PATH=/data/snapshot.json \
    VECTOR_DB_WAL_PATH=/data/wal.log \
    VECTOR_DB_VECTOR_PATH=/data/vectors

USER nonroot:nonroot

ENTRYPOINT ["/app/lumenvec"]
