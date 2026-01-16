# Contributing to Go-MLS

Thank you for your interest in contributing to Go-MLS!

## Development Setup

### Prerequisites
- Go 1.24+
- FFmpeg installed and in PATH
- golangci-lint (optional, for local linting)

### Getting Started

```bash
git clone https://github.com/krsna/go-mls.git
cd go-mls
go mod download
go build -o go-mls
./go-mls
```

### Running Tests

```bash
# Run all tests
go test ./...

# Run with race detector
go test -race ./...

# Run integration tests only
go test -v ./test/integration/...

# Run with coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### Linting

```bash
# Install golangci-lint
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# Run linter
golangci-lint run
```

## Code Style

- Use `gofumpt` for formatting (stricter than `gofmt`)
- Follow effective Go guidelines
- Add comments for exported functions and types
- Use structured logging with the `logger` package

## Pull Request Process

1. Fork the repository and create a feature branch
2. Ensure tests pass: `go test -race ./...`
3. Ensure linter passes: `golangci-lint run`
4. Update documentation if needed
5. Submit PR with clear description

## Architecture

See [docs/architecture-overview.md](docs/architecture-overview.md) for system design.

Key packages:
- `internal/stream/` - Core streaming logic
- `internal/api/` - HTTP handlers
- `internal/app/` - Dependency injection
- `internal/config/` - Configuration

## License

By contributing, you agree that your contributions will be licensed under the same license as the project.
