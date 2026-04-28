package squashfs

import (
	"testing"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{
			name: "absolute path",
			path: "/output/images",
		},
		{
			name: "relative path",
			path: "./output",
		},
		{
			name: "home directory",
			path: "~/images",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pub := New(tt.path)
			if pub == nil {
				t.Fatal("New() returned nil")
			}
			if pub.path != tt.path {
				t.Errorf("New() path = %v, want %v", pub.path, tt.path)
			}
		})
	}
}

func TestSquashfsPublisher_Type(t *testing.T) {
	pub := New("/output")
	
	if _, ok := interface{}(pub).(*SquashfsPublisher); !ok {
		t.Error("New() did not return *SquashfsPublisher")
	}
}
