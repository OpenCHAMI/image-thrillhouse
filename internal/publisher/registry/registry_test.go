package registry

import (
	"testing"
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
