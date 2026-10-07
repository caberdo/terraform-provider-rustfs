default: fmt lint install generate

build:
	go build -v ./...

install: build
	go install -v ./...

lint:
	golangci-lint run

generate:
	cd tools; go generate ./...

fmt:
	gofmt -s -w -e .

test:
	go test -v -cover -timeout=120s -parallel=10 ./...

testacc:
	TF_ACC=1 go test -v -cover -timeout 120m ./...

# Live acceptance tests against a throwaway RustFS server via docker compose.
# Requires docker (or podman) with the compose plugin.
testacc-live:
	docker compose run --rm acc

# Start the RustFS server used by the live acceptance tests and leave it running.
testacc-up:
	docker compose up -d rustfs

testacc-down:
	docker compose down -v

.PHONY: fmt lint test testacc testacc-live testacc-up testacc-down build install generate
