# Contributing to ClawHermes AI Go

Thank you for your interest in contributing! This document provides guidelines and instructions for contributing to the project.

## Code of Conduct

- Be respectful and inclusive
- Provide constructive feedback
- Focus on the code, not the person
- Help others learn and grow

## Getting Started

### 1. Fork & Clone

```bash
# Fork on GitHub, then clone your fork
git clone https://github.com/YOUR_USERNAME/ClawHermes-AI-Go.git
cd ClawHermes-AI-Go

# Add upstream remote
git remote add upstream https://github.com/ORIGINAL_OWNER/ClawHermes-AI-Go.git
```

### 2. Create Feature Branch

```bash
# Update main branch
git fetch upstream
git checkout main
git merge upstream/main

# Create feature branch
git checkout -b feature/your-feature-name
```

### 3. Make Changes

```bash
# Follow development guidelines
# See DEVELOPMENT.md for detailed instructions
```

## Commit Guidelines

### Commit Message Format

```
<type>(<scope>): <subject>

<body>

<footer>
```

### Types

- **feat**: New feature
- **fix**: Bug fix
- **docs**: Documentation
- **style**: Code style changes
- **refactor**: Code refactoring
- **perf**: Performance improvement
- **test**: Test additions/changes
- **chore**: Build/dependency changes

### Examples

```
feat(agent): add ReAct agent implementation

Implement ReAct (Reasoning + Acting) agent pattern
with support for multi-step reasoning and tool use.

Closes #123
```

```
fix(memory): resolve memory leak in vector store

Fix memory leak caused by unclosed connections
in Milvus vector store client.

Fixes #456
```

## Pull Request Process

### Before Submitting PR

1. **Run all checks**

   ```bash
   make check-all
   ```

2. **Update documentation**
   - Update README if needed
   - Add/update code comments
   - Update CHANGELOG

3. **Add tests**
   - Add unit tests for new features
   - Ensure >80% coverage
   - Test edge cases

4. **Verify CI/CD**
   - All GitHub Actions must pass
   - No security warnings
   - Coverage maintained or improved

### PR Description Template

```markdown
## Description
Brief description of changes

## Type of Change
- [ ] Bug fix
- [ ] New feature
- [ ] Breaking change
- [ ] Documentation update

## Related Issues
Closes #(issue number)

## Testing
- [ ] Unit tests added
- [ ] Integration tests added
- [ ] Manual testing completed

## Checklist
- [ ] Code follows style guidelines
- [ ] Self-review completed
- [ ] Comments added for complex logic
- [ ] Documentation updated
- [ ] No new warnings generated
- [ ] Tests pass locally
- [ ] Coverage maintained
```

## Code Review Process

### What Reviewers Look For

- Code quality and style
- Test coverage
- Security implications
- Performance impact
- Documentation completeness

### Responding to Feedback

- Address all comments
- Ask for clarification if needed
- Push new commits (don't force push)
- Mark conversations as resolved

## Project Standards

### Code Quality

- Follow Go best practices
- Use `gofmt` for formatting
- Pass `golangci-lint` checks
- Maintain >80% test coverage

### Security

- No hardcoded secrets
- Input validation required
- Use secure libraries
- Pass security scans

### Documentation

- Document public APIs
- Add examples where helpful
- Update README for new features
- Keep CHANGELOG updated

## Testing Requirements

### Unit Tests

- Required for all new features
- Required for bug fixes
- Use table-driven tests
- Test error cases

### Integration Tests

- For features involving multiple components
- For database operations
- For external service calls

### Running Tests

```bash
# Local tests
make test-local

# Full test suite
make test-full

# Specific test
go test -run TestName ./...
```

## Documentation Requirements

### Code Comments

```go
// Package agent provides AI agent implementations
package agent

// Agent defines the interface for AI agents
type Agent interface {
    Execute(ctx context.Context, input string) (string, error)
}
```

### Function Documentation

```go
// NewAgent creates a new agent with the given configuration
func NewAgent(config *Config) (Agent, error) {
    // implementation
}
```

### README Updates

- Add new features to feature list
- Update API documentation
- Add usage examples
- Update architecture diagram if needed

## Release Process

### Version Numbering

We follow [Semantic Versioning](https://semver.org/):

- MAJOR: Breaking changes
- MINOR: New features (backward compatible)
- PATCH: Bug fixes

### Release Checklist

- [ ] Update version in code
- [ ] Update CHANGELOG
- [ ] Create git tag
- [ ] Push to main
- [ ] Create GitHub release
- [ ] Build and push Docker image

## Areas for Contribution

### High Priority

- [ ] Performance optimization
- [ ] Security improvements
- [ ] Documentation
- [ ] Bug fixes

### Medium Priority

- [ ] New features
- [ ] Test coverage
- [ ] Code refactoring
- [ ] Examples

### Low Priority

- [ ] Code style improvements
- [ ] Comment updates
- [ ] Minor documentation

## Getting Help

### Questions?

- Check existing issues and discussions
- Read DEVELOPMENT.md
- Ask in GitHub discussions
- Contact maintainers

### Found a Bug?

1. Check if already reported
2. Create detailed bug report
3. Include reproduction steps
4. Provide environment info

### Feature Request?

1. Check if already requested
2. Describe use case
3. Provide examples
4. Discuss implementation approach

## Recognition

Contributors will be recognized in:

- CONTRIBUTORS.md file
- Release notes
- GitHub contributors page

## License

By contributing, you agree that your contributions will be licensed under the same license as the project.

---

**Thank you for contributing to ClawHermes AI Go!**

**Last Updated**: 2026-05-22
