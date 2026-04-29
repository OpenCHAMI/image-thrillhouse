# Go Testing Guide for Image-Build

## Overview

Go has **built-in testing** that's much simpler than Python's unittest/pytest setup. No external dependencies needed!

## Running Tests

### Run all tests
```bash
cd /Users/smehta/image-builder/image-build
go test ./...
```

### Run tests in a specific package
```bash
go test ./internal/config
go test ./internal/labels
```

### Run with verbose output
```bash
go test -v ./...
```

### Run with coverage
```bash
go test -cover ./...
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out  # View in browser
```

### Run specific test
```bash
go test -run TestLoadConfig ./internal/config
go test -run TestGenerate ./internal/labels
```

### Run tests in parallel (default)
```bash
go test -parallel 4 ./...
```

---

## Test File Structure

### Naming Convention
- Test files: `*_test.go` (e.g., `config_test.go`)
- Test functions: `Test*` (e.g., `TestLoadConfig`)
- Must be in same package as code being tested

### Basic Test Structure
```go
package mypackage

import "testing"

func TestMyFunction(t *testing.T) {
    // Arrange
    input := "test"
    
    // Act
    result := MyFunction(input)
    
    // Assert
    if result != "expected" {
        t.Errorf("Expected 'expected', got '%s'", result)
    }
}
```

---

## Test Patterns Used

### 1. Table-Driven Tests (Most Common)
```go
func TestValidate(t *testing.T) {
    tests := []struct {
        name    string
        input   MyType
        wantErr bool
    }{
        {
            name:    "valid input",
            input:   MyType{Field: "value"},
            wantErr: false,
        },
        {
            name:    "invalid input",
            input:   MyType{},
            wantErr: true,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := tt.input.Validate()
            if (err != nil) != tt.wantErr {
                t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}
```

**Why use this?**
- Tests multiple cases with same logic
- Easy to add new test cases
- Clear test names with `t.Run()`

### 2. Subtests with t.Run()
```go
func TestFeature(t *testing.T) {
    t.Run("scenario 1", func(t *testing.T) {
        // Test scenario 1
    })
    
    t.Run("scenario 2", func(t *testing.T) {
        // Test scenario 2
    })
}
```

**Why use this?**
- Organize related tests
- Run specific subtests: `go test -run TestFeature/scenario1`

### 3. Temporary Directories/Files
```go
func TestWithFile(t *testing.T) {
    // Creates temporary directory, auto-cleaned up
    tmpDir := t.TempDir()
    
    filePath := filepath.Join(tmpDir, "test.txt")
    // ... use file ...
    
    // No manual cleanup needed!
}
```

---

## Current Test Coverage

### ✅ `internal/config/config_test.go`

**Tests 10+ scenarios:**
- Loading valid config files
- Handling missing files
- Invalid YAML parsing
- Meta validation (name, tag, from)
- Layer validation (manager types)
- File validation (content/src/url)
- Module validation (actions)
- Command type detection

**Run:**
```bash
go test ./internal/config -v
```

### ✅ `internal/labels/labels_test.go`

**Tests 12+ scenarios:**
- Basic label generation
- Labels with packages
- Labels with package groups
- Labels with DNF modules
- Labels with repositories
- Custom label override
- Build date format (RFC3339)
- Empty config handling
- Repository name extraction

**Run:**
```bash
go test ./internal/labels -v
```

### ✅ `internal/backend/apt/apt_test.go`

**Tests 25+ scenarios:**
- Backend creation with options
- Default option values
- Install command structure
- Option flag generation (--no-install-recommends, etc.)
- APT-specific features (update command, quiet mode)
- Groups and modules (ignored with warnings)
- Option validation (unknown options, invalid values)
- Parent install support
- InstallRoot not supported

**Run:**
```bash
go test ./internal/backend/apt -v
```

### ✅ `internal/backend/dnf/dnf_test.go`

**Tests 20+ scenarios:**
- Backend creation
- Package installation commands
- Group installation
- Module operations (enable, install, disable)
- InstallRoot commands for scratch builds
- Command structure validation
- Option validation
- Flag generation

**Run:**
```bash
go test ./internal/backend/dnf -v
```

### ✅ `internal/backend/zypper/zypper_test.go`

**Tests 15+ scenarios:**
- Backend creation with repopath
- Package and pattern (group) installation
- InstallRoot commands
- Command structure
- Option validation
- GPG key handling

**Run:**
```bash
go test ./internal/backend/zypper -v
```

### ✅ `internal/backend/mmdebstrap/mmdebstrap_test.go`

**Tests 15+ scenarios:**
- Backend creation with required options
- Default values (variant=minbase, mode=fakechroot)
- Custom options parsing
- InstallRoot command generation
- Package list formatting (--include=pkg1,pkg2)
- Command argument order
- Required option validation (suite, mirror)
- Parent install not supported

**Run:**
```bash
go test ./internal/backend/mmdebstrap -v
```

---

## Example Test Output

```bash
$ go test ./... -v

=== RUN   TestLoadConfig
--- PASS: TestLoadConfig (0.00s)
=== RUN   TestValidateMeta
=== RUN   TestValidateMeta/valid_meta
=== RUN   TestValidateMeta/missing_name
--- PASS: TestValidateMeta (0.00s)
PASS
ok      github.com/travisbcotton/image-build/internal/config    0.123s

=== RUN   TestGenerate
--- PASS: TestGenerate (0.00s)
=== RUN   TestGenerateWithPackages
--- PASS: TestGenerateWithPackages (0.00s)
PASS
ok      github.com/travisbcotton/image-build/internal/labels    0.089s

=== RUN   TestNewWithOptions
=== RUN   TestNewWithOptions/nil_options
=== RUN   TestNewWithOptions/install-recommends_true
--- PASS: TestNewWithOptions (0.00s)
=== RUN   TestValidateOptions
=== RUN   TestValidateOptions/valid_options
=== RUN   TestValidateOptions/invalid_option_name
--- PASS: TestValidateOptions (0.00s)
PASS
ok      github.com/travisbcotton/image-build/internal/backend/apt    0.056s

=== RUN   TestInstallCommands
--- PASS: TestInstallCommands (0.00s)
=== RUN   TestValidateOptions
--- PASS: TestValidateOptions (0.00s)
PASS
ok      github.com/travisbcotton/image-build/internal/backend/dnf    0.047s

=== RUN   TestValidateOptions
--- PASS: TestValidateOptions (0.00s)
PASS
ok      github.com/travisbcotton/image-build/internal/backend/zypper    0.041s

=== RUN   TestNewWithDefaults
--- PASS: TestNewWithDefaults (0.00s)
=== RUN   TestValidateOptions
--- PASS: TestValidateOptions (0.00s)
PASS
ok      github.com/travisbcotton/image-build/internal/backend/mmdebstrap    0.038s
```

---

## Testing Best Practices

### ✅ DO:
- Use table-driven tests for multiple scenarios
- Use `t.TempDir()` for file operations
- Use descriptive test names
- Test both success and error cases
- Use `t.Helper()` for helper functions
- Keep tests independent

### ❌ DON'T:
- Don't use global state
- Don't rely on test execution order
- Don't make external network calls (mock instead)
- Don't test implementation details
- Don't skip cleanup

---

## Adding More Tests

### For Backend (Package Managers)

Backend tests should cover:
- Option parsing and defaults
- Command generation (install, installroot)
- Flag generation based on options
- Option validation
- Capability flags (SupportsInstallRoot, SupportsParentInstall)

Example structure:
```bash
# Create test file
touch internal/backend/mybackend/mybackend_test.go
```

```go
package mybackend

import "testing"

func TestNew(t *testing.T) {
    backend := New(nil)
    if backend == nil {
        t.Fatal("New() returned nil")
    }
}

func TestNewWithOptions(t *testing.T) {
    tests := []struct {
        name    string
        options map[string]string
        want    MyBackend
    }{
        {
            name:    "default options",
            options: nil,
            want:    MyBackend{optionA: false},
        },
        {
            name: "with options",
            options: map[string]string{
                "option-a": "true",
            },
            want: MyBackend{optionA: true},
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := New(tt.options)
            if got.optionA != tt.want.optionA {
                t.Errorf("optionA = %v, want %v", got.optionA, tt.want.optionA)
            }
        })
    }
}

func TestValidateOptions(t *testing.T) {
    tests := []struct {
        name    string
        options map[string]string
        wantErr bool
    }{
        {
            name:    "valid options",
            options: map[string]string{"option-a": "true"},
            wantErr: false,
        },
        {
            name:    "invalid option",
            options: map[string]string{"invalid": "true"},
            wantErr: true,
        },
        {
            name:    "invalid value",
            options: map[string]string{"option-a": "yes"},
            wantErr: true,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            backend := New(nil)
            err := backend.ValidateOptions(tt.options)
            if (err != nil) != tt.wantErr {
                t.Errorf("ValidateOptions() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}

func TestInstallCommands(t *testing.T) {
    backend := New(nil)
    
    install := config.Install{
        Packages: []string{"pkg1", "pkg2"},
    }
    
    cmds := backend.InstallCommands(install)
    
    if len(cmds) == 0 {
        t.Fatal("Expected at least one command")
    }
    
    // Verify command structure
    cmd := cmds[0]
    if cmd[0] != "mypackagemanager" {
        t.Errorf("Expected mypackagemanager command, got %s", cmd[0])
    }
    
    // Verify packages are included
    foundPkg1 := false
    foundPkg2 := false
    for _, arg := range cmd {
        if arg == "pkg1" {
            foundPkg1 = true
        }
        if arg == "pkg2" {
            foundPkg2 = true
        }
    }
    if !foundPkg1 || !foundPkg2 {
        t.Error("Expected both packages in command")
    }
}

func TestInstallCommandsWithOptions(t *testing.T) {
    options := map[string]string{
        "option-a": "true",
    }
    backend := New(options)
    
    install := config.Install{
        Packages: []string{"pkg1"},
    }
    
    cmds := backend.InstallCommands(install)
    cmd := cmds[0]
    
    // Check that option flag is present
    hasFlag := false
    for _, arg := range cmd {
        if arg == "--option-a-flag" {
            hasFlag = true
            break
        }
    }
    if !hasFlag {
        t.Error("Expected option flag in command")
    }
}
```

### For Publishers
```bash
touch internal/publisher/registry/registry_test.go
```

```go
package registry

import "testing"

func TestNew(t *testing.T) {
    url := "registry.example.com"
    options := []string{"--tls-verify=false"}
    
    pub := New(url, options)
    
    if pub.url != url {
        t.Errorf("Expected url %s, got %s", url, pub.url)
    }
    
    if len(pub.options) != 1 {
        t.Errorf("Expected 1 option, got %d", len(pub.options))
    }
}
```

---

## Mock Testing (Advanced)

For testing components that interact with external systems (buildah, S3, etc.):

```go
// Create a mock container
type mockContainer struct {
    container.Container
    commitCalled bool
}

func (m *mockContainer) CommitWithLabels(ctx context.Context, name, tag string, labels map[string]string) (string, error) {
    m.commitCalled = true
    return "test-id", nil
}

func TestPublisher(t *testing.T) {
    mock := &mockContainer{}
    
    // Test with mock
    pub := registry.New("test.io", nil)
    err := pub.Publish(context.Background(), mock, "test", "1.0", nil)
    
    if err != nil {
        t.Errorf("Unexpected error: %v", err)
    }
    
    if !mock.commitCalled {
        t.Error("Expected Commit to be called")
    }
}
```

---

## Continuous Integration

Add to `.github/workflows/test.yaml`:

```yaml
name: Run Tests

on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      
      - uses: actions/setup-go@v4
        with:
          go-version: '1.21'
      
      - name: Run tests
        run: go test -v -cover ./...
      
      - name: Run tests with race detector
        run: go test -race ./...
```

---

## Comparison: Python vs Go Testing

| Feature | Python (unittest/pytest) | Go (built-in) |
|---------|-------------------------|---------------|
| **Framework** | External (pytest) | Built-in |
| **File naming** | `test_*.py` | `*_test.go` |
| **Function naming** | `test_*` | `Test*` |
| **Running tests** | `pytest` | `go test` |
| **Setup/Teardown** | `setUp()`, `tearDown()` | `t.TempDir()`, defer |
| **Subtests** | Parametrize | `t.Run()` |
| **Assertions** | `assert x == y` | `if x != y { t.Error() }` |
| **Coverage** | `pytest --cov` | `go test -cover` |
| **Mocking** | `unittest.mock` | Interfaces (design pattern) |

---

## Quick Reference

### Common t methods:
```go
t.Error("message")           // Mark test failed, continue
t.Errorf("format %s", arg)   // Formatted error, continue
t.Fatal("message")           // Mark test failed, stop immediately
t.Fatalf("format %s", arg)   // Formatted fatal, stop
t.Log("message")             // Log message (only shown with -v)
t.Skip("reason")             // Skip this test
t.Parallel()                 // Run this test in parallel
t.TempDir()                  // Create temp dir (auto-cleanup)
t.Helper()                   // Mark function as test helper
```

### Running options:
```bash
go test                      # Run tests in current package
go test ./...                # Run all tests recursively
go test -v                   # Verbose output
go test -run TestName        # Run specific test
go test -cover               # Show coverage
go test -race                # Detect race conditions
go test -bench .             # Run benchmarks
go test -timeout 30s         # Set timeout
go test -count=1             # Disable test cache
```

---

## Next Steps

1. **Run existing tests:**
   ```bash
   go test ./internal/config -v
   go test ./internal/labels -v
   ```

2. **Add tests for your components:**
   - Backend implementations
   - Publisher implementations
   - Builder logic

3. **Set up CI/CD** to run tests automatically

4. **Aim for 80%+ coverage** on critical paths

Go testing is simpler than Python - no external dependencies, everything is built-in! 🎉
