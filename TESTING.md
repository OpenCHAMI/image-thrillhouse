# Go Testing Guide

## Quick Start

```bash
# Run all tests
go test ./...

# Run with verbose output
go test -v ./...

# Run with coverage
go test -cover ./...

# Run specific package
go test ./internal/config

# Run specific test
go test -run TestLoadConfig ./internal/config
```

## Test Structure

Test files must:
- End with `_test.go` (e.g., `config_test.go`)
- Have test functions starting with `Test*` (e.g., `TestLoadConfig`)
- Be in the same package as the code being tested

Basic example:
```go
package mypackage

import "testing"

func TestMyFunction(t *testing.T) {
    result := MyFunction("test")
    if result != "expected" {
        t.Errorf("Expected 'expected', got '%s'", result)
    }
}
```

## Common Test Patterns

### Table-Driven Tests (Recommended)
```go
func TestValidate(t *testing.T) {
    tests := []struct {
        name    string
        input   MyType
        wantErr bool
    }{
        {"valid input", MyType{Field: "value"}, false},
        {"invalid input", MyType{}, true},
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := tt.input.Validate()
            if (err != nil) != tt.wantErr {
                t.Errorf("got error %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}
```

### Temporary Files
```go
func TestWithFile(t *testing.T) {
    tmpDir := t.TempDir() // Auto-cleaned up
    filePath := filepath.Join(tmpDir, "test.txt")
    // ... use file ...
}
```

## Current Test Coverage

- `internal/config` - Config loading and validation (10+ tests)
- `internal/labels` - Label generation (12+ tests)
- `internal/backend/apt` - APT package manager (25+ tests)
- `internal/backend/dnf` - DNF package manager (20+ tests)
- `internal/backend/zypper` - Zypper package manager (15+ tests)
- `internal/backend/mmdebstrap` - mmdebstrap backend (15+ tests)

## Best Practices

**Do:**
- Use table-driven tests for multiple scenarios
- Use `t.TempDir()` for temporary files
- Test both success and error cases
- Keep tests independent

**Don't:**
- Use global state
- Rely on test execution order
- Make external network calls (mock instead)
- Test implementation details


## Adding New Tests

### Backend Tests Template

Backend tests should cover:
- Option parsing and defaults
- Command generation
- Flag generation based on options
- Option validation

```go
func TestNewWithOptions(t *testing.T) {
    tests := []struct {
        name    string
        options map[string]string
        want    MyBackend
    }{
        {"default options", nil, MyBackend{optionA: false}},
        {"with options", map[string]string{"option-a": "true"}, MyBackend{optionA: true}},
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := New(tt.options)
            if got.optionA != tt.want.optionA {
                t.Errorf("got %v, want %v", got.optionA, tt.want.optionA)
            }
        })
    }
}
```

## Useful Commands

```bash
go test                      # Run tests in current package
go test ./...                # Run all tests recursively
go test -v                   # Verbose output
go test -run TestName        # Run specific test
go test -cover               # Show coverage
go test -race                # Detect race conditions
go test -count=1             # Disable test cache
```

## Common t Methods

```go
t.Error("message")           // Mark failed, continue
t.Errorf("format %s", arg)   # Formatted error, continue
t.Fatal("message")           // Mark failed, stop immediately
t.Log("message")             # Log (only shown with -v)
t.Skip("reason")             # Skip test
t.TempDir()                  # Create temp dir (auto-cleanup)
t.Helper()                   # Mark as helper function
```
