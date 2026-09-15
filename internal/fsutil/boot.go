// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package fsutil

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ErrNoKernel reports that a root filesystem has no kernel installed
// (/lib/modules is missing or empty). Publishers that can emit a rootfs
// without boot files use it to tell "not a bootable image" apart from a
// kernel that is present but has no matching vmlinuz or initramfs.
var ErrNoKernel = errors.New("no kernel versions found in /lib/modules")

// BootFiles are the host paths of the kernel and initramfs inside a mounted
// root filesystem.
type BootFiles struct {
	KernelVersion string
	Vmlinuz       string
	Initramfs     string
}

// FindBootFiles locates the kernel and initramfs under rootDir. The kernel
// version is the first directory in /lib/modules; vmlinuz is
// /boot/vmlinuz-<version>, and the initramfs is the first of the
// RHEL/Rocky/Fedora (initramfs-<version>.img) or Debian/Ubuntu
// (initrd-<version>, initrd.img-<version>) names that exists.
//
// Returns an error wrapping ErrNoKernel when no kernel is installed.
func FindBootFiles(rootDir string) (BootFiles, error) {
	var bf BootFiles

	entries, err := os.ReadDir(filepath.Join(rootDir, "lib", "modules"))
	if err != nil && !os.IsNotExist(err) {
		return bf, fmt.Errorf("read /lib/modules: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			bf.KernelVersion = entry.Name()
			break
		}
	}
	if bf.KernelVersion == "" {
		return bf, ErrNoKernel
	}

	bootPath := filepath.Join(rootDir, "boot")

	bf.Vmlinuz = filepath.Join(bootPath, "vmlinuz-"+bf.KernelVersion)
	if _, err := os.Stat(bf.Vmlinuz); err != nil {
		return bf, fmt.Errorf("vmlinuz not found at %s: %w", bf.Vmlinuz, err)
	}

	for _, name := range []string{
		fmt.Sprintf("initramfs-%s.img", bf.KernelVersion),
		fmt.Sprintf("initrd-%s", bf.KernelVersion),
		fmt.Sprintf("initrd.img-%s", bf.KernelVersion),
	} {
		p := filepath.Join(bootPath, name)
		if _, err := os.Stat(p); err == nil {
			bf.Initramfs = p
			return bf, nil
		}
	}
	return bf, fmt.Errorf("no initramfs found for kernel %s", bf.KernelVersion)
}

// CopyFile copies src to dst, replacing dst if it exists. The copy is written
// to a temporary file in dst's directory and renamed into place, so a failed
// or interrupted copy never leaves a truncated dst behind.
func CopyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file for %s: %w", dst, err)
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()

	if _, err = io.Copy(tmp, in); err != nil {
		return fmt.Errorf("copy %s to %s: %w", src, dst, err)
	}
	if err = tmp.Chmod(0644); err != nil {
		return fmt.Errorf("chmod %s: %w", dst, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", dst, err)
	}
	if err = os.Rename(tmp.Name(), dst); err != nil {
		return fmt.Errorf("rename to %s: %w", dst, err)
	}
	return nil
}
