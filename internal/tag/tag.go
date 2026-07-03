// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package tag

import (
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"go.podman.io/storage/pkg/fileutils"

	"github.com/travisbcotton/image-thrillhouse/internal/config"
)

type LayerInput struct {
	ConfigPath string
	VarFiles   []string

	// VarOverrides are CLI-level "key=value" variable overrides (--var).
	// They change the rendered config just like var-file contents do, so
	// they must be part of the hash — otherwise two builds differing only
	// in --var would share a tag and skip-if-exists could silently reuse
	// the wrong image. Hashed sorted for determinism.
	VarOverrides []string
}

func Compute(layer LayerInput, ancestors []LayerInput) (string, error) {
	h := md5.New()

	// hash ancestors first in order
	for _, ancestor := range ancestors {
		if err := hashLayer(h, ancestor); err != nil {
			return "", fmt.Errorf("hash ancestor %s: %w", ancestor.ConfigPath, err)
		}
	}

	// hash this layer
	if err := hashLayer(h, layer); err != nil {
		return "", fmt.Errorf("hash layer %s: %w", layer.ConfigPath, err)
	}

	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func hashLayer(h io.Writer, layer LayerInput) error {
	// config file
	if err := hashFile(h, layer.ConfigPath); err != nil {
		return fmt.Errorf("hash config: %w", err)
	}

	// var files - sorted for determinism
	sorted := make([]string, len(layer.VarFiles))
	copy(sorted, layer.VarFiles)
	sort.Strings(sorted)
	for _, vf := range sorted {
		if err := hashFile(h, vf); err != nil {
			return fmt.Errorf("hash var file %s: %w", vf, err)
		}
	}

	// CLI --var overrides - sorted for determinism, length-prefixed so
	// adjacent entries can't collide by concatenation.
	overrides := make([]string, len(layer.VarOverrides))
	copy(overrides, layer.VarOverrides)
	sort.Strings(overrides)
	for _, ov := range overrides {
		if err := writeLengthPrefixedString(h, ov); err != nil {
			return fmt.Errorf("hash var override: %w", err)
		}
	}

	// src files and URLs from config
	cfg, err := config.LoadConfigRaw(layer.ConfigPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	for _, f := range cfg.Layer.Files {
		if f.Src != "" {
			if err := hashSrcPath(h, layer.ConfigPath, "file src", f.Src); err != nil {
				return err
			}
		}
		if f.URL != "" {
			// Length-prefixed for the same reason hashFile prefixes file
			// bytes: without a boundary, adjacent URLs (or a URL/src swap)
			// could concatenate to identical hash input.
			if err := writeLengthPrefixedString(h, f.URL); err != nil {
				return fmt.Errorf("hash url %s: %w", f.URL, err)
			}
		}
	}

	for _, r := range cfg.Layer.Repos {
		if r.Src != "" {
			if err := hashSrcPath(h, layer.ConfigPath, "repo src", r.Src); err != nil {
				return err
			}
		}
		if r.URL != "" {
			if err := writeLengthPrefixedString(h, r.URL); err != nil {
				return fmt.Errorf("hash repo url %s: %w", r.URL, err)
			}
		}
	}

	// Directory contents. Config-level option strings (Mode, Owner, Excludes,
	// PreserveOwnership, ContentsOnly) are already covered by hashing the
	// raw config bytes above — we only need to capture the host-side state
	// that the YAML can't see, plus the metadata that survives into the
	// resulting layer.
	for _, d := range cfg.Layer.Directories {
		if isTemplated(d.Src) {
			// See hashSrcPath — the real directory is only known after
			// rendering, so hash the template text and skip the walk.
			warnTemplatedSrc(layer.ConfigPath, "directory src", d.Src)
			if err := writeLengthPrefixedString(h, d.Src); err != nil {
				return err
			}
			continue
		}
		if err := hashDirectory(h, d); err != nil {
			return fmt.Errorf("hash directory %s: %w", d.Src, err)
		}
	}

	return nil
}

// isTemplated reports whether a value from the raw (unrendered) config was a
// template expression — LoadConfigRaw substitutes config.TemplatePlaceholder
// for every {{ ... }} directive before the YAML is parsed.
func isTemplated(s string) bool {
	return strings.Contains(s, config.TemplatePlaceholder)
}

// hashSrcPath folds a src path from the raw config into the hash. Literal
// paths are content-hashed via hashFile. Templated paths (the real value is
// only known after rendering) can't be opened here, so the template text
// itself is hashed instead — the config bytes already cover it, but hashing
// it again keeps this site self-contained. The trade-off is that changes to
// the *contents* of a templated src file do NOT invalidate the tag; we log a
// warning so users relying on tag-based caching know about the blind spot.
func hashSrcPath(h io.Writer, configPath, label, src string) error {
	if isTemplated(src) {
		warnTemplatedSrc(configPath, label, src)
		return writeLengthPrefixedString(h, src)
	}
	if err := hashFile(h, src); err != nil {
		return fmt.Errorf("hash %s %s: %w", label, src, err)
	}
	return nil
}

func warnTemplatedSrc(configPath, label, src string) {
	slog.With("component", "tag").Warn(
		"templated src path: contents not included in tag hash; edits to the referenced file will not change the computed tag",
		"config", configPath, "field", label, "src", src)
}

// hashDirectory walks dir.Src (filtered by dir.Excludes using buildah's own
// fileutils.PatternMatcher) and folds the host-side state that survives into
// the resulting layer into h.
//
// Per entry we hash:
//   - relative path (length-prefixed, slash-normalised so the hash is stable
//     across OSes)
//   - a type byte: 'f' regular file, 'd' directory, 'l' symlink
//   - regular files: contents via hashFile (length-prefixed bytes)
//   - symlinks: link target (length-prefixed)
//   - mode bits, IFF dir.Mode == "" (buildah preserves host modes in that case)
//   - UID/GID, IFF dir.PreserveOwnership && dir.Owner == "" (buildah preserves
//     host ownership in that case)
//
// Mtimes are deliberately excluded — they don't carry into the layer in a
// cache-relevant way for our pipeline and including them would invalidate
// every cache after a fresh git clone.
//
// Excluded directories are skipped wholesale via fs.SkipDir so we don't hash
// their contents. Buildah's own negated-pattern unexclusion
// (includeDirectoryAnyway) is not modelled here; negated patterns aren't a
// common need and would substantially complicate the hash. If we ever need
// them, the divergence becomes a CACHE-MISS (extra rebuild), not a
// cache-stale (silent wrong image), which is the safe direction.
func hashDirectory(h io.Writer, dir config.Directory) error {
	if dir.Src == "" {
		return nil
	}

	pm, err := fileutils.NewPatternMatcher(dir.Excludes)
	if err != nil {
		return fmt.Errorf("processing excludes %v: %w", dir.Excludes, err)
	}

	type entry struct {
		rel  string
		path string
		info fs.FileInfo
	}
	var entries []entry

	walkErr := filepath.WalkDir(dir.Src, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == dir.Src {
			return nil
		}
		rel, err := filepath.Rel(dir.Src, p)
		if err != nil {
			return err
		}
		excluded, err := pm.Matches(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		if excluded {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		entries = append(entries, entry{rel: rel, path: p, info: info})
		return nil
	})
	if walkErr != nil {
		return walkErr
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })

	preserveMode := dir.Mode == ""
	preserveOwner := dir.PreserveOwnership && dir.Owner == ""

	for _, e := range entries {
		if err := writeLengthPrefixedString(h, filepath.ToSlash(e.rel)); err != nil {
			return err
		}
		mode := e.info.Mode()
		switch {
		case mode&fs.ModeSymlink != 0:
			if _, err := h.Write([]byte{'l'}); err != nil {
				return err
			}
			target, err := os.Readlink(e.path)
			if err != nil {
				return err
			}
			if err := writeLengthPrefixedString(h, target); err != nil {
				return err
			}
		case mode.IsDir():
			if _, err := h.Write([]byte{'d'}); err != nil {
				return err
			}
		case mode.IsRegular():
			if _, err := h.Write([]byte{'f'}); err != nil {
				return err
			}
			if err := hashFile(h, e.path); err != nil {
				return err
			}
		default:
			// Devices, sockets, pipes — skip. Buildah's behavior on these is
			// quirky and they're effectively nonexistent in real source trees.
			continue
		}
		if preserveMode {
			var modeBuf [4]byte
			binary.BigEndian.PutUint32(modeBuf[:], uint32(mode.Perm()))
			if _, err := h.Write(modeBuf[:]); err != nil {
				return err
			}
		}
		if preserveOwner {
			uid, gid := unixOwnership(e.info)
			var idBuf [8]byte
			binary.BigEndian.PutUint32(idBuf[0:4], uid)
			binary.BigEndian.PutUint32(idBuf[4:8], gid)
			if _, err := h.Write(idBuf[:]); err != nil {
				return err
			}
		}
	}
	return nil
}

// unixOwnership pulls UID/GID out of a stat result. Both Linux and Darwin
// expose them via *syscall.Stat_t with identical field names. On any other
// platform we fall back to 0,0 — which is also the buildah default when
// PreserveOwnership is false, so the hash is still self-consistent.
func unixOwnership(info fs.FileInfo) (uint32, uint32) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0
	}
	return uint32(st.Uid), uint32(st.Gid)
}

// writeLengthPrefixedString prepends an 8-byte big-endian length so that
// adjacent strings can't collide with each other (same reasoning as
// hashFile's length prefix).
func writeLengthPrefixedString(h io.Writer, s string) error {
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], uint64(len(s)))
	if _, err := h.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err := io.WriteString(h, s)
	return err
}

// hashFile streams the file at path into h, length-prefixed.
//
// The length prefix prevents a theoretical collision where two layers have
// different (file, file) splits that concatenate to the same bytes — without
// a delimiter, hash(A || B) == hash(A' || B') is possible if A+B == A'+B'
// even when (A, B) ≠ (A', B'). Length-prefixing makes the byte boundary
// part of the hashed input so the split is unambiguous.
//
// MD5 collisions make this concern academic, but the fix is one writer call
// and removes the smell.
func hashFile(h io.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], uint64(info.Size()))
	if _, err := h.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err = io.Copy(h, f)
	return err
}
