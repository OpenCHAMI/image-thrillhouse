// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package idmap

import (
	"slices"
	"testing"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"go.podman.io/storage/pkg/idtools"
)

func TestParseExtraIDs(t *testing.T) {
	tests := []struct {
		name    string
		spec    string
		want    []uint32
		wantErr bool
	}{
		{name: "empty", spec: "", want: nil},
		{name: "blank", spec: "   ", want: nil},
		{name: "single", spec: "65534", want: []uint32{65534}},
		{name: "list", spec: "65534, 60000,1000", want: []uint32{1000, 60000, 65534}},
		{name: "range", spec: "65530-65534", want: []uint32{65530, 65531, 65532, 65533, 65534}},
		{name: "mixed and deduped", spec: "65534,65530-65534", want: []uint32{65530, 65531, 65532, 65533, 65534}},
		{name: "empty tokens ignored", spec: "65534,,", want: []uint32{65534}},
		{name: "not a number", spec: "abc", wantErr: true},
		{name: "reversed range", spec: "10-5", wantErr: true},
		{name: "overflows uint32", spec: "4294967296", wantErr: true},
		{name: "range too large", spec: "0-100000", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseExtraIDs(tt.spec)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseExtraIDs(%q) err = %v, wantErr %v", tt.spec, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("parseExtraIDs(%q) = %v, want %v", tt.spec, got, tt.want)
			}
		})
	}
}

// defaultBase mirrors what idtools produces for a single "builder:2000:50000"
// subordinate range: container ids 0..49999 → host 2000..51999.
func defaultBase() []idtools.IDMap {
	return []idtools.IDMap{{ContainerID: 0, HostID: 2000, Size: 50000}}
}

func TestSplice_NoExtras(t *testing.T) {
	got, err := splice(defaultBase(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []specs.LinuxIDMapping{{ContainerID: 0, HostID: 2000, Size: 50000}}
	if !slices.Equal(got, want) {
		t.Errorf("splice with no extras = %v, want unchanged %v", got, want)
	}
}

func TestSplice_NogroupCarvesTopOfRange(t *testing.T) {
	got, err := splice(defaultBase(), []uint32{65534})
	if err != nil {
		t.Fatal(err)
	}
	want := []specs.LinuxIDMapping{
		{ContainerID: 0, HostID: 2000, Size: 49999},  // block shrunk by one
		{ContainerID: 65534, HostID: 51999, Size: 1}, // gid 65534 → freed top id
	}
	if !slices.Equal(got, want) {
		t.Errorf("splice(65534) = %v, want %v", got, want)
	}
	assertWithinAllocation(t, got, 2000, 50000)
}

func TestSplice_MultipleExtrasDistinctHostIDs(t *testing.T) {
	got, err := splice(defaultBase(), []uint32{65533, 65534})
	if err != nil {
		t.Fatal(err)
	}
	// Two ids borrowed from the top of the block, each with a distinct host id.
	want := []specs.LinuxIDMapping{
		{ContainerID: 0, HostID: 2000, Size: 49998},
		{ContainerID: 65533, HostID: 51999, Size: 1},
		{ContainerID: 65534, HostID: 51998, Size: 1},
	}
	if !slices.Equal(got, want) {
		t.Errorf("splice(65533,65534) = %v, want %v", got, want)
	}
	assertWithinAllocation(t, got, 2000, 50000)
	assertNoHostCollision(t, got)
}

func TestSplice_AlreadyCoveredIsSkipped(t *testing.T) {
	got, err := splice(defaultBase(), []uint32{1000})
	if err != nil {
		t.Fatal(err)
	}
	// 1000 is inside the contiguous block, so the map is left untouched.
	want := []specs.LinuxIDMapping{{ContainerID: 0, HostID: 2000, Size: 50000}}
	if !slices.Equal(got, want) {
		t.Errorf("splice(1000) = %v, want unchanged %v", got, want)
	}
}

func TestSplice_ExhaustedRange(t *testing.T) {
	// A range with a single id has nothing to spare for a sparse entry.
	base := []idtools.IDMap{{ContainerID: 0, HostID: 2000, Size: 1}}
	if _, err := splice(base, []uint32{65534}); err == nil {
		t.Fatal("expected error when subordinate range is exhausted")
	}
}

// assertWithinAllocation verifies every mapped host id stays inside the
// [start, start+size) subordinate allocation, which is what newuidmap/newgidmap
// require.
func assertWithinAllocation(t *testing.T, maps []specs.LinuxIDMapping, start, size uint32) {
	t.Helper()
	for _, m := range maps {
		if m.HostID < start || m.HostID+m.Size > start+size {
			t.Errorf("mapping %v escapes allocation [%d,%d)", m, start, start+size)
		}
	}
}

// assertNoHostCollision verifies no two mappings claim the same host id.
func assertNoHostCollision(t *testing.T, maps []specs.LinuxIDMapping) {
	t.Helper()
	seen := make(map[uint32]bool)
	for _, m := range maps {
		for h := m.HostID; h < m.HostID+m.Size; h++ {
			if seen[h] {
				t.Errorf("host id %d mapped more than once", h)
			}
			seen[h] = true
		}
	}
}
