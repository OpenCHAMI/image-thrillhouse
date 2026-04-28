package registry

import (
	"testing"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		options []string
	}{
		{
			name:    "docker hub",
			url:     "docker.io/myuser",
			options: nil,
		},
		{
			name:    "private registry with tls-verify",
			url:     "registry.example.com/myorg",
			options: []string{"--tls-verify=false"},
		},
		{
			name:    "with credentials",
			url:     "quay.io/myorg",
			options: []string{"--creds=user:pass"},
		},
		{
			name:    "multiple options",
			url:     "localhost:5000",
			options: []string{"--tls-verify=false", "--creds=admin:admin"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pub := New(tt.url, tt.options)
			if pub == nil {
				t.Fatal("New() returned nil")
			}
			if pub.url != tt.url {
				t.Errorf("New() url = %v, want %v", pub.url, tt.url)
			}
			if len(pub.options) != len(tt.options) {
				t.Errorf("New() options length = %v, want %v", len(pub.options), len(tt.options))
			}
		})
	}
}

func TestRegistryPublisher_Type(t *testing.T) {
	pub := New("registry.io", nil)
	
	if _, ok := interface{}(pub).(*RegistryPublisher); !ok {
		t.Error("New() did not return *RegistryPublisher")
	}
}

func TestRegistryPublisher_Options(t *testing.T) {
	options := []string{
		"--tls-verify=false",
		"--cert-dir=/certs",
		"--creds=user:pass",
	}
	
	pub := New("registry.io", options)
	
	if len(pub.options) != 3 {
		t.Errorf("Expected 3 options, got %d", len(pub.options))
	}
	
	// Verify options are stored correctly
	for i, opt := range pub.options {
		if opt != options[i] {
			t.Errorf("Option %d = %v, want %v", i, opt, options[i])
		}
	}
}
