package builder

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/travisbcotton/image-build/internal/config"
	"github.com/travisbcotton/image-build/internal/container"
)

// runAnsibleCommand executes an Ansible playbook from the HOST system
// using the containers.podman.buildah connection plugin.
//
// This approach:
// 1. Runs Ansible on the host (not in container)
// 2. Uses Buildah connection plugin to target the container
// 3. Auto-generates dynamic inventory adding container to specified groups
// 4. Merges with existing inventory if provided
//
// Benefits:
// - No Ansible installation in container (smaller images)
// - Access to host's Ansible Galaxy roles and collections
// - True parity with Python image-builder's approach
func (b *Builder) runAnsibleCommand(ctx context.Context, c container.Container, ansible *config.AnsibleCommand) error {
	log := slog.With("component", "builder", "subsystem", "ansible")

	log.Info("Starting Ansible playbook execution (host-based)",
		"playbook", ansible.Playbook,
		"groups", ansible.Groups,
		"inventory", ansible.Inventory)

	// Get container name from Container interface
	containerName := c.GetName()

	// Verify Ansible is installed on host
	if err := b.ensureAnsibleInstalledOnHost(); err != nil {
		return fmt.Errorf("ansible not available on host: %w", err)
	}

	// Verify Buildah connection plugin is available
	if err := b.ensureBuildahPluginAvailable(); err != nil {
		return fmt.Errorf("buildah connection plugin not available: %w", err)
	}

	// Debug: List all buildah containers to verify our container exists
	// This helps diagnose graph root mismatches when running as non-root
	if err := b.listBuildahContainers(containerName); err != nil {
		log.Warn("Failed to list buildah containers", "error", err)
	}

	// Generate dynamic inventory with container in specified groups
	dynamicInventory, err := b.generateDynamicInventory(containerName, ansible.Groups)
	if err != nil {
		return fmt.Errorf("generate dynamic inventory: %w", err)
	}

	log.Debug("Generated dynamic inventory",
		"container", containerName,
		"groups", ansible.Groups,
		"inventory", dynamicInventory)

	// Build ansible-playbook command
	ansibleCmd, err := b.buildAnsibleHostCommand(ansible, containerName)
	if err != nil {
		return fmt.Errorf("build ansible command: %w", err)
	}

	log.Info("Executing ansible-playbook on host",
		"container", containerName,
		"limit", ansible.Limit)

	// Execute ansible-playbook on host with dynamic inventory
	if err := b.executeAnsibleOnHost(ctx, ansibleCmd, dynamicInventory, ansible.Playbook); err != nil {
		return fmt.Errorf("ansible-playbook failed: %w", err)
	}

	log.Info("Ansible playbook execution completed successfully")
	return nil
}

// ensureAnsibleInstalledOnHost verifies ansible-playbook is available on the host.
func (b *Builder) ensureAnsibleInstalledOnHost() error {
	cmd := exec.Command("ansible-playbook", "--version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ansible-playbook not found on host system. Install with:\n" +
			"  # RHEL/Rocky/Fedora:\n" +
			"  sudo dnf install ansible-core\n" +
			"  # Debian/Ubuntu:\n" +
			"  sudo apt install ansible-core\n" +
			"  # Or via pip:\n" +
			"  pip install ansible-core")
	}
	return nil
}

// listBuildahContainers lists all buildah containers and checks if our target container exists.
// This helps diagnose issues with non-root users and graph root mismatches.
func (b *Builder) listBuildahContainers(targetContainer string) error {
	log := slog.With("component", "builder", "subsystem", "ansible")

	// Run buildah containers to list all containers
	cmd := exec.Command("buildah", "containers", "--format", "{{.ContainerName}}")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("buildah containers failed: %w (output: %s)", err, string(output))
	}

	containers := strings.Split(strings.TrimSpace(string(output)), "\n")

	// Filter out empty strings
	var nonEmptyContainers []string
	for _, name := range containers {
		if name != "" {
			nonEmptyContainers = append(nonEmptyContainers, name)
		}
	}

	// Log all containers found
	log.Info("Buildah containers list (CLI)",
		"count", len(nonEmptyContainers),
		"containers", nonEmptyContainers,
		"target", targetContainer)

	// Also get detailed container info
	detailCmd := exec.Command("buildah", "containers", "--format", "{{.ContainerName}}|{{.ContainerID}}|{{.Builder}}")
	detailOutput, err := detailCmd.CombinedOutput()
	if err == nil {
		log.Debug("Buildah containers details", "output", strings.TrimSpace(string(detailOutput)))
	}

	// Check if our target container is in the list
	found := false
	for _, name := range nonEmptyContainers {
		if name == targetContainer {
			found = true
			break
		}
	}

	if !found && len(nonEmptyContainers) > 0 {
		log.Warn("Target container not found in buildah containers list",
			"target", targetContainer,
			"available", nonEmptyContainers)
		return fmt.Errorf("container %q not found in buildah containers (found: %v)", targetContainer, nonEmptyContainers)
	}

	if !found && len(nonEmptyContainers) == 0 {
		log.Warn("No containers found in buildah containers list - possible graph root mismatch",
			"target", targetContainer)
		return fmt.Errorf("no containers found via 'buildah containers' - this indicates a storage/graph root mismatch")
	}

	if found {
		log.Info("Target container confirmed in buildah containers", "container", targetContainer)
	}

	// Also check graph root for non-root users
	graphRootCmd := exec.Command("buildah", "info", "--format", "{{.store.GraphRoot}}")
	graphOutput, err := graphRootCmd.CombinedOutput()
	if err == nil {
		cliGraphRoot := strings.TrimSpace(string(graphOutput))
		log.Info("Buildah graph root (CLI)", "path", cliGraphRoot)

		// Compare with Go library's graph root (if we can get it)
		// The Go library graph root was logged in openStore()
	} else {
		log.Warn("Failed to get buildah graph root", "error", err)
	}

	return nil
}

// ensureBuildahPluginAvailable verifies the buildah connection plugin is installed.
func (b *Builder) ensureBuildahPluginAvailable() error {
	// Check if the plugin is available via ansible-doc
	cmd := exec.Command("ansible-doc", "-t", "connection", "containers.podman.buildah")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("buildah connection plugin not found. Install with:\n" +
			"  ansible-galaxy collection install containers.podman")
	}
	return nil
}

// generateDynamicInventory creates a YAML inventory that adds the container
// to the specified Ansible groups using the buildah connection plugin.
//
// Example output:
//
//	all:
//	  children:
//	    web:
//	      hosts:
//	        my-container:
//	          ansible_connection: containers.podman.buildah
//	    db:
//	      hosts:
//	        my-container:
//	          ansible_connection: containers.podman.buildah
func (b *Builder) generateDynamicInventory(containerName string, groups []string) (string, error) {
	if len(groups) == 0 {
		return "", fmt.Errorf("at least one group must be specified")
	}

	var inv strings.Builder
	inv.WriteString("# Auto-generated by image-build\n")
	inv.WriteString("# Dynamic inventory for buildah container\n")
	inv.WriteString("all:\n")
	inv.WriteString("  children:\n")

	for _, group := range groups {
		inv.WriteString(fmt.Sprintf("    %s:\n", group))
		inv.WriteString("      hosts:\n")
		inv.WriteString(fmt.Sprintf("        %s:\n", containerName))
		inv.WriteString("          ansible_connection: containers.podman.buildah\n")
	}

	return inv.String(), nil
}

// buildAnsibleHostCommand constructs the ansible-playbook command to run on the host.
//
// The command structure:
//   - Multiple -i flags (user inventory + dynamic inventory placeholder)
//   - --limit to target only the container
//   - Connection plugin automatically used via inventory vars
func (b *Builder) buildAnsibleHostCommand(ansible *config.AnsibleCommand, containerName string) ([]string, error) {
	// Verify playbook exists
	if _, err := os.Stat(ansible.Playbook); err != nil {
		return nil, fmt.Errorf("playbook not found: %s", ansible.Playbook)
	}

	// Convert to absolute path
	absPlaybook, err := filepath.Abs(ansible.Playbook)
	if err != nil {
		return nil, fmt.Errorf("resolve playbook path: %w", err)
	}

	// Get playbook directory for roles path
	//playbookDir := filepath.Dir(absPlaybook)

	cmd := []string{"ansible-playbook"}

	// Add dynamic inventory FIRST (process substitution)
	// This will be replaced with <(...) in the shell execution
	cmd = append(cmd, "-i", "DYNAMIC_INVENTORY_PLACEHOLDER")

	// Add user's inventory if provided (merged with dynamic)
	if ansible.Inventory != "" {
		if _, err := os.Stat(ansible.Inventory); err != nil {
			return nil, fmt.Errorf("inventory not found: %s", ansible.Inventory)
		}
		absInventory, err := filepath.Abs(ansible.Inventory)
		if err != nil {
			return nil, fmt.Errorf("resolve inventory path: %w", err)
		}
		cmd = append(cmd, "-i", absInventory)
	}

	// Limit to container only (prevents running on other hosts in inventory)
	limit := ansible.Limit
	if limit == "" {
		limit = containerName
	}
	cmd = append(cmd, "--limit", limit)

	// Verbosity
	if ansible.Verbose > 0 {
		verbosity := strings.Repeat("v", min(ansible.Verbose, 4))
		cmd = append(cmd, "-"+verbosity)
	}

	// Extra vars
	for key, value := range ansible.ExtraVars {
		cmd = append(cmd, "-e", fmt.Sprintf("%s=%s", key, value))
	}

	// Tags
	if ansible.Tags != "" {
		cmd = append(cmd, "--tags", ansible.Tags)
	}
	if ansible.SkipTags != "" {
		cmd = append(cmd, "--skip-tags", ansible.SkipTags)
	}

	// Check mode
	if ansible.CheckMode {
		cmd = append(cmd, "--check")
	}

	// Playbook (must be last)
	cmd = append(cmd, absPlaybook)

	// Store playbook dir for ANSIBLE_ROLES_PATH env var
	return cmd, nil
}

// executeAnsibleOnHost runs ansible-playbook on the host using bash
// with a temporary YAML inventory file for the dynamic inventory.
//
// This constructs a command like:
//
//	ansible-playbook -i /tmp/inventory.yaml -i inventory/ --limit container playbook.yaml
//
// We use a temporary file instead of process substitution because Ansible
// needs a file extension (.yaml) to determine the inventory format.
func (b *Builder) executeAnsibleOnHost(ctx context.Context, ansibleCmd []string, dynamicInventory, playbookPath string) error {
	// Get playbook directory for roles path
	absPlaybook, _ := filepath.Abs(playbookPath)
	playbookDir := filepath.Dir(absPlaybook)

	// Look for roles in parent directory (common structure: playbooks/ and roles/ as siblings)
	rolesPath := filepath.Join(filepath.Dir(playbookDir), "roles")

	// Also check for roles in playbook directory itself
	playbookRolesPath := filepath.Join(playbookDir, "roles")

	// Build ANSIBLE_ROLES_PATH - check both locations
	var rolesPaths []string
	if _, err := os.Stat(playbookRolesPath); err == nil {
		rolesPaths = append(rolesPaths, playbookRolesPath)
	}
	if _, err := os.Stat(rolesPath); err == nil {
		rolesPaths = append(rolesPaths, rolesPath)
	}

	// Write dynamic inventory to a temporary YAML file
	// Ansible needs the .yaml extension to recognize the format
	tmpDir := os.TempDir()
	inventoryFile := filepath.Join(tmpDir, fmt.Sprintf("image-build-ansible-inventory-%d.yaml", time.Now().UnixNano()))

	// Ensure cleanup
	defer os.Remove(inventoryFile)

	// Write the dynamic inventory
	if err := os.WriteFile(inventoryFile, []byte(dynamicInventory), 0600); err != nil {
		return fmt.Errorf("write dynamic inventory: %w", err)
	}

	slog.Debug("Wrote dynamic inventory to temp file",
		"path", inventoryFile,
		"content", dynamicInventory)

	// Build the command, replacing DYNAMIC_INVENTORY_PLACEHOLDER with the temp file
	var finalCmd []string
	for _, arg := range ansibleCmd {
		if arg == "DYNAMIC_INVENTORY_PLACEHOLDER" {
			finalCmd = append(finalCmd, inventoryFile)
		} else {
			finalCmd = append(finalCmd, arg)
		}
	}

	command := strings.Join(finalCmd, " ")
	slog.Debug("Executing ansible-playbook on host",
		"command", command,
		"dynamic_inventory_file", inventoryFile,
		"roles_path", strings.Join(rolesPaths, ":"))

	// Execute ansible-playbook directly (no need for bash -c anymore)
	// Important: We need to ensure blocking I/O for Ansible
	cmd := exec.CommandContext(ctx, finalCmd[0], finalCmd[1:]...)

	// Set environment variables
	cmd.Env = os.Environ()

	// Check if we need to pass graph root to Ansible/buildah connection plugin
	// This is critical for non-root users where buildah might use a different storage location
	if graphRoot := os.Getenv("BUILDAH_GRAPH_ROOT"); graphRoot != "" {
		slog.Debug("Using BUILDAH_GRAPH_ROOT from environment", "path", graphRoot)
		// Already in environment via os.Environ()
	} else if graphRoot := os.Getenv("STORAGE_DRIVER"); graphRoot != "" {
		slog.Debug("STORAGE_DRIVER set", "driver", graphRoot)
	}

	// Log the user context for debugging
	if user := os.Getenv("USER"); user != "" {
		slog.Debug("Running as user", "user", user, "uid", os.Getuid())
	}

	// Add ANSIBLE_ROLES_PATH if we found roles directories
	if len(rolesPaths) > 0 {
		cmd.Env = append(cmd.Env, "ANSIBLE_ROLES_PATH="+strings.Join(rolesPaths, ":"))
	}

	// Disable host key checking for buildah containers (they don't have SSH)
	cmd.Env = append(cmd.Env, "ANSIBLE_HOST_KEY_CHECKING=False")

	// Create pipes for stdout/stderr to ensure blocking I/O
	// Ansible requires blocking file handles, not the potentially non-blocking
	// os.Stdout/os.Stderr that the Go process might have inherited
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("create stderr pipe: %w", err)
	}

	// Don't attach stdin - Ansible shouldn't need interactive input during builds
	// Setting to nil gives it /dev/null which is blocking
	cmd.Stdin = nil

	// Start the command
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ansible-playbook: %w", err)
	}

	// Use WaitGroup to ensure we capture all output before cmd.Wait()
	var wg sync.WaitGroup
	wg.Add(2)

	// Stream stdout to our stdout in real-time using io.Copy (blocking)
	go func() {
		defer wg.Done()
		io.Copy(os.Stdout, stdout)
	}()

	// Stream stderr to our stderr in real-time using io.Copy (blocking)
	go func() {
		defer wg.Done()
		io.Copy(os.Stderr, stderr)
	}()

	// Wait for output streaming to complete
	wg.Wait()

	// Wait for command to exit
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("ansible-playbook execution failed: %w", err)
	}

	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
