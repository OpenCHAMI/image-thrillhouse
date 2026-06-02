// Package oscap provides OpenSCAP security scanning functionality.
// OpenSCAP enables security compliance checking and vulnerability assessment
// for container images using XCCDF benchmarks and OVAL definitions.
package oscap

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/travisbcotton/image-build/internal/config"
	"github.com/travisbcotton/image-build/internal/container"
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

// InstallSCAP installs OpenSCAP utilities into the container.
// It installs: openscap-utils, scap-security-guide, bzip2
// These packages are required for running security scans and handling OVAL files.
func (s *Scanner) InstallSCAP(ctx context.Context, c container.Container, pkgManager string) error {
	s.log.Info("Installing OpenSCAP packages")
	
	packages := []string{"openscap-utils", "scap-security-guide", "bzip2"}
	
	var cmd []string
	switch pkgManager {
	case "dnf":
		cmd = []string{"dnf", "install", "-y", "--nogpgcheck"}
		cmd = append(cmd, packages...)
	case "zypper":
		cmd = []string{"zypper", "install", "-y", "--no-gpg-checks"}
		cmd = append(cmd, packages...)
	case "apt":
		// Update package list first for APT, matching the builder's style.
		updateCmd := []string{"apt-get", "update", "-q"}
		out := container.NewBufLogWriter("stdout")
		if err := c.Run(ctx, updateCmd, container.RunModeContainer, out); err != nil {
			s.log.Warn("Failed to update apt cache", "error", err)
		}
		cmd = []string{"apt-get", "install", "-y", "-q", "--no-install-recommends"}
		cmd = append(cmd, packages...)
	default:
		return fmt.Errorf("unsupported package manager for OpenSCAP: %s", pkgManager)
	}
	
	out := container.NewBufLogWriter("stdout")
	if err := c.Run(ctx, cmd, container.RunModeContainer, out); err != nil {
		return fmt.Errorf("install OpenSCAP packages: %w", err)
	}
	
	s.log.Info("OpenSCAP packages installed successfully")
	return nil
}

// CheckInstall verifies that OpenSCAP is installed in the container.
// The hint included in the error depends on whether the user already asked
// install_scap to install it — saying "set install_scap: true" when they
// already did is just noise.
func (s *Scanner) CheckInstall(ctx context.Context, c container.Container) error {
	s.log.Info("Checking OpenSCAP installation")

	cmd := []string{"oscap", "--version"}
	out := container.NewBufLogWriter("stdout")

	if err := c.Run(ctx, cmd, container.RunModeContainer, out); err != nil {
		if s.cfg != nil && s.cfg.InstallSCAP {
			return fmt.Errorf("oscap --version failed after install_scap; installation may have failed: %w", err)
		}
		return fmt.Errorf("oscap not found in image; set install_scap: true or pre-install openscap-utils in the base image: %w", err)
	}

	s.log.Info("OpenSCAP is installed")
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
	s.log.Info("Running XCCDF security benchmark")
	
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
	
	s.log.Info("Running XCCDF evaluation", "profile", s.cfg.Profile, "benchmark", s.cfg.BenchmarkPath)
	out := container.NewBufLogWriter("stdout")
	
	// Note: SCAP scans often return non-zero exit codes even on successful scans
	// (when findings are discovered), so we continue even if there's an error
	if err := c.Run(ctx, evalCmd, container.RunModeContainer, out); err != nil {
		s.log.Warn("XCCDF evaluation completed with findings", "error", err)
	}
	
	s.log.Info("XCCDF evaluation complete", "results", resultsPath)
	
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
	
	s.log.Info("Generating remediation script", "output", remediatePath)
	out := container.NewBufLogWriter("stdout")
	
	if err := c.Run(ctx, cmd, container.RunModeContainer, out); err != nil {
		s.log.Warn("Failed to generate remediation script", "error", err)
		return nil // Don't fail the build if remediation generation fails
	}
	
	s.log.Info("Remediation script generated", "path", remediatePath)
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
	s.log.Info("Running OVAL vulnerability evaluation")
	
	if s.cfg.OVALUrl == "" {
		return fmt.Errorf("oval_url is required for OVAL evaluation")
	}
	
	ovalXMLPath := "/root/oval.xml"
	ovalResultPath := s.cfg.OVALResultPath
	if ovalResultPath == "" {
		ovalResultPath = "/root/vulnerabilities.xml"
	}
	
	// Download and decompress OVAL definitions
	downloadCmd := fmt.Sprintf("curl -L -o - %s | bzip2 --decompress > %s", s.cfg.OVALUrl, ovalXMLPath)
	s.log.Info("Downloading OVAL definitions", "url", s.cfg.OVALUrl)
	
	out := container.NewBufLogWriter("stdout")
	if err := c.RunScript(ctx, downloadCmd, out); err != nil {
		return fmt.Errorf("download OVAL definitions: %w", err)
	}
	
	// Run OVAL evaluation
	evalCmd := []string{
		"oscap", "oval", "eval",
		"--report", ovalResultPath,
		ovalXMLPath,
	}
	
	s.log.Info("Running OVAL evaluation")
	out = container.NewBufLogWriter("stdout")
	
	// OVAL evaluations return non-zero when vulnerabilities are found
	if err := c.Run(ctx, evalCmd, container.RunModeContainer, out); err != nil {
		s.log.Warn("OVAL evaluation completed with vulnerabilities found", "error", err)
	}
	
	s.log.Info("OVAL evaluation complete", "results", ovalResultPath)
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
