APP_NAME=lumenvec

.PHONY: test vet build run bench tidy coverage docker-build compose-up compose-down compose-validate loadgen release-assets

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

coverage:
	go run ./tools/checkcoverage

docker-build:
	docker build -t $(APP_NAME):latest .

compose-up:
	docker compose up --build

compose-down:
	docker compose down

compose-validate:
	bash scripts/validate-observability.sh

loadgen:
	go run ./tools/loadgen

release-assets:
	bash scripts/package-release.sh
