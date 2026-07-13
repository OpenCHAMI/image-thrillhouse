// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package buildah

import (
	"testing"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

// buildNamespaceOptions must default to host networking so a plain image
// build doesn't depend on a configured netavark/CNI stack — the failure mode
// ("did not get container create message from subprocess: EOF") this override
// exists to prevent.
func TestBuildNamespaceOptions_DefaultsToHostNetwork(t *testing.T) {
	t.Setenv("BUILDAH_HOST_NETWORK", "")
	opts := buildNamespaceOptions()
	net := opts.Find(string(specs.NetworkNamespace))
	if net == nil {
		t.Fatal("expected a network namespace option to be set")
	}
	if !net.Host {
		t.Error("network namespace should default to host to skip per-container netavark setup")
	}
}

// The escape hatch must genuinely fall back to buildah's defaults (nil
// overrides), so a host with a working netavark/CNI setup can opt back into a
// private network namespace.
func TestBuildNamespaceOptions_OptOut(t *testing.T) {
	for _, v := range []string{"false", "0"} {
		t.Setenv("BUILDAH_HOST_NETWORK", v)
		if opts := buildNamespaceOptions(); opts != nil {
			t.Errorf("BUILDAH_HOST_NETWORK=%q should disable the host-network override, got %+v", v, opts)
		}
	}
}
