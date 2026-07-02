// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package local

import (
	"testing"
)

func TestNew(t *testing.T) {
	pub := New()
	if pub == nil {
		t.Fatal("New() returned nil")
	}
}

// Note: Full Publish() testing requires a mock container
// We'll test the structure here, integration tests would test actual commits
func TestLocalPublisher_Type(t *testing.T) {
	pub := New()

	// Verify it's the correct type
	if _, ok := interface{}(pub).(*LocalPublisher); !ok {
		t.Error("New() did not return *LocalPublisher")
	}
}
