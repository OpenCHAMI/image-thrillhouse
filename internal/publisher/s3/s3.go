// Package s3 provides a publisher for uploading boot images to S3-compatible storage.
// This publisher creates SquashFS images and extracts kernel/initramfs for network booting.
package s3

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/travisbcotton/image-build/internal/container"
)

// S3Publisher uploads boot images to S3-compatible storage.
// It creates a SquashFS rootfs and uploads it along with the kernel and initramfs.
type S3Publisher struct {
	endpoint  string // S3 endpoint URL
	bucket    string // S3 bucket name
	prefix    string // S3 key prefix
	accessKey string // AWS access key ID
	secretKey string // AWS secret access key
}

// New creates a new S3Publisher with the specified configuration.
// Credentials are typically provided via environment variables S3_ACCESS and S3_SECRET.
//
// Parameters:
//   - endpoint: S3 endpoint URL (e.g., "https://s3.amazonaws.com" or custom S3 service)
//   - bucket: S3 bucket name
//   - prefix: Key prefix for uploaded objects (e.g., "compute/base/")
//   - accessKey: AWS access key ID or S3-compatible access key
//   - secretKey: AWS secret access key or S3-compatible secret
func New(endpoint, bucket, prefix, accessKey, secretKey string) *S3Publisher {
	return &S3Publisher{
		endpoint:  endpoint,
		bucket:    bucket,
		prefix:    prefix,
		accessKey: accessKey,
		secretKey: secretKey,
	}
}

// Publish creates a SquashFS image and uploads it to S3 along with kernel and initramfs.
//
// The upload structure is:
//   - s3://<bucket>/<prefix><os>-<name>-<tag> (SquashFS rootfs)
//   - s3://<bucket>/efi-images/<prefix>vmlinuz-<kver> (kernel)
//   - s3://<bucket>/efi-images/<prefix>initramfs-<kver>.img (initramfs)
//
// This format is compatible with network booting systems that expect separate
// kernel, initramfs, and rootfs files.
//
// Note: Labels are not uploaded to S3 as they are only relevant for OCI images.
func (s *S3Publisher) Publish(ctx context.Context, c container.Container, name string, tags []string, labels map[string]string) error {
	// Use the first tag for the image name
	tag := tags[0]

	log := slog.With("component", "publisher", "type", "s3")
	log.Info("Publishing to S3", "bucket", s.bucket, "prefix", s.prefix)

	mountPath := c.MountPath()

	// Step 1: Find kernel version
	kernelVersion, err := s.findKernelVersion(mountPath)
	if err != nil {
		return fmt.Errorf("find kernel version: %w", err)
	}
	log.Info("Found kernel version", "version", kernelVersion)

	// Step 2: Find initramfs
	initramfsPath, initramfsName, err := s.findInitramfs(mountPath, kernelVersion)
	if err != nil {
		return fmt.Errorf("find initramfs: %w", err)
	}
	log.Info("Found initramfs", "path", initramfsPath, "name", initramfsName)

	// Step 3: Find vmlinuz
	vmlinuzPath := filepath.Join(mountPath, "boot", fmt.Sprintf("vmlinuz-%s", kernelVersion))
	if _, err := os.Stat(vmlinuzPath); err != nil {
		return fmt.Errorf("vmlinuz not found at %s: %w", vmlinuzPath, err)
	}
	log.Info("Found vmlinuz", "path", vmlinuzPath)

	// Step 4: Create SquashFS image
	squashfsPath, err := s.createSquashFS(ctx, mountPath, name, tag)
	if err != nil {
		return fmt.Errorf("create squashfs: %w", err)
	}
	defer os.Remove(squashfsPath)
	log.Info("Created SquashFS", "path", squashfsPath)

	// Step 5: Detect OS for naming
	osName, err := s.detectOS(mountPath)
	if err != nil {
		log.Warn("Could not detect OS, using 'linux'", "error", err)
		osName = "linux"
	}

	// Step 6: Create S3 client
	client, err := s.createS3Client(ctx)
	if err != nil {
		return fmt.Errorf("create S3 client: %w", err)
	}

	uploader := manager.NewUploader(client)

	// Step 7: Upload rootfs
	rootfsKey := fmt.Sprintf("%s%s-%s-%s", s.prefix, osName, name, tag)
	if err := s.uploadFile(ctx, uploader, squashfsPath, rootfsKey); err != nil {
		return fmt.Errorf("upload rootfs: %w", err)
	}
	log.Info("Uploaded rootfs", "key", rootfsKey)

	// Step 8: Upload kernel
	vmlinuzKey := fmt.Sprintf("efi-images/%svmlinuz-%s", s.prefix, kernelVersion)
	if err := s.uploadFile(ctx, uploader, vmlinuzPath, vmlinuzKey); err != nil {
		return fmt.Errorf("upload vmlinuz: %w", err)
	}
	log.Info("Uploaded vmlinuz", "key", vmlinuzKey)

	// Step 9: Upload initramfs
	initramfsKey := fmt.Sprintf("efi-images/%s%s", s.prefix, initramfsName)
	if err := s.uploadFile(ctx, uploader, initramfsPath, initramfsKey); err != nil {
		return fmt.Errorf("upload initramfs: %w", err)
	}
	log.Info("Uploaded initramfs", "key", initramfsKey)

	log.Info("Successfully published to S3")
	return nil
}

// findKernelVersion finds the first available kernel version in /lib/modules
func (s *S3Publisher) findKernelVersion(mountPath string) (string, error) {
	modulesPath := filepath.Join(mountPath, "lib", "modules")
	entries, err := os.ReadDir(modulesPath)
	if err != nil {
		return "", fmt.Errorf("read /lib/modules: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			return entry.Name(), nil
		}
	}

	return "", fmt.Errorf("no kernel versions found in /lib/modules")
}

// findInitramfs finds the initramfs file for the given kernel version
func (s *S3Publisher) findInitramfs(mountPath, kernelVersion string) (string, string, error) {
	bootPath := filepath.Join(mountPath, "boot")

	// Try initramfs-<version>.img (RHEL/Rocky/Fedora style)
	initramfsName := fmt.Sprintf("initramfs-%s.img", kernelVersion)
	initramfsPath := filepath.Join(bootPath, initramfsName)
	if _, err := os.Stat(initramfsPath); err == nil {
		return initramfsPath, initramfsName, nil
	}

	// Try initrd-<version> (Debian/Ubuntu style)
	initramfsName = fmt.Sprintf("initrd-%s", kernelVersion)
	initramfsPath = filepath.Join(bootPath, initramfsName)
	if _, err := os.Stat(initramfsPath); err == nil {
		return initramfsPath, initramfsName, nil
	}

	// Try initrd.img-<version> (another Debian variant)
	initramfsName = fmt.Sprintf("initrd.img-%s", kernelVersion)
	initramfsPath = filepath.Join(bootPath, initramfsName)
	if _, err := os.Stat(initramfsPath); err == nil {
		return initramfsPath, initramfsName, nil
	}

	return "", "", fmt.Errorf("no initramfs found for kernel %s", kernelVersion)
}

// createSquashFS creates a SquashFS image in a temporary location
func (s *S3Publisher) createSquashFS(ctx context.Context, mountPath, name, tag string) (string, error) {
	tmpFile, err := os.CreateTemp("", fmt.Sprintf("image-build-%s-%s-*.squashfs", name, tag))
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpFile.Close()

	squashfsPath := tmpFile.Name()

	cmd := exec.CommandContext(ctx, "mksquashfs", mountPath, squashfsPath, "-noappend", "-no-progress")
	if output, err := cmd.CombinedOutput(); err != nil {
		os.Remove(squashfsPath)
		return "", fmt.Errorf("mksquashfs: %w (output: %s)", err, string(output))
	}

	return squashfsPath, nil
}

// detectOS tries to detect the OS name from /etc/os-release
func (s *S3Publisher) detectOS(mountPath string) (string, error) {
	osReleasePath := filepath.Join(mountPath, "etc", "os-release")
	content, err := os.ReadFile(osReleasePath)
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "ID=") {
			osID := strings.TrimPrefix(line, "ID=")
			osID = strings.Trim(osID, "\"")
			return osID, nil
		}
	}

	return "", fmt.Errorf("ID not found in os-release")
}

// createS3Client creates an AWS SDK v2 S3 client with custom endpoint support
func (s *S3Publisher) createS3Client(ctx context.Context) (*s3.Client, error) {
	// Create custom endpoint resolver for non-AWS S3 endpoints
	customResolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
		if service == s3.ServiceID {
			return aws.Endpoint{
				URL:               s.endpoint,
				HostnameImmutable: true,
				Source:            aws.EndpointSourceCustom,
			}, nil
		}
		return aws.Endpoint{}, &aws.EndpointNotFoundError{}
	})

	// Load default config with custom credentials
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"), // Default region, not critical for custom endpoints
		config.WithEndpointResolverWithOptions(customResolver),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			s.accessKey,
			s.secretKey,
			"",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	// Create S3 client with path-style addressing for S3-compatible services
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true
	})

	return client, nil
}

// uploadFile uploads a file to S3
func (s *S3Publisher) uploadFile(ctx context.Context, uploader *manager.Uploader, filePath, key string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	_, err = uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   file,
	})
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}

	return nil
}
