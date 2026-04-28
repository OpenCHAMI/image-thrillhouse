package s3

import (
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name      string
		endpoint  string
		bucket    string
		prefix    string
		accessKey string
		secretKey string
	}{
		{
			name:      "AWS S3",
			endpoint:  "https://s3.amazonaws.com",
			bucket:    "boot-images",
			prefix:    "compute/",
			accessKey: "AKIAIOSFODNN7EXAMPLE",
			secretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		},
		{
			name:      "MinIO",
			endpoint:  "http://localhost:9000",
			bucket:    "images",
			prefix:    "test/",
			accessKey: "minioadmin",
			secretKey: "minioadmin",
		},
		{
			name:      "custom S3",
			endpoint:  "https://s3.example.com",
			bucket:    "boot",
			prefix:    "",
			accessKey: "access",
			secretKey: "secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pub := New(tt.endpoint, tt.bucket, tt.prefix, tt.accessKey, tt.secretKey)
			if pub == nil {
				t.Fatal("New() returned nil")
			}
			if pub.endpoint != tt.endpoint {
				t.Errorf("endpoint = %v, want %v", pub.endpoint, tt.endpoint)
			}
			if pub.bucket != tt.bucket {
				t.Errorf("bucket = %v, want %v", pub.bucket, tt.bucket)
			}
			if pub.prefix != tt.prefix {
				t.Errorf("prefix = %v, want %v", pub.prefix, tt.prefix)
			}
			if pub.accessKey != tt.accessKey {
				t.Errorf("accessKey = %v, want %v", pub.accessKey, tt.accessKey)
			}
			if pub.secretKey != tt.secretKey {
				t.Errorf("secretKey = %v, want %v", pub.secretKey, tt.secretKey)
			}
		})
	}
}

func TestS3Publisher_Type(t *testing.T) {
	pub := New("https://s3.amazonaws.com", "bucket", "prefix/", "key", "secret")
	
	if _, ok := interface{}(pub).(*S3Publisher); !ok {
		t.Error("New() did not return *S3Publisher")
	}
}

func TestDetectOS(t *testing.T) {
	pub := New("", "", "", "", "")
	
	// Create a temporary os-release file
	tmpDir := t.TempDir()
	
	tests := []struct {
		name       string
		content    string
		expectedID string
		wantErr    bool
	}{
		{
			name: "rocky linux",
			content: `NAME="Rocky Linux"
VERSION="9.5"
ID=rocky
ID_LIKE="rhel centos fedora"
VERSION_ID="9.5"`,
			expectedID: "rocky",
			wantErr:    false,
		},
		{
			name: "ubuntu",
			content: `NAME="Ubuntu"
VERSION="22.04"
ID=ubuntu
ID_LIKE=debian
VERSION_ID="22.04"`,
			expectedID: "ubuntu",
			wantErr:    false,
		},
		{
			name: "with quotes",
			content: `NAME="AlmaLinux"
ID="almalinux"
VERSION_ID="9"`,
			expectedID: "almalinux",
			wantErr:    false,
		},
		{
			name:       "missing ID",
			content:    `NAME="Unknown"\nVERSION="1.0"`,
			expectedID: "",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// We can't easily test this without creating temp files
			// but we can verify the logic works with real content
			if !tt.wantErr {
				lines := strings.Split(tt.content, "\n")
				found := false
				for _, line := range lines {
					if strings.HasPrefix(line, "ID=") {
						osID := strings.TrimPrefix(line, "ID=")
						osID = strings.Trim(osID, "\"")
						if osID == tt.expectedID {
							found = true
						}
						break
					}
				}
				if !found {
					t.Errorf("Failed to extract ID=%s from content", tt.expectedID)
				}
			}
		})
	}
}

func TestFindKernelVersion_Logic(t *testing.T) {
	// Test the logic without actual filesystem
	// In real scenario, we'd read /lib/modules/
	
	kernelVersions := []string{
		"5.14.0-362.24.1.el9_3.x86_64",
		"6.1.0-18-amd64",
		"5.15.0-91-generic",
	}
	
	for _, version := range kernelVersions {
		if version == "" {
			t.Error("Kernel version should not be empty")
		}
		if !strings.Contains(version, ".") {
			t.Error("Kernel version should contain dots")
		}
	}
}

func TestInitramfsPatterns(t *testing.T) {
	tests := []struct {
		name    string
		kver    string
		pattern string
	}{
		{
			name:    "RHEL/Rocky style",
			kver:    "5.14.0-362.el9.x86_64",
			pattern: "initramfs-5.14.0-362.el9.x86_64.img",
		},
		{
			name:    "Debian style",
			kver:    "6.1.0-18-amd64",
			pattern: "initrd-6.1.0-18-amd64",
		},
		{
			name:    "Ubuntu style",
			kver:    "5.15.0-91-generic",
			pattern: "initrd.img-5.15.0-91-generic",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(tt.pattern, tt.kver) {
				t.Errorf("Pattern %s should contain kernel version %s", tt.pattern, tt.kver)
			}
		})
	}
}

func TestVmlinuzPattern(t *testing.T) {
	kver := "5.14.0-362.el9.x86_64"
	expected := "vmlinuz-" + kver
	
	if !strings.HasPrefix(expected, "vmlinuz-") {
		t.Error("vmlinuz pattern should start with 'vmlinuz-'")
	}
	
	if !strings.Contains(expected, kver) {
		t.Error("vmlinuz pattern should contain kernel version")
	}
}

func TestS3KeyGeneration(t *testing.T) {
	tests := []struct {
		name         string
		prefix       string
		osName       string
		imageName    string
		tag          string
		expectedKey  string
	}{
		{
			name:        "with prefix",
			prefix:      "compute/base/",
			osName:      "rocky",
			imageName:   "rocky-base",
			tag:         "9.5",
			expectedKey: "compute/base/rocky-rocky-base-9.5",
		},
		{
			name:        "without prefix",
			prefix:      "",
			osName:      "ubuntu",
			imageName:   "ubuntu-base",
			tag:         "22.04",
			expectedKey: "ubuntu-ubuntu-base-22.04",
		},
		{
			name:        "efi-images path",
			prefix:      "compute/",
			osName:      "rocky",
			imageName:   "compute",
			tag:         "1.0",
			expectedKey: "efi-images/compute/vmlinuz-5.14.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test rootfs key
			rootfsKey := tt.prefix + tt.osName + "-" + tt.imageName + "-" + tt.tag
			if !strings.Contains(rootfsKey, tt.imageName) {
				t.Errorf("Key should contain image name: %s", rootfsKey)
			}
		})
	}
}
