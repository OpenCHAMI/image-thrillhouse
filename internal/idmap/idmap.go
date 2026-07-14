// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

// Package idmap builds the uid/gid mapping the rootless builder hands to
// buildah when an image needs ids that fall outside the default contiguous
// subordinate range.
//
// Background: the rootless builder maps a single contiguous subordinate range
// (builder:2000:50000 by default, see the Dockerfile) into the build user
// namespace, so only container ids 0..49999 exist. Images that assign ids above
// that ceiling — most commonly Debian/Ubuntu's `nogroup` (gid 65534) — have no
// mapping, and any `chown user:group` against them fails with EINVAL
// ("Invalid argument").
//
// Simply widening the contiguous range often isn't possible: it has to fit
// inside the outer podman user namespace (typically 65536 ids), and with a
// start offset of 2000 the reachable ceiling sits below 65534. Instead we keep
// the contiguous block intact and splice in *sparse* single-id mappings for the
// specific high ids a build needs, borrowing one host id per entry from the top
// of the range. Every mapped host id stays inside the original /etc/subuid
// allocation, so newuidmap/newgidmap accept the map without any host change.
package idmap

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"go.podman.io/storage/pkg/idtools"
)

// maxExtraIDs caps how many sparse ids we accept. Each spliced id borrows one id
// from the contiguous block, so an unbounded range could decimate it; this keeps
// a fat-fingered "0-4000000000" from doing so.
const maxExtraIDs = 4096

// Build returns the uid/gid maps to hand buildah for the build namespace, given
// the raw THRILLHOUSE_EXTRA_UIDS / _GIDS values. It reproduces buildah's default
// rootless map from the subordinate ranges owned by username (mirroring
// go.podman.io/storage, which reads /etc/subuid and /etc/subgid keyed by the
// username for both) and splices in a single-id entry for each requested id the
// default map doesn't already cover.
//
// ok is false — with nil maps and no error — when neither spec requests
// anything; callers should then leave buildah's IDMappingOptions nil so it
// derives the default mapping itself, preserving prior behaviour exactly.
func Build(username, extraUIDSpec, extraGIDSpec string) (uidMap, gidMap []specs.LinuxIDMapping, ok bool, err error) {
	extraUIDs, err := parseExtraIDs(extraUIDSpec)
	if err != nil {
		return nil, nil, false, fmt.Errorf("parse extra uids: %w", err)
	}
	extraGIDs, err := parseExtraIDs(extraGIDSpec)
	if err != nil {
		return nil, nil, false, fmt.Errorf("parse extra gids: %w", err)
	}
	if len(extraUIDs) == 0 && len(extraGIDs) == 0 {
		return nil, nil, false, nil
	}

	base, err := idtools.NewIDMappings(username, username)
	if err != nil {
		return nil, nil, false, fmt.Errorf("read subordinate id ranges for %q: %w", username, err)
	}

	// Both maps must be non-empty for buildah to use them (it falls back to host
	// mapping otherwise), so splice each dimension even when only one has extras.
	uidMap, err = splice(base.UIDs(), extraUIDs)
	if err != nil {
		return nil, nil, false, fmt.Errorf("uid map: %w", err)
	}
	gidMap, err = splice(base.GIDs(), extraGIDs)
	if err != nil {
		return nil, nil, false, fmt.Errorf("gid map: %w", err)
	}
	return uidMap, gidMap, true, nil
}

// parseExtraIDs parses a comma-separated list of ids and inclusive "lo-hi"
// ranges into a sorted, de-duplicated slice. An empty/blank spec yields no ids.
func parseExtraIDs(spec string) ([]uint32, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}
	seen := make(map[uint32]struct{})
	for _, tok := range strings.Split(spec, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		loStr, hiStr, isRange := strings.Cut(tok, "-")
		start, err := parseID(loStr)
		if err != nil {
			return nil, err
		}
		end := start
		if isRange {
			if end, err = parseID(hiStr); err != nil {
				return nil, err
			}
			if end < start {
				return nil, fmt.Errorf("invalid range %q: end before start", tok)
			}
		}
		if int(end-start)+1 > maxExtraIDs {
			return nil, fmt.Errorf("range %q spans more than %d ids", tok, maxExtraIDs)
		}
		for id := start; ; id++ {
			seen[id] = struct{}{}
			if id == end {
				break
			}
		}
		if len(seen) > maxExtraIDs {
			return nil, fmt.Errorf("more than %d extra ids requested", maxExtraIDs)
		}
	}
	out := make([]uint32, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	slices.Sort(out)
	return out, nil
}

func parseID(s string) (uint32, error) {
	v, err := strconv.ParseUint(strings.TrimSpace(s), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid id %q", s)
	}
	return uint32(v), nil
}

// splice returns buildah's contiguous rootless map with an extra single-id
// mapping added for each requested id the contiguous block doesn't already
// cover. Each new entry borrows one host id from the top of the largest range
// that can spare it, so every mapped host id stays inside the original
// /etc/subuid allocation.
func splice(base []idtools.IDMap, extra []uint32) ([]specs.LinuxIDMapping, error) {
	out := make([]specs.LinuxIDMapping, 0, len(base)+len(extra))
	for _, m := range base {
		out = append(out, specs.LinuxIDMapping{
			ContainerID: uint32(m.ContainerID),
			HostID:      uint32(m.HostID),
			Size:        uint32(m.Size),
		})
	}

	covered := func(id uint32) bool {
		for _, m := range out {
			if id >= m.ContainerID && id-m.ContainerID < m.Size {
				return true
			}
		}
		return false
	}

	for _, id := range extra {
		if covered(id) {
			continue // already mapped by the contiguous block
		}
		// Borrow one host id from the top of the largest range that can spare it.
		// Size-1 entries (our own sparse entries) are never donors.
		donor := -1
		for i := range out {
			if out[i].Size < 2 {
				continue
			}
			if donor == -1 || out[i].Size > out[donor].Size {
				donor = i
			}
		}
		if donor == -1 {
			return nil, fmt.Errorf("no room to map id %d: subordinate range exhausted", id)
		}
		out[donor].Size--
		out = append(out, specs.LinuxIDMapping{
			ContainerID: id,
			HostID:      out[donor].HostID + out[donor].Size,
			Size:        1,
		})
	}
	return out, nil
}
