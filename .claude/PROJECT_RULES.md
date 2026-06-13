# Stratum Project Development Standards

This document defines development standards that are automatically enforced through hooks and permission rules in `.claude/settings.json`. No manual intervention required.

## 📋 5 Project Commands

All commands are implemented in `Makefile` and support quick verification and comprehensive testing:

### 1. Install Dependencies (`make install`)

```bash
make install
```

- Download Go module dependencies
- Organize go.mod and go.sum
- Verify dependency integrity

### 2. Type Checking (`make typecheck`)

```bash
make typecheck
```

- Execute `go vet` for static analysis
- Check type safety
- Detect potential programming errors

### 3. Lint Checking (`make lint`)

```bash
make lint
```

- Run `golangci-lint` for code quality checks
- Verify code style and best practices
- 5-minute timeout

### 4. Local Testing (`make test-local`)

```bash
make test-local
```

- Execute quick unit tests (`-short` flag)
- 30-second timeout
- For rapid feedback loops

### 5. Full Testing (`make test-full`)

```bash
make test-full
```

- Execute complete test suite (includes race detection)
- Generate coverage reports
- 5-minute timeout
- Display coverage statistics

---

## 🚫 5 Project Boundaries

Automatically enforced through hooks and sandbox configuration in `.claude/settings.json`:

### Boundary 1: Forbidden Commands

**Trigger**: `PreToolUse` hook on `Bash`

Blocks dangerous commands:

- `rm -rf` - Recursive deletion
- `sudo` - Privilege escalation
- `chmod 777` - Permission modification
- `chown` - Ownership modification
- `dd if=` - Disk operations
- `mkfs` - Filesystem formatting
- `fdisk` - Disk partitioning

**Implementation**: Pre-execution check, blocks matching commands with warning

### Boundary 2: Protected System Directories

**Trigger**: `PreToolUse` hook on `Write|Edit`

Prevents modification of critical system directories:

- `/etc` - System configuration
- `/sys` - System interface
- `/proc` - Process information
- `/dev` - Device files
- `/root` - Root home directory
- `/var/log` - System logs
- `/boot` - Boot files
- `/usr/bin` - System binaries
- `/usr/sbin` - System administration tools

**Implementation**: Pre-write path check, blocks matching paths

### Boundary 3: Protected Sensitive Files

**Trigger**: `PreToolUse` hook on `Read`

Prevents reading sensitive credential files:

- `/root/.ssh` - SSH keys
- `/root/.aws` - AWS credentials
- `/root/.kube` - Kubernetes configuration
- `/etc/shadow` - Password hashes
- `/etc/passwd` - User information
- `/.aws/credentials` - AWS credentials

**Implementation**: Pre-read path check, blocks matching files

### Boundary 4: Data Dry-run Mode

**Trigger**: `PostToolUse` hook on `Bash`

Detects and encourages safe mode flags:

- `--dry-run` - Simulate execution
- `--dry_run` - Simulate execution (underscore)
- `-n` - No execution
- `--no-op` - No operation

**Implementation**: Post-execution check, confirms if safe flags used

### Boundary 5: Test Execution Reporting

**Trigger**: `PostToolUse` hook on `Bash`

Validates project command execution:

- Checks if one of the 5 project commands was executed
- Displays confirmation on successful execution
- Records command execution history

**Implementation**: Post-execution command name check, displays success message

---

## 🔒 Permission Rules

### Allowed Operations

```json
"allow": [
  "Bash(go *)",           // Go commands
  "Bash(make *)",         // Make commands
  "Bash(docker *)",       // Docker commands
  "Bash(git *)",          // Git commands
  "Bash(npm *)",          // NPM commands
  "Bash(curl *)",         // HTTP requests
  "Bash(wget *)",         // File downloads
  "Read",                 // Read files
  "Edit",                 // Edit files
  "Write"                 // Write files
]
```

### Operations Requiring Confirmation

```json
"ask": [
  "Bash(docker-compose down)",  // Stop containers
  "Bash(git push *)",           // Push code
  "Bash(git reset --hard)",     // Hard reset
  "Bash(go mod tidy)",          // Organize dependencies
  "Bash(make clean)"            // Clean build
]
```

---

## 🛡️ Sandbox Configuration

### Protected Write Directories

System critical directories are protected by sandbox to prevent accidental modifications.

### Protected Read Files

Sensitive credential files are protected by sandbox to prevent leakage.

---

## 📝 Usage Examples

### Normal Workflow

```bash
# 1. Install dependencies
make install

# 2. Type checking
make typecheck

# 3. Code checking
make lint

# 4. Quick testing
make test-local

# 5. Full testing
make test-full
```

### Blocked Operations

```bash
# ❌ Blocked: Dangerous command
rm -rf /

# ❌ Blocked: Modify system file
echo "hack" > /etc/passwd

# ❌ Blocked: Read sensitive file
cat /root/.ssh/id_rsa

# ✅ Allowed: Safe operation
make test-full --dry-run
```

---

## 🔄 Automatic Enforcement

All standards are automatically enforced through:

1. **SessionStart Hook** - Load standards on session start
2. **PreToolUse Hooks** - Validate before command execution
3. **PostToolUse Hooks** - Confirm after command execution
4. **Sandbox Configuration** - Filesystem-level protection
5. **Permission Rules** - Operation-level control

All checks are automatic, no manual configuration needed.

---

## 📊 Standards Coverage Matrix

| Standard Type | Implementation | Trigger | Enforcement Level |
|--------------|----------------|---------|------------------|
| 5 Commands | Makefile + Hook | Command execution | Validation |
| Forbidden Commands | PreToolUse Hook | Before execution | Block |
| Protected Directories | PreToolUse Hook | Before write | Block |
| Protected Files | PreToolUse Hook | Before read | Block |
| Dry-run Mode | PostToolUse Hook | After execution | Prompt |
| Test Reporting | PostToolUse Hook | After execution | Record |

---

**Last Updated**: 2026-05-22
**Configuration File**: `.claude/settings.json`
**Makefile**: `Makefile`
