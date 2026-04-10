FROM golang:1.24 AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/lumenvec ./cmd/server

FROM gcr.io/distroless/static-debian12

WORKDIR /app

COPY --from=builder /out/lumenvec /app/lumenvec
COPY configs/config.yaml /app/configs/config.yaml

EXPOSE 19190

ENTRYPOINT ["/app/lumenvec"]
