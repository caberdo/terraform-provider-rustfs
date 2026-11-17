default: build

BINARY_NAME=terraform-provider-rustfs
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

# Duration of each target's run in `make fuzz`; the weekly Fuzz workflow uses 60s.
FUZZTIME ?= 30s
# The shared tooling for the caberdo providers: the coverage gate, merge and
# README targets. Keep this pin equal to the `uses:` refs in
# .github/workflows/*.yml: one version for the local targets and the reusable
# workflows, so they cannot drift apart.
TFKIT ?= go run github.com/caberdo/terraform-provider-kit/cmd/tfkit@v0.15.0

build:
	go build -o bin/$(BINARY_NAME) -ldflags "-X main.version=$(VERSION)" .

test:
	go test -v -cover ./...

coverage:
	go test -count=1 -skip '^TestAcc' -covermode=atomic -coverprofile=coverage.out ./...
	@echo
	@go tool cover -func=coverage.out

coverage-html: coverage
	go tool cover -html=coverage.out

# Unit coverage (coverage.out) plus the acceptance profile written by
# acc_test/docker-compose.yml (coverage-acceptance.out) as one number.
coverage-merge:
	$(TFKIT) coverage merge --output coverage-union.out coverage.out coverage-acceptance.out
	@echo
	@go tool cover -func=coverage-union.out

# Fail when coverage-union.out is below .github/coverage-baseline.txt. CI runs
# the same tfkit command on every pull request and on main.
coverage-gate:
	$(TFKIT) coverage gate --profile coverage-union.out --packages ./...

# Regenerate the coverage badge and block in README.md from the baseline;
# `make coverage-readme-check` is what CI runs to fail on drift.
coverage-readme:
	$(TFKIT) coverage readme

coverage-readme-check:
	$(TFKIT) coverage readme --check

# Short fuzz run of every target. `-fuzz` takes exactly one target, so each is
# listed explicitly; the weekly Fuzz workflow runs the same commands (60s each).
# `go test ./...` replays the committed seed corpus, so no target needs fuzzing
# to stay green — use this for real campaigns: `make fuzz FUZZTIME=10m`.
fuzz:
	go test -run '^$$' -fuzz '^FuzzPolicyDocument$$' -fuzztime $(FUZZTIME) ./pkg/rustfs
	go test -run '^$$' -fuzz '^FuzzAdminErrorBody$$' -fuzztime $(FUZZTIME) ./pkg/rustfs
	go test -run '^$$' -fuzz '^FuzzConfigKV$$' -fuzztime $(FUZZTIME) ./pkg/rustfs
	go test -run '^$$' -fuzz '^FuzzTargetConfig$$' -fuzztime $(FUZZTIME) ./provider

testacc:
	TF_ACC=1 go test -v -cover ./...

lint:
	golangci-lint run --config .golangci.yml ./...

lint-fix:
	golangci-lint run --config .golangci.yml --fix ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

install: build
	mkdir -p ~/.terraform.d/plugins/registry.terraform.io/caberdo/rustfs/$(VERSION)/$$(go env GOOS)_$$(go env GOARCH)
	cp bin/$(BINARY_NAME) ~/.terraform.d/plugins/registry.terraform.io/caberdo/rustfs/$(VERSION)/$$(go env GOOS)_$$(go env GOARCH)/$(BINARY_NAME)

testacc-docker:
	docker compose -f acc_test/docker-compose.yml --profile test run --rm test

# The acceptance credentials belong to the deployment, not to the repository:
# export RUSTFS_USER/RUSTFS_SECRET (the primary server) and
# RUSTFS_PEER_ACCESS_KEY/RUSTFS_PEER_SECRET_KEY (the peer) before running this
# target, with the same values the compose fixture gives its services.
testacc-live:
	podman-compose -f acc_test/docker-compose.yml up -d rustfs rustfs-peer rustfs-tls
	RUSTFS_ENDPOINT=127.0.0.1:9001 \
	RUSTFS_USER="$${RUSTFS_USER:?RUSTFS_USER must be set (the acceptance fixture's access key)}" \
	RUSTFS_SECRET="$${RUSTFS_SECRET:?RUSTFS_SECRET must be set (the acceptance fixture's secret key)}" \
	RUSTFS_NOTIFY_TEST_ENDPOINT=http://127.0.0.1:9001/health/ready \
	RUSTFS_PEER_ENDPOINT=rustfs-peer:9000 RUSTFS_PEER_ADMIN_ENDPOINT=127.0.0.1:9003 \
	RUSTFS_PEER_S3_ENDPOINT=127.0.0.1:9002 \
	RUSTFS_PEER_ACCESS_KEY="$${RUSTFS_PEER_ACCESS_KEY:?RUSTFS_PEER_ACCESS_KEY must be set}" \
	RUSTFS_PEER_SECRET_KEY="$${RUSTFS_PEER_SECRET_KEY:?RUSTFS_PEER_SECRET_KEY must be set}" \
	RUSTFS_TLS_ENDPOINT=127.0.0.1:9005 \
	TF_ACC=1 go test -v ./...

validate-examples:
	./scripts/validate-examples.sh

generate:
	go generate ./...

# Regenerate the resource and data source tables in AGENTS.md and README.md
# from the provider registry and the generated docs pages. Idempotent.
generate-tables:
	go run ./scripts/gen-tables.go

# Turn changelog.d/*.md fragments into a dated CHANGELOG.md section and remove
# the fragments. Refuses to reuse a version and refuses malformed filenames.
changelog-release:
	@case "$(VERSION)" in [0-9]*.[0-9]*.[0-9]*) ;; *) echo "usage: make changelog-release VERSION=x.y.z" >&2; exit 2 ;; esac
	./scripts/changelog-release.sh "$(VERSION)"

.PHONY: build test coverage coverage-html coverage-merge coverage-gate coverage-readme coverage-readme-check testacc lint lint-fix fmt vet install testacc-docker testacc-live validate-examples generate generate-tables changelog-release fuzz
