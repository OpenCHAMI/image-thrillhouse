package builder

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/travisbcotton/image-build/internal/config"
	"github.com/travisbcotton/image-build/internal/container"
)

// runAnsibleCommand executes an Ansible playbook inside the container.
//
// This approach:
// 1. Copies playbook and inventory files into the container
// 2. Generates a localhost inventory with the container in specified groups
// 3. Runs ansible-playbook inside the container using ansible_connection=local
// 4. Cleans up temporary files
//
// Benefits:
// - No storage isolation issues (container sees itself)
// - Uses existing c.Run() infrastructure
// - No buildah connection plugin needed
// - Works reliably regardless of user/root context
//
// Requirements:
// - ansible-core must be installed in the container
// - python3 must be available for Ansible modules
func (b *Builder) runAnsibleCommand(ctx context.Context, c container.Container, ansible *config.AnsibleCommand) error {
	log := slog.With("component", "builder", "subsystem", "ansible")

	log.Info("Starting Ansible playbook execution (in-container)",
		"playbook", ansible.Playbook,
		"groups", ansible.Groups,
		"inventory", ansible.Inventory)

	// Verify Ansible is installed in the container
	if err := b.ensureAnsibleInstalledInContainer(ctx, c); err != nil {
		return fmt.Errorf("ansible not available in container: %w", err)
	}

	// Create temp directory in container for Ansible files
	ansibleTmpDir := "/tmp/image-build-ansible"

	// Create the directory structure
	if err := b.createAnsibleDirectories(ctx, c, ansibleTmpDir); err != nil {
		return fmt.Errorf("create ansible directories: %w", err)
	}

	// Copy playbook to container
	playbookPath, err := b.copyPlaybookToContainer(ctx, c, ansible.Playbook, ansibleTmpDir)
	if err != nil {
		return fmt.Errorf("copy playbook: %w", err)
	}

	// Copy roles directory if it exists
	if err := b.copyRolesDirectory(ctx, c, ansible.Playbook, ansibleTmpDir); err != nil {
		log.Warn("Failed to copy roles directory", "error", err)
		// Non-fatal - playbook might not use roles
	}

	// Copy user's inventory if provided, or create minimal one
	inventoryPath, err := b.copyInventoryToContainer(ctx, c, ansible.Inventory, ansibleTmpDir)
	if err != nil {
		return fmt.Errorf("copy inventory: %w", err)
	}

	// Generate localhost inventory with groups
	if err := b.generateLocalhostInventory(ctx, c, ansible.Groups, ansibleTmpDir); err != nil {
		return fmt.Errorf("generate localhost inventory: %w", err)
	}

	// Build and execute ansible-playbook command
	if err := b.executeAnsibleInContainer(ctx, c, ansible, playbookPath, inventoryPath, ansibleTmpDir); err != nil {
		return fmt.Errorf("execute ansible-playbook: %w", err)
	}

	// Cleanup temp directory
	if err := b.cleanupAnsibleFiles(ctx, c, ansibleTmpDir); err != nil {
		log.Warn("Failed to cleanup ansible temp directory", "error", err)
	}

	log.Info("Ansible playbook execution completed successfully")
	return nil
}

// ensureAnsibleInstalledInContainer verifies ansible-playbook is available in the container.
func (b *Builder) ensureAnsibleInstalledInContainer(ctx context.Context, c container.Container) error {
	out := container.NewBufLogWriter("ansible-check")
	err := c.Run(ctx, []string{"ansible-playbook", "--version"}, container.RunModeContainer, out)
	if err != nil {
		return fmt.Errorf("ansible-playbook not found in container. Install it first:\n" +
			"  actions:\n" +
			"    install:\n" +
			"      packages:\n" +
			"        - ansible-core\n" +
			"        - python3\n" +
			"        - python3-dnf  # For dnf module")
	}
	return nil
}

// createAnsibleDirectories creates the directory structure for Ansible files in the container.
func (b *Builder) createAnsibleDirectories(ctx context.Context, c container.Container, baseDir string) error {
	dirs := []string{
		baseDir,
		filepath.Join(baseDir, "playbooks"),
		filepath.Join(baseDir, "inventory"),
		filepath.Join(baseDir, "roles"),
	}

	for _, dir := range dirs {
		out := container.NewBufLogWriter("mkdir")
		if err := c.Run(ctx, []string{"mkdir", "-p", dir}, container.RunModeContainer, out); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}

	return nil
}

// copyPlaybookToContainer copies the playbook file into the container.
func (b *Builder) copyPlaybookToContainer(ctx context.Context, c container.Container, playbookSrc, baseDir string) (string, error) {
	// Read playbook from host
	content, err := os.ReadFile(playbookSrc)
	if err != nil {
		return "", fmt.Errorf("read playbook %s: %w", playbookSrc, err)
	}

	// Write to container
	destPath := filepath.Join(baseDir, "playbooks", "playbook.yaml")
	if err := c.WriteFile(ctx, config.File{
		Path:    destPath,
		Content: string(content),
	}); err != nil {
		return "", fmt.Errorf("write playbook to container: %w", err)
	}

	slog.Debug("Copied playbook to container",
		"src", playbookSrc,
		"dest", destPath)

	return destPath, nil
}

// copyRolesDirectory copies the roles directory to the container if it exists.
func (b *Builder) copyRolesDirectory(ctx context.Context, c container.Container, playbookPath, baseDir string) error {
	// Look for roles directory relative to playbook
	playbookDir := filepath.Dir(playbookPath)

	// Try parent directory first (common layout: playbooks/ and roles/ as siblings)
	rolesPath := filepath.Join(filepath.Dir(playbookDir), "roles")
	if _, err := os.Stat(rolesPath); err == nil {
		return b.copyDirectoryToContainer(ctx, c, rolesPath, filepath.Join(baseDir, "roles"))
	}

	// Try in playbook directory
	rolesPath = filepath.Join(playbookDir, "roles")
	if _, err := os.Stat(rolesPath); err == nil {
		return b.copyDirectoryToContainer(ctx, c, rolesPath, filepath.Join(baseDir, "roles"))
	}

	// No roles directory found - not an error
	return nil
}

// copyDirectoryToContainer recursively copies a directory into the container.
func (b *Builder) copyDirectoryToContainer(ctx context.Context, c container.Container, srcDir, destDir string) error {
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip the root directory itself
		if path == srcDir {
			return nil
		}

		// Get relative path
		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}

		destPath := filepath.Join(destDir, relPath)

		// Handle directories
		if info.IsDir() {
			out := container.NewBufLogWriter("mkdir")
			if err := c.Run(ctx, []string{"mkdir", "-p", destPath}, container.RunModeContainer, out); err != nil {
				return fmt.Errorf("create directory %s: %w", destPath, err)
			}
			return nil
		}

		// Handle files
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}

		if err := c.WriteFile(ctx, config.File{
			Path:    destPath,
			Content: string(content),
		}); err != nil {
			return fmt.Errorf("write %s: %w", destPath, err)
		}

		slog.Debug("Copied file to container", "src", path, "dest", destPath)
		return nil
	})
}

// copyInventoryToContainer copies the user's inventory to the container if provided.
func (b *Builder) copyInventoryToContainer(ctx context.Context, c container.Container, inventorySrc, baseDir string) (string, error) {
	if inventorySrc == "" {
		// No user inventory - we'll just use the generated localhost one
		return filepath.Join(baseDir, "inventory"), nil
	}

	// Check if it's a directory or file
	info, err := os.Stat(inventorySrc)
	if err != nil {
		return "", fmt.Errorf("stat inventory %s: %w", inventorySrc, err)
	}

	inventoryDest := filepath.Join(baseDir, "inventory", "user")

	if info.IsDir() {
		// Copy entire directory
		if err := b.copyDirectoryToContainer(ctx, c, inventorySrc, inventoryDest); err != nil {
			return "", fmt.Errorf("copy inventory directory: %w", err)
		}
	} else {
		// Copy single file
		content, err := os.ReadFile(inventorySrc)
		if err != nil {
			return "", fmt.Errorf("read inventory: %w", err)
		}

		if err := c.WriteFile(ctx, config.File{
			Path:    inventoryDest,
			Content: string(content),
		}); err != nil {
			return "", fmt.Errorf("write inventory: %w", err)
		}
	}

	slog.Debug("Copied user inventory to container",
		"src", inventorySrc,
		"dest", inventoryDest)

	return filepath.Join(baseDir, "inventory"), nil
}

// generateLocalhostInventory creates an inventory file that adds localhost to the specified groups.
func (b *Builder) generateLocalhostInventory(ctx context.Context, c container.Container, groups []string, baseDir string) error {
	var inventory strings.Builder

	inventory.WriteString("# Auto-generated by image-build\n")
	inventory.WriteString("# Localhost inventory for in-container Ansible execution\n\n")

	// Create a localhost host that will be in all the specified groups
	inventory.WriteString("[image_build_localhost]\n")
	inventory.WriteString("localhost ansible_connection=local ansible_python_interpreter=/usr/bin/python3\n\n")

	// Add localhost to each specified group
	for _, group := range groups {
		inventory.WriteString(fmt.Sprintf("[%s:children]\n", group))
		inventory.WriteString("image_build_localhost\n\n")
	}

	// Write inventory file
	inventoryPath := filepath.Join(baseDir, "inventory", "localhost.ini")
	if err := c.WriteFile(ctx, config.File{
		Path:    inventoryPath,
		Content: inventory.String(),
	}); err != nil {
		return fmt.Errorf("write localhost inventory: %w", err)
	}

	slog.Debug("Generated localhost inventory",
		"path", inventoryPath,
		"groups", groups,
		"content", inventory.String())

	return nil
}

// executeAnsibleInContainer runs ansible-playbook inside the container.
func (b *Builder) executeAnsibleInContainer(ctx context.Context, c container.Container, ansible *config.AnsibleCommand, playbookPath, inventoryPath, baseDir string) error {
	// Build ansible-playbook command
	cmd := []string{"ansible-playbook"}

	// Add inventory
	cmd = append(cmd, "-i", inventoryPath)

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
	cmd = append(cmd, playbookPath)

	slog.Info("Executing ansible-playbook in container",
		"command", strings.Join(cmd, " "))

	// Execute the command
	out := container.NewBufLogWriter("ansible")
	if err := c.Run(ctx, cmd, container.RunModeContainer, out); err != nil {
		return fmt.Errorf("ansible-playbook failed: %w", err)
	}

	return nil
}

// cleanupAnsibleFiles removes the temporary Ansible directory from the container.
func (b *Builder) cleanupAnsibleFiles(ctx context.Context, c container.Container, baseDir string) error {
	out := container.NewBufLogWriter("cleanup")
	return c.Run(ctx, []string{"rm", "-rf", baseDir}, container.RunModeContainer, out)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
