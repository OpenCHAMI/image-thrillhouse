// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package main

import (
	"slices"
	"testing"
)

func TestResolveTargetTypes(t *testing.T) {
	tests := []struct {
		name    string
		flags   []string
		want    []string
		wantErr bool
	}{
		{
			name:  "absent flag defaults to registry",
			flags: nil,
			want:  []string{"registry"},
		},
		{
			name:  "single target",
			flags: []string{"s3"},
			want:  []string{"s3"},
		},
		{
			name:  "repeated flag preserves the order given",
			flags: []string{"s3", "registry"},
			want:  []string{"s3", "registry"},
		},
		{
			name:  "duplicates collapse so a destination is written once",
			flags: []string{"registry", "s3", "registry"},
			want:  []string{"registry", "s3"},
		},
		{
			name:    "unsupported target is rejected",
			flags:   []string{"registry", "ftp"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveTargetTypes(tt.flags)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("resolveTargetTypes(%v) = %v, want error", tt.flags, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("resolveTargetTypes(%v) = %v, want %v", tt.flags, got, tt.want)
			}
		})
	}
}

func TestJoinTypes(t *testing.T) {
	tests := []struct {
		name  string
		types []string
		want  string
	}{
		{
			name:  "single target reads as itself",
			types: []string{"registry"},
			want:  "registry",
		},
		{
			// "registry, s3" reads as one compound destination, which is how
			// the error gets misread as naming a required target rather than
			// several acceptable ones.
			name:  "two targets are joined disjunctively",
			types: []string{"registry", "s3"},
			want:  "registry or s3",
		},
		{
			name:  "three targets keep the serial comma",
			types: []string{"registry", "s3", "local"},
			want:  "registry, s3, or local",
		},
		{
			name:  "empty",
			types: nil,
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := joinTypes(tt.types); got != tt.want {
				t.Errorf("joinTypes(%v) = %q, want %q", tt.types, got, tt.want)
			}
		})
	}
}
