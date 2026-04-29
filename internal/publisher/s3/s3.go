package s3

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/travisbcotton/image-build/internal/container"
)

type S3Publisher struct {
	endpoint string
	bucket   string
	prefix   string
	format   string
}

func New(endpoint, bucket, prefix, format string) *S3Publisher {
	return &S3Publisher{
		endpoint: endpoint,
		bucket:   bucket,
		prefix:   prefix,
		format:   format,
	}
}

func (p *S3Publisher) Publish(ctx context.Context, c container.Container, name string, tags []string) error {
	// read os-release
	osID, osVersion, err := readOSRelease(c.MountPath())
	if err != nil {
		return fmt.Errorf("read os-release: %w", err)
	}

	// set up s3 client
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("load aws config: %w", err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if p.endpoint != "" {
			o.BaseEndpoint = aws.String(p.endpoint)
			o.UsePathStyle = true // required for most S3-compatible stores including VersityGW
		}
	})

	for _, tag := range tags {
		keyPrefix := strings.TrimSuffix(p.prefix, "/") +
			fmt.Sprintf("/%s/%s/%s/%s/", osID, osVersion, name, tag)

		slog.Info("publishing to s3", "bucket", p.bucket, "prefix", keyPrefix)

		// create and upload squashfs
		squashfsPath, err := p.createSquashfs(c.MountPath(), name, tag)
		if err != nil {
			return fmt.Errorf("create squashfs: %w", err)
		}
		defer os.Remove(squashfsPath)

		if err := p.uploadFile(ctx, client, squashfsPath, keyPrefix+"rootfs.squashfs"); err != nil {
			return fmt.Errorf("upload squashfs: %w", err)
		}

		// find and upload kernel
		vmlinuz, initrd, err := findLatestKernel(c.MountPath())
		if err != nil {
			slog.Warn("kernel not found, skipping", "msg", err.Error())
		} else {
			if err := p.uploadFile(ctx, client, vmlinuz, keyPrefix+"kernel"); err != nil {
				return fmt.Errorf("upload kernel: %w", err)
			}
			if err := p.uploadFile(ctx, client, initrd, keyPrefix+"initramfs"); err != nil {
				return fmt.Errorf("upload initramfs: %w", err)
			}
		}

		slog.Info("published to s3", "bucket", p.bucket, "prefix", keyPrefix)
	}
	return nil
}

func (p *S3Publisher) createSquashfs(mountPath, name, tag string) (string, error) {
	tmp, err := os.CreateTemp("", fmt.Sprintf("image-build-%s-%s-*.squashfs", name, tag))
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmp.Close()

	if _, err := exec.LookPath("mksquashfs"); err != nil {
		return "", fmt.Errorf("mksquashfs not found: install squashfs-tools")
	}

	cmd := exec.Command("mksquashfs", mountPath, tmp.Name(), "-noappend", "-no-progress")
	cmd.Stdout = nil
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("mksquashfs: %w", err)
	}

	return tmp.Name(), nil
}

func (p *S3Publisher) uploadFile(ctx context.Context, client *s3.Client, path, key string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	slog.Info("uploading", "key", key)
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(p.bucket),
		Key:    aws.String(key),
		Body:   f,
	})
	return err
}

func readOSRelease(mountPath string) (id, versionID string, err error) {
	data, err := os.ReadFile(filepath.Join(mountPath, "etc/os-release"))
	if err != nil {
		return "", "", fmt.Errorf("read os-release: %w", err)
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ID=") {
			id = strings.Trim(strings.TrimPrefix(line, "ID="), "\"")
		}
		if strings.HasPrefix(line, "VERSION_ID=") {
			versionID = strings.Trim(strings.TrimPrefix(line, "VERSION_ID="), "\"")
		}
	}

	if id == "" {
		return "", "", fmt.Errorf("ID not found in os-release")
	}
	if versionID == "" {
		return "", "", fmt.Errorf("VERSION_ID not found in os-release")
	}

	return id, versionID, nil
}

func findLatestKernel(mountPath string) (vmlinuz, initramfs string, err error) {
	bootDir := filepath.Join(mountPath, "boot")

	// find all vmlinuz files
	matches, err := filepath.Glob(filepath.Join(bootDir, "vmlinuz-*"))
	if err != nil || len(matches) == 0 {
		return "", "", fmt.Errorf("no kernel found in %s", bootDir)
	}

	// sort and take latest
	sort.Strings(matches)
	vmlinuz = matches[len(matches)-1]

	// derive initramfs path from kernel version
	version := strings.TrimPrefix(filepath.Base(vmlinuz), "vmlinuz-")

	// try different initramfs naming conventions
	candidates := []string{
		filepath.Join(bootDir, fmt.Sprintf("initramfs-%s.img", version)), // RHEL/Rocky
		filepath.Join(bootDir, fmt.Sprintf("initrd.img-%s", version)),    // Debian
		filepath.Join(bootDir, fmt.Sprintf("initrd-%s.img", version)),    // openSUSE
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return vmlinuz, c, nil
		}
	}

	return "", "", fmt.Errorf("no initramfs found for kernel %s", version)
}
