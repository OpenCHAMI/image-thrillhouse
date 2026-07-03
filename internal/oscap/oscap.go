// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

// Package oscap provides OpenSCAP security scanning functionality.
// OpenSCAP enables security compliance checking and vulnerability assessment
// for container images using XCCDF benchmarks and OVAL definitions.
package oscap

import (
	"bytes"
	"compress/bzip2"
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/travisbcotton/image-thrillhouse/internal/config"
	"github.com/travisbcotton/image-thrillhouse/internal/container"
	"github.com/travisbcotton/image-thrillhouse/internal/fetch"
)

// Scanner handles OpenSCAP security scanning operations.
// It can install OpenSCAP tools, run security benchmarks, and evaluate vulnerabilities.
type Scanner struct {
	cfg *config.OpenSCAP
	log *slog.Logger
}

// New creates a new OpenSCAP scanner with the given configuration.
func New(cfg *config.OpenSCAP) *Scanner {
	return &Scanner{
		cfg: cfg,
		log: slog.With("component", "oscap"),
	}
}

// InstallSCAP installs OpenSCAP utilities into the container: the oscap
// scanner (openscap-utils), the SCAP Security Guide content, and bzip2 for
// OVAL archives. The SCAP Security Guide package name differs per ecosystem:
// RPM distros ship "scap-security-guide", while Debian/Ubuntu split the same
// content into ssg-* packages.
func (s *Scanner) InstallSCAP(ctx context.Context, c container.Container, pkgManager string) error {
	s.log.Info("installing openscap packages")

	rpmPackages := []string{"openscap-utils", "scap-security-guide", "bzip2"}

	var cmd []string
	switch pkgManager {
	case "dnf":
		cmd = []string{"dnf", "install", "-y", "--nogpgcheck"}
		cmd = append(cmd, rpmPackages...)
	case "zypper":
		// --no-gpg-checks is a zypper GLOBAL option and must precede the
		// `install` subcommand; zypper rejects it in the command position.
		cmd = []string{"zypper", "--no-gpg-checks", "install", "-y"}
		cmd = append(cmd, rpmPackages...)
	case "apt":
		// Update package list first for APT, matching the builder's style.
		updateCmd := []string{"apt-get", "update", "-q"}
		if err := container.RunCmd(ctx, c, "oscap", updateCmd, container.RunModeContainer); err != nil {
			s.log.Warn("failed to update apt cache", "error", err)
		}
		cmd = []string{"apt-get", "install", "-y", "-q", "--no-install-recommends"}
		// Debian/Ubuntu have no "scap-security-guide" package; the SSG
		// content lives in ssg-base (shared) + ssg-debian / ssg-debderived
		// (Debian and Ubuntu profiles respectively; both are small
		// arch-independent content packages, so install both).
		cmd = append(cmd, "openscap-utils", "bzip2", "ssg-base", "ssg-debian", "ssg-debderived")
	default:
		return fmt.Errorf("unsupported package manager for OpenSCAP: %s", pkgManager)
	}

	if err := container.RunCmd(ctx, c, "oscap", cmd, container.RunModeContainer); err != nil {
		return fmt.Errorf("install OpenSCAP packages: %w", err)
	}

	s.log.Info("openscap packages installed")
	return nil
}

// CheckInstall verifies that OpenSCAP is installed in the container.
// The hint included in the error depends on whether the user already asked
// install_scap to install it — saying "set install_scap: true" when they
// already did is just noise.
func (s *Scanner) CheckInstall(ctx context.Context, c container.Container) error {
	s.log.Info("checking openscap installation")

	cmd := []string{"oscap", "--version"}
	if err := container.RunCmd(ctx, c, "oscap", cmd, container.RunModeContainer); err != nil {
		if s.cfg != nil && s.cfg.InstallSCAP {
			return fmt.Errorf("oscap --version failed after install_scap; installation may have failed: %w", err)
		}
		return fmt.Errorf("oscap not found in image; set install_scap: true or pre-install openscap-utils in the base image: %w", err)
	}

	s.log.Info("openscap is installed")
	return nil
}

// RunBenchmark runs an XCCDF security benchmark scan.
// It evaluates the system against a security profile and generates results.
//
// Required configuration:
//   - profile: The SCAP profile to evaluate against
//   - benchmark_path: Path to the XCCDF XML file
//
// Results are saved to results_path (default: /root/scan.xml)
func (s *Scanner) RunBenchmark(ctx context.Context, c container.Container) error {
	s.log.Info("running xccdf security benchmark")

	if s.cfg.Profile == "" {
		return fmt.Errorf("profile is required for SCAP benchmark")
	}
	if s.cfg.BenchmarkPath == "" {
		return fmt.Errorf("benchmark_path is required for SCAP benchmark")
	}

	resultsPath := s.cfg.ResultsPath
	if resultsPath == "" {
		resultsPath = "/root/scan.xml"
	}

	// Run the XCCDF evaluation
	evalCmd := []string{
		"oscap", "xccdf", "eval",
		"--fetch-remote-resources",
		"--profile", s.cfg.Profile,
		"--results", resultsPath,
		s.cfg.BenchmarkPath,
	}

	s.log.Info("running xccdf evaluation", "profile", s.cfg.Profile, "benchmark", s.cfg.BenchmarkPath)

	// Note: SCAP scans often return non-zero exit codes even on successful scans
	// (when findings are discovered), so we continue even if there's an error
	if err := container.RunCmd(ctx, c, "oscap", evalCmd, container.RunModeContainer); err != nil {
		s.log.Warn("xccdf evaluation completed with findings", "error", err)
	}

	s.log.Info("xccdf evaluation complete", "results", resultsPath)

	// Generate remediation script
	return s.generateRemediation(ctx, c, resultsPath)
}

// generateRemediation creates a remediation script from scan results.
// This script can be used to automatically fix security findings.
func (s *Scanner) generateRemediation(ctx context.Context, c container.Container, resultsPath string) error {
	remediatePath := s.cfg.RemediatePath
	if remediatePath == "" {
		remediatePath = "/root/remediate.sh"
	}

	cmd := []string{
		"oscap", "xccdf", "generate", "fix",
		"--output", remediatePath,
		"--profile", s.cfg.Profile,
		resultsPath,
	}

	s.log.Info("generating remediation script", "output", remediatePath)

	if err := container.RunCmd(ctx, c, "oscap", cmd, container.RunModeContainer); err != nil {
		s.log.Warn("failed to generate remediation script", "error", err)
		return nil // Don't fail the build if remediation generation fails
	}

	s.log.Info("remediation script generated", "path", remediatePath)
	return nil
}

// RunOVALEval runs an OVAL (Open Vulnerability Assessment Language) evaluation.
// OVAL checks for known vulnerabilities (CVEs) in installed packages.
//
// Required configuration:
//   - oval_url: URL to download OVAL definitions (usually .bz2 compressed)
//
// Results are saved to oval_result_path (default: /root/vulnerabilities.xml)
func (s *Scanner) RunOVALEval(ctx context.Context, c container.Container) error {
	s.log.Info("running oval vulnerability evaluation")

	if s.cfg.OVALUrl == "" {
		return fmt.Errorf("oval_url is required for OVAL evaluation")
	}

	ovalXMLPath := "/root/oval.xml"
	ovalResultPath := s.cfg.OVALResultPath
	if ovalResultPath == "" {
		ovalResultPath = "/root/vulnerabilities.xml"
	}

	// Fetch and decompress OVAL definitions on the host, then write the
	// resulting XML into the container. Doing the work in Go avoids piping a
	// user-supplied URL through `sh -c`, which was a shell-injection sink, and
	// removes the implicit dependency on curl + bzip2 being present in the
	// container.
	s.log.Info("downloading oval definitions", "url", s.cfg.OVALUrl)
	ovalXML, err := fetchOVAL(ctx, s.cfg.OVALUrl)
	if err != nil {
		return fmt.Errorf("download OVAL definitions: %w", err)
	}
	if err := c.WriteFile(ctx, config.File{Path: ovalXMLPath, Content: string(ovalXML)}); err != nil {
		return fmt.Errorf("write OVAL definitions: %w", err)
	}

	// Run OVAL evaluation
	evalCmd := []string{
		"oscap", "oval", "eval",
		"--report", ovalResultPath,
		ovalXMLPath,
	}

	s.log.Info("running oval evaluation")

	// OVAL evaluations return non-zero when vulnerabilities are found
	if err := container.RunCmd(ctx, c, "oscap", evalCmd, container.RunModeContainer); err != nil {
		s.log.Warn("oval evaluation completed with vulnerabilities found", "error", err)
	}

	s.log.Info("oval evaluation complete", "results", ovalResultPath)
	return nil
}

// Run executes the full OpenSCAP workflow based on configuration.
// It handles installation, benchmark scanning, and OVAL evaluation as configured.
func (s *Scanner) Run(ctx context.Context, c container.Container, pkgManager string) error {
	// Install OpenSCAP if requested
	if s.cfg.InstallSCAP {
		if err := s.InstallSCAP(ctx, c, pkgManager); err != nil {
			return fmt.Errorf("install SCAP: %w", err)
		}
	}

	// Verify OpenSCAP is installed
	if err := s.CheckInstall(ctx, c); err != nil {
		return err
	}

	// Run XCCDF benchmark if requested
	if s.cfg.SCAPBenchmark {
		if err := s.RunBenchmark(ctx, c); err != nil {
			return fmt.Errorf("SCAP benchmark: %w", err)
		}
	}

	// Run OVAL evaluation if requested
	if s.cfg.OVALEval {
		if err := s.RunOVALEval(ctx, c); err != nil {
			return fmt.Errorf("OVAL evaluation: %w", err)
		}
	}

	return nil
}

// Size caps for fetchOVAL. Both are enforced because bzip2 is famously
// asymmetric — a small compressed body can decompress to many GB and OOM the
// process. The compressed cap stops the network read; the decompressed cap
// stops the bzip2 decoder. Both are vars (not consts) so tests can shrink
// them without staging GB of test data.
//
// Defaults are chosen so real-world OVAL definitions (RHEL/Rocky are
// typically <20 MiB compressed, <500 MiB decompressed) round-trip fine while
// pathological inputs surface as a clear error instead of an OOM.
var (
	maxOVALCompressed   int64 = 256 * 1024 * 1024      // 256 MiB
	maxOVALDecompressed int64 = 2 * 1024 * 1024 * 1024 // 2 GiB
)

// fetchOVAL downloads an OVAL definitions file from url and returns the
// decoded XML bytes. The on-disk artifacts published by upstreams are
// typically bzip2-compressed (.bz2), but plain .xml is also accepted —
// callers shouldn't have to special-case the extension. Detection is by
// the bzip2 magic bytes ("BZh"); on a miss the body is returned verbatim.
//
// The HTTP body and the bzip2 output stream are both bounded — see the
// maxOVAL* package vars. Hitting either cap surfaces as an error rather
// than a truncated payload, so callers can't silently process a partial
// OVAL definition.
//
// Memory note: the decoded XML is materialised in full to return []byte.
// For unusually large OVAL inputs, streaming to a temp file + passing that
// path via config.File.Src would cut peak RSS — left as a follow-up.
func fetchOVAL(ctx context.Context, url string) ([]byte, error) {
	body, err := fetch.GetStream(ctx, url)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	// Cap the compressed bytes coming off the wire. LimitReader by (cap + 1)
	// so we can distinguish "exactly the cap" from "too big".
	limitedBody := io.LimitReader(body, maxOVALCompressed+1)

	head := make([]byte, 3)
	n, err := io.ReadFull(limitedBody, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, fmt.Errorf("read OVAL header: %w", err)
	}
	head = head[:n]
	combined := io.MultiReader(bytes.NewReader(head), limitedBody)

	var reader io.Reader = combined
	if n == 3 && string(head) == "BZh" {
		reader = bzip2.NewReader(combined)
	}

	// Cap the decoded stream. For the passthrough path this is the same as
	// the compressed cap; for bzip2 it's the much-larger decompressed cap.
	out, err := io.ReadAll(io.LimitReader(reader, maxOVALDecompressed+1))
	if err != nil {
		return nil, fmt.Errorf("decode OVAL body: %w", err)
	}
	// Distinguish "we hit the compressed cap" from "we hit the decompressed
	// cap" so the error message points at the right knob.
	if n == 3 && string(head) == "BZh" {
		if int64(len(out)) > maxOVALDecompressed {
			return nil, fmt.Errorf("decode OVAL body: decompressed size exceeds %d-byte cap", maxOVALDecompressed)
		}
	} else {
		if int64(len(out)) > maxOVALCompressed {
			return nil, fmt.Errorf("fetch OVAL body: size exceeds %d-byte cap", maxOVALCompressed)
		}
	}
	return out, nil
}
