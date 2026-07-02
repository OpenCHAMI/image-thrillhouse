// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package dnf

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/travisbcotton/image-thrillhouse/internal/container"
)

func TestDnfClassifier_ErrorCapture(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		err           error
		wantErrorMsgs []string
	}{
		{
			name: "simple error with details",
			input: `Error:
 Problem: cannot install the best candidate for the job
  - nothing provides libfoo needed by doca-ofed-userspace-1.0`,
			err:           errors.New("exit status 1"),
			wantErrorMsgs: []string{"Error:", "Problem: cannot install the best candidate for the job", "- nothing provides libfoo needed by doca-ofed-userspace-1.0"},
		},
		{
			name: "error with package not found",
			input: `Error:
Unable to find a match: nonexistent-package`,
			err:           errors.New("exit status 1"),
			wantErrorMsgs: []string{"Error:", "Unable to find a match: nonexistent-package"},
		},
		{
			name: "multiple error blocks separated by blank line",
			input: `Error:
 Problem 1: conflicting requests
  - package A conflicts with package B

Error:
 Problem 2: missing dependencies`,
			err:           errors.New("exit status 1"),
			wantErrorMsgs: []string{"Error:", "Problem 1: conflicting requests", "- package A conflicts with package B", "Error:", "Problem 2: missing dependencies"},
		},
		{
			name: "error followed by blank line then other output",
			input: `Error:
Unable to find a match: doca-extra

Installed:
bash-5.0
systemd-239`,
			err:           errors.New("exit status 1"),
			wantErrorMsgs: []string{"Error:", "Unable to find a match: doca-extra"},
		},
		{
			name: "success with no errors",
			input: `Installed:
bash-5.0
systemd-239`,
			err:           nil,
			wantErrorMsgs: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a buffer to capture log output
			var logBuf bytes.Buffer
			handler := slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})
			logger := slog.New(handler)

			// Create classifier with our test logger
			classifier := &dnfClassifier{
				log: logger.With("component", "backend.dnf"),
			}

			// Create LineWriter
			writer := container.NewLineWriter(classifier)

			// Write the input
			writer.Write([]byte(tt.input))

			// Flush with error state
			writer.Flush(tt.err)

			// Check that we captured the expected error messages
			if len(classifier.errors) != len(tt.wantErrorMsgs) {
				t.Errorf("Expected %d error messages, got %d: %v", len(tt.wantErrorMsgs), len(classifier.errors), classifier.errors)
			}

			for i, want := range tt.wantErrorMsgs {
				if i >= len(classifier.errors) {
					t.Errorf("Missing error message at index %d: want %q", i, want)
					continue
				}
				if classifier.errors[i] != want {
					t.Errorf("Error message %d:\n  got:  %q\n  want: %q", i, classifier.errors[i], want)
				}
			}

			// Verify the log output contains the combined error message if there was an error
			if tt.err != nil && len(tt.wantErrorMsgs) > 0 {
				logOutput := logBuf.String()
				if !strings.Contains(logOutput, "dnf error") {
					t.Error("Log output does not contain 'dnf error'")
				}
				// Check that all error messages are in the log
				for _, msg := range tt.wantErrorMsgs {
					if !strings.Contains(logOutput, msg) {
						t.Errorf("Log output does not contain expected error message: %q\nLog output:\n%s", msg, logOutput)
					}
				}
			}
		})
	}
}

func TestDnfClassifier_InstalledPackages(t *testing.T) {
	input := `Installed:
bash-5.0
systemd-239
kernel-5.14.0`

	var logBuf bytes.Buffer
	handler := slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(handler)

	classifier := &dnfClassifier{
		log: logger.With("component", "backend.dnf"),
	}

	writer := container.NewLineWriter(classifier)
	writer.Write([]byte(input))
	writer.Flush(nil)

	want := []string{"bash-5.0", "systemd-239", "kernel-5.14.0"}
	if len(classifier.installed) != len(want) {
		t.Fatalf("Expected %d installed packages, got %d: %v", len(want), len(classifier.installed), classifier.installed)
	}

	for i, pkg := range want {
		if classifier.installed[i] != pkg {
			t.Errorf("Installed package %d: got %q, want %q", i, classifier.installed[i], pkg)
		}
	}

	// Verify log output
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "packages installed") {
		t.Error("Log output does not contain 'packages installed'")
	}
}

func TestDnfClassifier_Warnings(t *testing.T) {
	input := `Unable to detect release version (use '--releasever' to specify release version)
Installed:
bash-5.0`

	var logBuf bytes.Buffer
	handler := slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(handler)

	classifier := &dnfClassifier{
		log: logger.With("component", "backend.dnf"),
	}

	writer := container.NewLineWriter(classifier)
	writer.Write([]byte(input))
	writer.Flush(nil)

	if len(classifier.warnings) != 1 {
		t.Fatalf("Expected 1 warning, got %d: %v", len(classifier.warnings), classifier.warnings)
	}

	// Verify log output contains warning
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "dnf warning") {
		t.Error("Log output does not contain 'dnf warning'")
	}
	if !strings.Contains(logOutput, "Unable to detect release version") {
		t.Error("Log output does not contain expected warning message")
	}
}

// TestDnfClassifier_ScriptletWarning verifies that scriptlet failures — which
// don't fail the transaction but can leave a package half-configured — are
// surfaced as warnings rather than staying buried in the raw DEBUG output.
func TestDnfClassifier_ScriptletWarning(t *testing.T) {
	input := `Error in POSTTRANS scriptlet in rpm package kernel-core
Installed:
kernel-core-5.14.0`

	var logBuf bytes.Buffer
	handler := slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(handler)

	classifier := &dnfClassifier{
		log: logger.With("component", "backend.dnf"),
	}

	writer := container.NewLineWriter(classifier)
	writer.Write([]byte(input))
	writer.Flush(nil)

	if len(classifier.warnings) != 1 {
		t.Fatalf("Expected 1 warning, got %d: %v", len(classifier.warnings), classifier.warnings)
	}
	if len(classifier.errors) != 0 {
		t.Fatalf("Scriptlet failure must classify as warning, not error, got errors: %v", classifier.errors)
	}

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "dnf warning") {
		t.Error("Log output does not contain 'dnf warning'")
	}
	if !strings.Contains(logOutput, "Error in POSTTRANS scriptlet") {
		t.Error("Log output does not contain the scriptlet warning message")
	}
}
