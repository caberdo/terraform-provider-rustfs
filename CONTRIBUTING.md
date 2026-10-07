# Contributing

Thank you for considering contributing to the Terraform Provider for RustFS! This document provides guidelines for contributors.

## Getting Started

### Prerequisites

- [Go](https://golang.org/doc/install) 1.25 or later
- [Docker](https://docs.docker.com/get-docker/) with the Compose plugin (or Podman with `podman compose`)
- [Git](https://git-scm.com)

### First Time Setup

```bash
git clone https://github.com/weinmann-emt/terraform-provider-rustfs.git
cd terraform-provider-rustfs
go mod download
```

## Development

### Building

```bash
go build -o terraform-provider-rustfs
```

### Local Development

Add to `~/.terraformrc`:

```hcl
provider_installation {
  dev_overrides {
    "weinmann-emt/rustfs" = "/path/to/terraform-provider-rustfs"
  }
}
```

### Project Structure

```
├── provider/                # Terraform resource implementations
├── provider/                # Provider definition + resources (legacy flat package)
│   ├── provider.go          # Provider definition and registration
│   ├── rustfs_*_resource.go # Resource CRUD implementations
│   ├── *_test.go            # Schema/metadata unit tests + resource acceptance tests
│   └── provider_test.go     # Resource test infrastructure
├── internal/                # Provider internals
│   ├── acceptance/          # Live acceptance-test harness (LivePreCheck, factories)
│   ├── client/              # RustFS API client library (SigV4 signing)
│   ├── datasources/<name>/  # Data sources (+ acceptance_test.go per data source)
│   └── models/              # Terraform model structs
├── examples/                # Example Terraform configurations
├── docs/                    # Generated documentation
├── scripts/                 # Acceptance-test helper scripts
├── docker-compose.yml       # RustFS + test/acc services
└── .github/                 # CI, templates
```

## Making Changes

### Code Style

- Run `go fmt ./...` before committing
- Run `go vet ./...` to catch issues
- All attributes must have descriptions
- All resources must implement `ResourceWithImportState`
- Sensitive fields must be marked `Sensitive: true`

### Resource Implementation Pattern

```go
var (
    _ resource.Resource                = &ExampleResource{}
    _ resource.ResourceWithImportState = &ExampleResource{}
)

func (r *ExampleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) { ... }
func (r *ExampleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse)     { ... }
func (r *ExampleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) { ... }
func (r *ExampleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) { ... }
func (r *ExampleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) { ... }
```

### Adding New Resources

1. Create API client methods in `internal/client/<name>.go`
2. Create resource file in `provider/rustfs_<name>_resource.go`
3. Register in `provider/provider.go` Resources list
4. Add documentation in `docs/resources/<name>.md`
5. Add example in `examples/resources/<name>/resource.tf`

## Testing

### Unit Tests

```bash
go test ./internal/client/... -v
go test ./provider/... -v -run "^[^T]"
```

### Acceptance Tests

The live suite uses the shared harness in `internal/acceptance` and the RustFS
server in the root `docker-compose.yml`:

```bash
# Bring up RustFS and run the live suite
make testacc-live

# ...or manage the server yourself
make testacc-up
RUSTFS_ENDPOINT="127.0.0.1:9001" \
  RUSTFS_USER="rustfsadmin" RUSTFS_SECRET="rustfsadmin" \
  TF_ACC=1 go test -v ./internal/... ./provider -run "TestAcc"
make testacc-down
```

Run a specific test:

```bash
TF_ACC=1 go test -v ./provider -run "TestAccBucketResource"
```

## Submitting Changes

### Before Submitting

1. Run all unit tests
2. Run acceptance tests locally for new resources
3. Run `go fmt ./...` and `go vet ./...`
4. Add/update documentation

### Pull Request Process

1. One feature or fix per PR
2. Use descriptive title referencing the issue (e.g., "feat: add rustfs_group resource (#50)")
3. Link related issues with "Fixes #123"
4. Include unit tests for new code
5. Include acceptance tests for new resources

## Getting Help

- **GitHub Issues**: Bug reports and feature requests
- **Pull Requests**: Code contributions
- **Discussions**: General questions

## License

By contributing, you agree that your contributions will be licensed under the MPL-2.0 License.
