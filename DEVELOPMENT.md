# Development Guide

This guide provides comprehensive instructions for developing ClawHermes AI Go.

## Quick Start

### Prerequisites

- Go 1.22+
- Docker & Docker Compose
- Make
- Git

### Setup Development Environment

```bash
# 1. Clone repository
git clone https://github.com/yourusername/ClawHermes-AI-Go.git
cd ClawHermes-AI-Go

# 2. Install dependencies
make install

# 3. Setup pre-commit hooks
pre-commit install

# 4. Start development
make run
```

## Development Workflow

### 1. Create Feature Branch

```bash
git checkout -b feature/your-feature-name
```

### 2. Make Changes

```bash
# Edit code
vim internal/your/file.go

# Format code
make fmt

# Run type checks
make typecheck

# Run linters
make lint
```

### 3. Test Changes

```bash
# Quick local tests
make test-local

# Full test suite
make test-full

# Security scan
make security-scan
```

### 4. Pre-commit Checks

```bash
# Run all pre-commit hooks
make pre-commit

# Or manually
pre-commit run --all-files
```

### 5. Commit & Push

```bash
git add .
git commit -m "feat: add your feature"
git push origin feature/your-feature-name
```

### 6. Create Pull Request

- Push to GitHub
- Create PR with detailed description
- Wait for CI/CD to pass
- Request review from maintainers

## Project Commands Reference

### 5 Core Commands

| Command | Purpose | Duration |
|---------|---------|----------|
| `make install` | Install dependencies | ~30s |
| `make typecheck` | Type checking (go vet) | ~10s |
| `make lint` | Code quality checks | ~30s |
| `make test-local` | Quick unit tests | ~30s |
| `make test-full` | Full test suite + coverage | ~2m |

### Additional Commands

| Command | Purpose |
|---------|---------|
| `make fmt` | Format code |
| `make security-scan` | Security scanning |
| `make pre-commit` | Run pre-commit hooks |
| `make audit` | Audit dependencies |
| `make docs` | Generate documentation |
| `make bench` | Run benchmarks |
| `make check-all` | Run all checks |

## Code Style Guidelines

### Go Code Style

- Follow [Effective Go](https://golang.org/doc/effective_go)
- Use `gofmt` for formatting
- Use `golangci-lint` for linting
- Write tests for all public functions

### Naming Conventions

- Package names: lowercase, single word
- Function names: CamelCase, exported functions start with uppercase
- Variable names: camelCase
- Constants: UPPER_SNAKE_CASE

### Comments

- Write comments for all exported functions
- Use `//` for single-line comments
- Use `/* */` for multi-line comments
- Keep comments concise and meaningful

## Testing Guidelines

### Unit Tests

```go
func TestFunctionName(t *testing.T) {
    // Arrange
    input := "test"
    expected := "result"
    
    // Act
    result := FunctionName(input)
    
    // Assert
    if result != expected {
        t.Errorf("expected %s, got %s", expected, result)
    }
}
```

### Test Coverage

- Aim for >80% coverage
- Test both happy path and error cases
- Use table-driven tests for multiple scenarios

### Running Tests

```bash
# Run all tests
go test ./...

# Run with coverage
go test -cover ./...

# Run specific test
go test -run TestFunctionName ./...

# Run with verbose output
go test -v ./...
```

## Security Guidelines

### Code Security

- Never hardcode secrets (use environment variables)
- Validate all user input
- Use parameterized queries for database operations
- Sanitize output to prevent XSS
- Use HTTPS for all external communications

### Dependency Security

```bash
# Audit dependencies
make audit

# Check for vulnerabilities
go list -json -m all | nancy sleuth
```

### Secret Management

- Use `.env` files for local development (never commit)
- Use environment variables in production
- Rotate secrets regularly
- Never log sensitive information

## Debugging

### Enable Debug Logging

```bash
DEBUG=true make run
```

### Use Delve Debugger

```bash
# Install delve
go install github.com/go-delve/delve/cmd/dlv@latest

# Debug application
dlv debug ./cmd/server
```

### Common Issues

**Issue**: Tests fail locally but pass in CI

- Solution: Check Go version, run `go mod tidy`, clear cache with `go clean -testcache`

**Issue**: Linter errors

- Solution: Run `make fmt` to auto-fix, check `.golangci.yml` for rules

**Issue**: Docker build fails

- Solution: Check Dockerfile, ensure all dependencies are listed

## Performance Optimization

### Profiling

```bash
# CPU profiling
go test -cpuprofile=cpu.prof ./...
go tool pprof cpu.prof

# Memory profiling
go test -memprofile=mem.prof ./...
go tool pprof mem.prof
```

### Benchmarking

```bash
# Run benchmarks
make bench

# Run specific benchmark
go test -bench=BenchmarkFunctionName -benchmem ./...
```

## Documentation

### Generate API Documentation

```bash
make docs
# Visit http://localhost:6060
```

### Update README

- Keep README.md up-to-date
- Include setup instructions
- Document API endpoints
- Add examples

## CI/CD Pipeline

### GitHub Actions Workflow

The project uses GitHub Actions for automated testing and deployment:

1. **Type Check** - Runs `go vet`
2. **Lint** - Runs `golangci-lint`
3. **Security** - Runs Semgrep security scan
4. **Local Tests** - Quick unit tests
5. **Full Tests** - Complete test suite with coverage
6. **Build** - Build Docker image (main branch only)
7. **Quality** - Generate coverage reports

### Viewing CI/CD Status

- Check GitHub Actions tab in repository
- View workflow runs and logs
- Check status badges in README

## Troubleshooting

### Common Problems

**Problem**: `go mod tidy` fails

```bash
# Solution
rm go.sum
go mod download
go mod tidy
```

**Problem**: Pre-commit hooks fail

```bash
# Solution
pre-commit clean
pre-commit install
pre-commit run --all-files
```

**Problem**: Docker build fails

```bash
# Solution
docker system prune
docker build --no-cache -t clawhermes-ai-go:latest .
```

## Getting Help

- Check existing issues on GitHub
- Read project documentation
- Ask in discussions
- Contact maintainers

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution guidelines.

---

**Last Updated**: 2026-05-22
