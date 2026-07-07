// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package tag

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"syscall"

	"go.podman.io/storage/pkg/fileutils"

	"github.com/travisbcotton/image-thrillhouse/internal/config"
)

// SelfTagSentinel is the value bound to {{ .tag }} when a config is rendered
// for hashing: a layer's tag cannot participate in its own hash, so the
// hash-input render substitutes this fixed string. The build render uses the
// real computed tag; it is the only variable the two renders differ on.
const SelfTagSentinel = "__self_tag__"

// TagHexLen is the length of a computed tag: sha256 truncated to 128 bits
// (32 hex chars), keeping tags short enough to compose into registry tags.
const TagHexLen = 32

// LayerInput is one fully rendered layer to hash. Rendered is the config text
// after template execution with {{ .tag }} bound to SelfTagSentinel and all
// other variables (var files, --var, parent tags, arch) bound to their real
// values. Cfg is the parse of Rendered — used to locate the src files, URLs,
// and directories whose content participates in the hash. Relative src paths
// resolve against the process working directory, same as the build itself.
type LayerInput struct {
	ConfigPath string // used in error messages only
	Rendered   string
	Cfg        *config.Config
}

// Compute returns the layer's deterministic tag. Ancestry is chained
// Merkle-style: each parent's already-computed tag is folded in (in
// DependsOn order), so any change anywhere in the ancestry propagates to
// every descendant without re-hashing ancestor content here.
func Compute(layer LayerInput, parentTags []string) (string, error) {
	h := sha256.New()

	for _, pt := range parentTags {
		if err := writeLengthPrefixedString(h, pt); err != nil {
			return "", fmt.Errorf("hash parent tag: %w", err)
		}
	}

	if err := hashLayer(h, layer); err != nil {
		return "", fmt.Errorf("hash layer %s: %w", layer.ConfigPath, err)
	}

	return fmt.Sprintf("%x", h.Sum(nil))[:TagHexLen], nil
}

func hashLayer(h io.Writer, layer LayerInput) error {
	if err := writeLengthPrefixedString(h, layer.Rendered); err != nil {
		return fmt.Errorf("hash rendered config: %w", err)
	}

	// Host-side content the rendered config references. The config text
	// itself (paths, URLs, option fields) is already covered by hashing
	// Rendered above; this captures the file bytes the YAML can't see.
	for _, f := range layer.Cfg.Layer.Files {
		if f.Src != "" {
			if err := hashFile(h, f.Src); err != nil {
				return fmt.Errorf("hash file src %s: %w", f.Src, err)
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

	for _, r := range layer.Cfg.Layer.Repos {
		if r.Src != "" {
			if err := hashFile(h, r.Src); err != nil {
				return fmt.Errorf("hash repo src %s: %w", r.Src, err)
			}
		}
		if r.URL != "" {
			if err := writeLengthPrefixedString(h, r.URL); err != nil {
				return fmt.Errorf("hash repo url %s: %w", r.URL, err)
			}
		}
	}

	for _, d := range layer.Cfg.Layer.Directories {
		if err := hashDirectory(h, d); err != nil {
			return fmt.Errorf("hash directory %s: %w", d.Src, err)
		}
	}

	return nil
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
