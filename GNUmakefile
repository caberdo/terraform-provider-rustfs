default: build

BINARY_NAME=terraform-provider-rustfs
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

build:
	go build -o bin/$(BINARY_NAME) -ldflags "-X main.version=$(VERSION)" .

test:
	go test -v -cover ./...

testacc:
	TF_ACC=1 go test -v -cover ./...

lint:
	golangci-lint run --config .github/golangci.yml ./...

lint-fix:
	golangci-lint run --config .github/golangci.yml --fix ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

install: build
	mkdir -p ~/.terraform.d/plugins/registry.terraform.io/caberdo/rustfs/$(VERSION)/$$(go env GOOS)_$$(go env GOARCH)
	cp bin/$(BINARY_NAME) ~/.terraform.d/plugins/registry.terraform.io/caberdo/rustfs/$(VERSION)/$$(go env GOOS)_$$(go env GOARCH)/$(BINARY_NAME)

testacc-docker:
	docker compose -f acc_test/docker-compose.yml --profile test run --rm test

testacc-live:
	podman-compose -f acc_test/docker-compose.yml up -d rustfs
	RUSTFS_ENDPOINT=127.0.0.1:9001 RUSTFS_USER=rustfsadmin RUSTFS_SECRET=rustfsadmin TF_ACC=1 go test -v ./...

validate-examples:
	./scripts/validate-examples.sh

generate:
	go generate ./...

.PHONY: build test testacc lint lint-fix fmt vet install testacc-docker testacc-live validate-examples generate
