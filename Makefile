APP_NAME=lumenvec

.PHONY: test vet build run bench tidy docker-build compose-up compose-down

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

build:
	go build -o $(APP_NAME) ./cmd/server

run:
	go run ./cmd/server

bench:
	go test ./internal/core -bench . -benchmem

docker-build:
	docker build -t $(APP_NAME):latest .

compose-up:
	docker compose up --build

compose-down:
	docker compose down

