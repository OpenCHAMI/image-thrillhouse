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
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/openchami/image-thrillhouse/internal/container"
	"github.com/openchami/image-thrillhouse/internal/fsutil"
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

// baseKey returns the directory all of a tag's boot artifacts live under:
//
//	<prefix><tag>/<arch>/
//
// Everything for a tag lives under one directory, so a materialized image is
// self-contained and immutable — a different tag is a different directory, and
// there is no shared object a later build of a different tag can overwrite.
// The arch segment is omitted when arch is empty (single-arch / non-manifest).
func (s *S3Publisher) baseKey(tag string) string {
	base := s.prefix + tag + "/"
	if s.arch != "" {
		base += s.arch + "/"
	}
	return base
}

// rootfsKey returns the S3 key for a tag's rootfs, which is the
// tag-identifying artifact. It is deliberately independent of the kernel
// version so Exists can probe it without mounting a container.
func (s *S3Publisher) rootfsKey(tag string) string {
	return s.baseKey(tag) + "rootfs.squashfs"
}

// objectKeys returns the S3 keys for a tag's three boot artifacts, laid out as
//
//	<prefix><tag>/<arch>/rootfs.squashfs
//	<prefix><tag>/<arch>/vmlinuz
//	<prefix><tag>/<arch>/initramfs.img
//
// The kernel and initramfs get fixed, generic names regardless of the kernel
// version or the distro's /boot naming convention (initramfs-<kver>.img,
// initrd-<kver>, initrd.img-<kver>). Each tag already has its own directory,
// so the path identifies the image and consumers can find its boot artifacts
// without knowing which kernel it carries.
func (s *S3Publisher) objectKeys(tag string) (rootfs, kernel, initramfs string) {
	base := s.baseKey(tag)
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
	// Say so out loud rather than dropping them silently: one build writes one
	// self-contained directory keyed by the primary tag, and Exists probes that
	// same key. Matches the squashfs publisher's behaviour and message.
	if len(tags) > 1 {
		log.Info("multiple tags configured; using the first for the s3 key layout",
			"primary", tag, "ignored_tags", tags[1:])
	}
	log.Info("publishing to s3", "bucket", s.bucket, "prefix", s.prefix)

	mountPath := c.MountPath()

	// Step 1: Pick the kernel the image boots and find its initramfs.
	initramfsPath, kernelVersion, err := s.findBootInitramfs(mountPath)
	if err != nil {
		return fmt.Errorf("find initramfs: %w", err)
	}
	log.Info("found initramfs", "path", initramfsPath, "kernel_version", kernelVersion)

	// Step 2: Find vmlinuz
	vmlinuzPath := filepath.Join(mountPath, "boot", fmt.Sprintf("vmlinuz-%s", kernelVersion))
	if _, err := os.Stat(vmlinuzPath); err != nil {
		return fmt.Errorf("vmlinuz not found at %s: %w", vmlinuzPath, err)
	}
	log.Info("found vmlinuz", "path", vmlinuzPath)

	// Step 3: Create SquashFS image
	squashfsPath, err := s.createSquashFS(ctx, mountPath, name, tag)
	if err != nil {
		return fmt.Errorf("create squashfs: %w", err)
	}
	defer os.Remove(squashfsPath)
	log.Info("created squashfs", "path", squashfsPath)

	// Step 4: Create S3 client
	client, err := s.createS3Client(ctx)
	if err != nil {
		return fmt.Errorf("create S3 client: %w", err)
	}

	uploader := manager.NewUploader(client)

	rootfsKey, vmlinuzKey, initramfsKey := s.objectKeys(tag)

	// Step 5: Upload rootfs
	if err := s.uploadFile(ctx, uploader, squashfsPath, rootfsKey); err != nil {
		return fmt.Errorf("upload rootfs: %w", err)
	}
	log.Info("uploaded rootfs", "key", rootfsKey)

	// Step 6: Upload kernel
	if err := s.uploadFile(ctx, uploader, vmlinuzPath, vmlinuzKey); err != nil {
		return fmt.Errorf("upload vmlinuz: %w", err)
	}
	log.Info("uploaded vmlinuz", "key", vmlinuzKey, "source", filepath.Base(vmlinuzPath))

	// Step 7: Upload initramfs
	if err := s.uploadFile(ctx, uploader, initramfsPath, initramfsKey); err != nil {
		return fmt.Errorf("upload initramfs: %w", err)
	}
	log.Info("uploaded initramfs", "key", initramfsKey, "source", filepath.Base(initramfsPath))

	log.Info("published to s3")
	return nil
}

// initramfsPatterns are the /boot initramfs naming conventions, as
// prefix/suffix pairs around the kernel version: initramfs-<kver>.img
// (RHEL/Rocky/Fedora), initrd-<kver> and initrd.img-<kver> (Debian/Ubuntu).
var initramfsPatterns = []struct{ prefix, suffix string }{
	{"initramfs-", ".img"},
	{"initrd.img-", ""},
	{"initrd-", ""},
}

// initramfsKernelVersion extracts the kernel version from an initramfs
// filename, reporting false if the name matches no known convention.
func initramfsKernelVersion(name string) (string, bool) {
	for _, p := range initramfsPatterns {
		if strings.HasPrefix(name, p.prefix) && strings.HasSuffix(name, p.suffix) {
			kver := strings.TrimSuffix(strings.TrimPrefix(name, p.prefix), p.suffix)
			if kver != "" {
				return kver, true
			}
		}
	}
	return "", false
}

// defaultKernelLinks are the symlinks some distros maintain to the kernel the
// image boots by default (Debian/Ubuntu's linux-base keeps /vmlinuz, or
// /boot/vmlinuz when /boot is a separate filesystem). Paths are relative to the
// image root.
var defaultKernelLinks = []string{"vmlinuz", "boot/vmlinuz"}

// findBootInitramfs picks the kernel the image boots and returns its initramfs
// path along with the kernel version. /lib/modules can't be used for this:
// images often carry modules for several kernels, and directory order says
// nothing about which one is the default.
//
// Candidates are /boot initramfs images with a matching vmlinuz-<kver>, which
// excludes kdump images (initramfs-<kver>kdump.img). Rescue images
// (initramfs-0-rescue-<machine-id>.img) do have a matching vmlinuz but are a
// generic fallback, so they are skipped explicitly.
//
// Among the candidates, the kernel a default-kernel symlink points at wins.
// Otherwise the highest kernel version wins, compared the way RPM compares
// versions (see compareKernelVersions), which is also the kernel a package
// manager install leaves as the default. File modification times are
// deliberately ignored: rebuilding an older kernel's initramfs would otherwise
// make it look like the default.
func (s *S3Publisher) findBootInitramfs(mountPath string) (path, kernelVersion string, err error) {
	bootPath := filepath.Join(mountPath, "boot")
	entries, err := os.ReadDir(bootPath)
	if err != nil {
		return "", "", fmt.Errorf("read /boot: %w", err)
	}

	// candidates maps kernel version to initramfs filename. Patterns are
	// tried in initramfsPatterns order, so if one kernel somehow has
	// initramfs files under two conventions, the first convention wins.
	candidates := map[string]string{}
	for _, entry := range entries {
		name := entry.Name()
		kver, ok := initramfsKernelVersion(name)
		if !ok || strings.Contains(kver, "-rescue-") {
			continue
		}
		info, err := os.Stat(filepath.Join(bootPath, name))
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if _, err := os.Stat(filepath.Join(bootPath, "vmlinuz-"+kver)); err != nil {
			continue
		}
		if prev, ok := candidates[kver]; !ok || initramfsPatternRank(name) < initramfsPatternRank(prev) {
			candidates[kver] = name
		}
	}
	if len(candidates) == 0 {
		return "", "", fmt.Errorf("no initramfs with a matching vmlinuz found in /boot")
	}

	if kver, ok := defaultKernelVersion(mountPath); ok {
		if name, ok := candidates[kver]; ok {
			return filepath.Join(bootPath, name), kver, nil
		}
	}

	for kver := range candidates {
		if kernelVersion == "" || compareKernelVersions(kver, kernelVersion) > 0 {
			kernelVersion = kver
		}
	}
	return filepath.Join(bootPath, candidates[kernelVersion]), kernelVersion, nil
}

// initramfsPatternRank returns the index of the first initramfsPatterns entry
// that name matches, or len(initramfsPatterns) if none does.
func initramfsPatternRank(name string) int {
	for i, p := range initramfsPatterns {
		if strings.HasPrefix(name, p.prefix) && strings.HasSuffix(name, p.suffix) {
			return i
		}
	}
	return len(initramfsPatterns)
}

// defaultKernelVersion reports the kernel version named by the first
// default-kernel symlink (see defaultKernelLinks) that points at a
// vmlinuz-<kver> file. Only the link target's name is used, so an absolute
// target inside the image is never resolved against the host filesystem.
func defaultKernelVersion(mountPath string) (string, bool) {
	for _, link := range defaultKernelLinks {
		target, err := os.Readlink(filepath.Join(mountPath, link))
		if err != nil {
			continue
		}
		if kver, ok := strings.CutPrefix(filepath.Base(target), "vmlinuz-"); ok && kver != "" {
			return kver, true
		}
	}
	return "", false
}

// compareKernelVersions compares two kernel versions, returning -1, 0 or 1.
// It follows RPM's rpmvercmp: both strings are split into runs of digits and
// runs of letters (everything else separates runs). Digit runs compare
// numerically, so 6.10 > 6.9 and 5.14.0-427 > 5.14.0-362; letter runs compare
// lexically; a digit run beats a letter run; and when one version runs out of
// runs first, the longer one is newer. A tilde sorts before everything,
// including the end of the string, so 6.1~rc1 < 6.1. Versions that compare
// equal but differ textually (6.1-1 vs 6.1.1) fall back to string order so the
// result is deterministic.
func compareKernelVersions(a, b string) int {
	if c := rpmvercmp(a, b); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

func rpmvercmp(a, b string) int {
	isDigit := func(c byte) bool { return c >= '0' && c <= '9' }
	isAlpha := func(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
	isAlnum := func(c byte) bool { return isDigit(c) || isAlpha(c) }

	for {
		// Skip separators, but stop at a tilde.
		for a != "" && !isAlnum(a[0]) && a[0] != '~' {
			a = a[1:]
		}
		for b != "" && !isAlnum(b[0]) && b[0] != '~' {
			b = b[1:]
		}

		aTilde, bTilde := a != "" && a[0] == '~', b != "" && b[0] == '~'
		switch {
		case aTilde && bTilde:
			a, b = a[1:], b[1:]
			continue
		case aTilde:
			return -1
		case bTilde:
			return 1
		}

		if a == "" || b == "" {
			switch {
			case a == b:
				return 0
			case a == "":
				return -1
			default:
				return 1
			}
		}

		// Take the leading run of the same class from each side; the
		// class is decided by a, and a run of the other class in b is
		// empty.
		class := isAlpha
		if isDigit(a[0]) {
			class = isDigit
		}
		run := func(s string) (string, string) {
			i := 0
			for i < len(s) && class(s[i]) {
				i++
			}
			return s[:i], s[i:]
		}
		var aRun, bRun string
		aRun, a = run(a)
		bRun, b = run(b)

		if bRun == "" {
			// Different classes: digits are newer than letters.
			if isDigit(aRun[0]) {
				return 1
			}
			return -1
		}

		if isDigit(aRun[0]) {
			aRun = strings.TrimLeft(aRun, "0")
			bRun = strings.TrimLeft(bRun, "0")
			if len(aRun) != len(bRun) {
				if len(aRun) > len(bRun) {
					return 1
				}
				return -1
			}
		}
		if c := strings.Compare(aRun, bRun); c != 0 {
			return c
		}
	}
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

// Exists reports whether this image is already materialized in S3. It probes
// the rootfs key for the primary (first) tag — the tag-identifying artifact —
// which is fully determined by prefix + tag + arch, so no container is needed.
// This is also why the rootfs key deliberately carries no kernel version:
// Exists runs before any image is pulled or mounted, so a version-bearing key
// would be unknowable here and the --skip-if-exists guard could never fire.
//
// Only tags[0] is probed because only tags[0] is ever written: Publish
// materializes one self-contained directory per build (see objectKeys) rather
// than duplicating a multi-gigabyte rootfs under every tag. Probing every tag
// here meant --skip-if-exists could never succeed for a config with more than
// one tag — the second tag's key was asked for but never uploaded, so the guard
// reported "missing" forever and every run rebuilt and re-uploaded. This
// mirrors the squashfs publisher, which keys off tags[0] the same way.
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
	return s.objectExists(ctx, client, s.rootfsKey(tags[0]))
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
