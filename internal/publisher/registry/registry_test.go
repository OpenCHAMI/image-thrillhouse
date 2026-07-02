// SPDX-FileCopyrightText: © 2025 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package registry

import (
	"errors"
	"fmt"
	"testing"

	"github.com/docker/distribution/registry/api/errcode"
	v2 "github.com/docker/distribution/registry/api/v2"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		tlsVerify bool
	}{
		{
			name:      "docker hub with tls verify",
			url:       "docker.io/myuser",
			tlsVerify: true,
		},
		{
			name:      "private registry without tls verify",
			url:       "registry.example.com/myorg",
			tlsVerify: false,
		},
		{
			name:      "quay with tls verify",
			url:       "quay.io/myorg",
			tlsVerify: true,
		},
		{
			name:      "local registry",
			url:       "localhost:5000",
			tlsVerify: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pub := New(tt.url, tt.tlsVerify)
			if pub == nil {
				t.Fatal("New() returned nil")
			}
			if pub.url != tt.url {
				t.Errorf("New() url = %v, want %v", pub.url, tt.url)
			}
			if pub.tlsVerify != tt.tlsVerify {
				t.Errorf("New() tlsVerify = %v, want %v", pub.tlsVerify, tt.tlsVerify)
			}
		})
	}
}

func TestRegistryPublisher_Type(t *testing.T) {
	pub := New("registry.io", true)

	if _, ok := interface{}(pub).(*RegistryPublisher); !ok {
		t.Error("New() did not return *RegistryPublisher")
	}
}

func TestIsManifestUnknown(t *testing.T) {
	// Cover every error shape isManifestUnknown is meant to catch, plus a
	// couple of negatives so the "true on anything that mentions an error"
	// failure mode would show up here.
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "errcode ManifestUnknown",
			err:  v2.ErrorCodeManifestUnknown.WithMessage("manifest unknown"),
			want: true,
		},
		{
			name: "errcode ManifestUnknown wrapped",
			err:  fmt.Errorf("probe failed: %w", v2.ErrorCodeManifestUnknown.WithMessage("manifest unknown")),
			want: true,
		},
		{
			name: "bare ManifestUnknown ErrorCode (no Error wrapper)",
			err:  v2.ErrorCodeManifestUnknown,
			want: true,
		},
		{
			name: "errcode Unknown with 'Not Found' message (redhat.io)",
			err:  errcode.ErrorCodeUnknown.WithMessage("Not Found"),
			want: true,
		},
		{
			name: "errcode Unknown wrapped with 'not found' lowercase",
			err:  fmt.Errorf("wrap: %w", errcode.ErrorCodeUnknown.WithMessage("manifest not found")),
			want: true,
		},
		{
			name: "errcode Unknown with unrelated message",
			err:  errcode.ErrorCodeUnknown.WithMessage("internal server error"),
			want: false,
		},
		{
			name: "plain error mentioning 'manifest unknown'",
			err:  errors.New("registry response: manifest unknown for tag 9.5"),
			want: true,
		},
		{
			name: "plain error mentioning 'status 404'",
			err:  errors.New("transport: HTTP status 404"),
			want: true,
		},
		{
			name: "plain error mentioning 'not found' (case insensitive)",
			err:  errors.New("Manifest Not Found"),
			want: true,
		},
		{
			name: "transport error must not be classified as not-found",
			err:  errors.New("dial tcp: connection refused"),
			want: false,
		},
		{
			name: "auth error must not be classified as not-found",
			err:  errors.New("unauthorized: invalid credentials"),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isManifestUnknown(tt.err)
			if got != tt.want {
				t.Errorf("isManifestUnknown(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestRegistryPublisher_TLSVerify(t *testing.T) {
	tests := []struct {
		name      string
		tlsVerify bool
	}{
		{
			name:      "with tls verify",
			tlsVerify: true,
		},
		{
			name:      "without tls verify",
			tlsVerify: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pub := New("registry.io", tt.tlsVerify)

			if pub.tlsVerify != tt.tlsVerify {
				t.Errorf("Expected tlsVerify = %v, got %v", tt.tlsVerify, pub.tlsVerify)
			}
		})
	}
}
