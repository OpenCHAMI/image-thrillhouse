// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

// Package s3 provides a publisher for uploading boot images to S3-compatible storage.
// This publisher creates SquashFS images and extracts kernel/initramfs for network booting.
package s3

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/travisbcotton/image-thrillhouse/internal/container"
	"github.com/travisbcotton/image-thrillhouse/internal/fsutil"
)

// S3Publisher uploads boot images to S3-compatible storage.
// It creates a SquashFS rootfs and uploads it along with the kernel and initramfs.
type S3Publisher struct {
	endpoint  string // S3 endpoint URL
	bucket    string // S3 bucket name
	prefix    string // S3 key prefix
	arch      string // target arch for the key layout ("" omits the arch segment)
	accessKey string // AWS access key ID
	secretKey string // AWS secret access key
}

// New creates a new S3Publisher with the specified configuration.
// Credentials are typically provided via environment variables S3_ACCESS and S3_SECRET.
//
// Parameters:
//   - endpoint: S3 endpoint URL (e.g., "https://s3.amazonaws.com" or custom S3 service)
//   - bucket: S3 bucket name
//   - prefix: Key prefix for uploaded objects (e.g., "compute/")
//   - arch: target architecture for the key layout (e.g. "x86_64"); "" omits the
//     arch path segment (single-arch / non-manifest builds)
//   - accessKey: AWS access key ID or S3-compatible access key
//   - secretKey: AWS secret access key or S3-compatible secret
func New(endpoint, bucket, prefix, arch, accessKey, secretKey string) *S3Publisher {
	return &S3Publisher{
		endpoint:  endpoint,
		bucket:    bucket,
		prefix:    prefix,
		arch:      arch,
		accessKey: accessKey,
		secretKey: secretKey,
	}
}

// objectKeys returns the S3 keys for a tag's three boot artifacts, laid out as
//
//	<prefix><tag>/<arch>/rootfs.squashfs
//	<prefix><tag>/<arch>/vmlinuz
//	<prefix><tag>/<arch>/initramfs.img
//
// Everything for a tag lives under one directory, so a materialized image is
// self-contained and immutable — a different tag is a different directory, and
// there is no shared kernel-version-keyed object a later build can overwrite.
// The arch segment is omitted when arch is empty (single-arch / non-manifest).
func (s *S3Publisher) objectKeys(tag string) (rootfs, kernel, initramfs string) {
	base := s.prefix + tag + "/"
	if s.arch != "" {
		base += s.arch + "/"
	}
	return base + "rootfs.squashfs", base + "vmlinuz", base + "initramfs.img"
}

// Publish creates a SquashFS image and uploads it to S3 along with kernel and initramfs.
//
// The upload structure is a self-contained directory per tag (see objectKeys):
//   - s3://<bucket>/<prefix><tag>/<arch>/rootfs.squashfs
//   - s3://<bucket>/<prefix><tag>/<arch>/vmlinuz
//   - s3://<bucket>/<prefix><tag>/<arch>/initramfs.img
//
// The <arch> segment is omitted when the publisher has no arch configured.
//
// Note: Labels are not uploaded to S3 as they are only relevant for OCI images.
func (s *S3Publisher) Publish(ctx context.Context, c container.Container, name string, tags []string, labels map[string]string) error {
	if len(tags) == 0 {
		return fmt.Errorf("s3 publisher requires at least one tag")
	}
	// Use the first tag for the image name
	tag := tags[0]

	log := slog.With("component", "publisher.s3")
	log.Info("publishing to s3", "bucket", s.bucket, "prefix", s.prefix)

	mountPath := c.MountPath()

	// Step 1: Find kernel version
	kernelVersion, err := s.findKernelVersion(mountPath)
	if err != nil {
		return fmt.Errorf("find kernel version: %w", err)
	}
	log.Info("found kernel version", "version", kernelVersion)

	// Step 2: Find initramfs
	initramfsPath, err := s.findInitramfs(mountPath, kernelVersion)
	if err != nil {
		return fmt.Errorf("find initramfs: %w", err)
	}
	log.Info("found initramfs", "path", initramfsPath)

	// Step 3: Find vmlinuz
	vmlinuzPath := filepath.Join(mountPath, "boot", fmt.Sprintf("vmlinuz-%s", kernelVersion))
	if _, err := os.Stat(vmlinuzPath); err != nil {
		return fmt.Errorf("vmlinuz not found at %s: %w", vmlinuzPath, err)
	}
	log.Info("found vmlinuz", "path", vmlinuzPath)

	// Step 4: Create SquashFS image
	squashfsPath, err := s.createSquashFS(ctx, mountPath, name, tag)
	if err != nil {
		return fmt.Errorf("create squashfs: %w", err)
	}
	defer os.Remove(squashfsPath)
	log.Info("created squashfs", "path", squashfsPath)

	// Step 5: Create S3 client
	client, err := s.createS3Client(ctx)
	if err != nil {
		return fmt.Errorf("create S3 client: %w", err)
	}

	uploader := manager.NewUploader(client)

	rootfsKey, vmlinuzKey, initramfsKey := s.objectKeys(tag)

	// Step 6: Upload rootfs
	if err := s.uploadFile(ctx, uploader, squashfsPath, rootfsKey); err != nil {
		return fmt.Errorf("upload rootfs: %w", err)
	}
	log.Info("uploaded rootfs", "key", rootfsKey)

	// Step 7: Upload kernel
	if err := s.uploadFile(ctx, uploader, vmlinuzPath, vmlinuzKey); err != nil {
		return fmt.Errorf("upload vmlinuz: %w", err)
	}
	log.Info("uploaded vmlinuz", "key", vmlinuzKey)

	// Step 8: Upload initramfs
	if err := s.uploadFile(ctx, uploader, initramfsPath, initramfsKey); err != nil {
		return fmt.Errorf("upload initramfs: %w", err)
	}
	log.Info("uploaded initramfs", "key", initramfsKey)

	log.Info("published to s3")
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

// findInitramfs returns the path to the initramfs file for the given kernel
// version, trying the RHEL/Rocky/Fedora and Debian/Ubuntu naming variants.
func (s *S3Publisher) findInitramfs(mountPath, kernelVersion string) (string, error) {
	bootPath := filepath.Join(mountPath, "boot")

	// initramfs-<version>.img (RHEL/Rocky/Fedora), initrd-<version> and
	// initrd.img-<version> (Debian/Ubuntu variants).
	for _, name := range []string{
		fmt.Sprintf("initramfs-%s.img", kernelVersion),
		fmt.Sprintf("initrd-%s", kernelVersion),
		fmt.Sprintf("initrd.img-%s", kernelVersion),
	} {
		p := filepath.Join(bootPath, name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("no initramfs found for kernel %s", kernelVersion)
}

// createSquashFS creates a SquashFS image in a temporary location
func (s *S3Publisher) createSquashFS(ctx context.Context, mountPath, name, tag string) (string, error) {
	tmpFile, err := os.CreateTemp("", fmt.Sprintf("image-thrillhouse-%s-%s-*.squashfs", name, tag))
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpFile.Close()

	squashfsPath := tmpFile.Name()
	if err := fsutil.MakeSquashFS(ctx, mountPath, squashfsPath); err != nil {
		os.Remove(squashfsPath)
		return "", err
	}
	return squashfsPath, nil
}

// createS3Client creates an AWS SDK v2 S3 client with custom endpoint support
func (s *S3Publisher) createS3Client(ctx context.Context) (*s3.Client, error) {
	// Load default config with custom credentials
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"), // Default region, not critical for custom endpoints
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			s.accessKey,
			s.secretKey,
			"",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	// Point the client at the configured (possibly non-AWS) endpoint and use
	// path-style addressing for S3-compatible services. BaseEndpoint replaces
	// the deprecated aws.EndpointResolverWithOptions mechanism.
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(s.endpoint)
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

// Exists reports whether every tag is already materialized in S3. It probes the
// rootfs key for each tag (objectKeys) — the tag-identifying artifact — and
// short-circuits on the first missing one. The key is fully determined by
// prefix + tag + arch, so no container is needed.
//
// A missing object surfaces as (false, nil); any other error (auth, DNS, TLS,
// 5xx) surfaces as (false, err) so callers fail loud rather than silently
// rebuilding or overwriting on an infra outage. name is unused — the S3 key
// layout is keyed by tag and arch, not by image name.
func (s *S3Publisher) Exists(ctx context.Context, name string, tags []string) (bool, error) {
	if len(tags) == 0 {
		return false, nil
	}
	client, err := s.createS3Client(ctx)
	if err != nil {
		return false, fmt.Errorf("create S3 client: %w", err)
	}
	for _, tag := range tags {
		rootfsKey, _, _ := s.objectKeys(tag)
		ok, err := s.objectExists(ctx, client, rootfsKey)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// objectExists reports whether key is present in the bucket via HeadObject. A
// 404 (typed NotFound, or a bare HTTP 404 from S3-compatible services whose HEAD
// response carries no error body) is (false, nil); anything else is a real error.
func (s *S3Publisher) objectExists(ctx context.Context, client *s3.Client, key string) (bool, error) {
	_, err := client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		return true, nil
	}

	var notFound *s3types.NotFound
	if errors.As(err, &notFound) {
		return false, nil
	}
	var respErr *smithyhttp.ResponseError
	if errors.As(err, &respErr) && respErr.HTTPStatusCode() == http.StatusNotFound {
		return false, nil
	}
	return false, fmt.Errorf("head object %s: %w", key, err)
}
