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
// It performs the following steps:
//  1. Verifies Ansible is installed in the container
//  2. Creates temporary Ansible directory structure
//  3. Copies playbook, inventory, and roles to the container
//  4. Generates dynamic localhost inventory
//  5. Generates ansible.cfg
//  6. Executes ansible-playbook with specified options
//  7. Cleans up temporary files
func (b *Builder) runAnsibleCommand(ctx context.Context, c container.Container, ansible *config.AnsibleCommand) error {
	log := slog.With("component", "builder", "subsystem", "ansible")

	// Step 1: Verify Ansible is installed
	log.Debug("Verifying Ansible installation")
	if err := b.verifyAnsibleInstalled(ctx, c); err != nil {
		return fmt.Errorf("ansible not installed: %w", err)
	}

	// Step 2: Create Ansible directory structure in container
	ansibleTmpDir := "/tmp/image-build-ansible"
	log.Debug("Creating Ansible directory structure", "dir", ansibleTmpDir)
	if err := b.createAnsibleDirectories(ctx, c, ansibleTmpDir); err != nil {
		return fmt.Errorf("create ansible directories: %w", err)
	}

	// Ensure cleanup happens even if steps fail
	defer func() {
		log.Debug("Cleaning up Ansible files")
		_ = b.cleanupAnsibleFiles(ctx, c, ansibleTmpDir)
	}()

	// Step 3: Copy playbook to container
	playbookPath := b.resolveConfigPath(ansible.Playbook)
	log.Info("Copying playbook to container", "playbook", playbookPath)
	containerPlaybookPath, err := b.copyPlaybookToContainer(ctx, c, playbookPath, ansibleTmpDir)
	if err != nil {
		return fmt.Errorf("copy playbook: %w", err)
	}

	// Step 4: Copy roles directory if it exists
	log.Debug("Checking for roles directory")
	if err := b.copyRolesDirectory(ctx, c, playbookPath, ansibleTmpDir); err != nil {
		return fmt.Errorf("copy roles: %w", err)
	}

	// Step 5: Copy inventory to container (if specified)
	if ansible.Inventory != "" {
		inventoryPath := b.resolveConfigPath(ansible.Inventory)
		log.Info("Copying inventory to container", "inventory", inventoryPath)
		if err := b.copyInventoryToContainer(ctx, c, inventoryPath, ansibleTmpDir); err != nil {
			return fmt.Errorf("copy inventory: %w", err)
		}
	}

	// Step 6: Generate dynamic localhost inventory
	log.Debug("Generating localhost inventory", "groups", ansible.Groups)
	localhostInventoryPath, err := b.generateLocalhostInventory(ctx, c, ansible.Groups, ansibleTmpDir)
	if err != nil {
		return fmt.Errorf("generate localhost inventory: %w", err)
	}

	// Step 7: Generate ansible.cfg
	log.Debug("Generating ansible.cfg")
	if err := b.generateAnsibleConfig(ctx, c, ansibleTmpDir); err != nil {
		return fmt.Errorf("generate ansible.cfg: %w", err)
	}

	// Step 8: Execute ansible-playbook
	log.Info("Executing Ansible playbook", "playbook", containerPlaybookPath)
	if err := b.executeAnsiblePlaybook(ctx, c, ansible, containerPlaybookPath, ansibleTmpDir, localhostInventoryPath); err != nil {
		return fmt.Errorf("execute ansible-playbook: %w", err)
	}

	log.Info("Ansible playbook execution completed successfully")
	return nil
}

// verifyAnsibleInstalled checks if Ansible is installed in the container
func (b *Builder) verifyAnsibleInstalled(ctx context.Context, c container.Container) error {
	out := container.NewBufLogWriter("stdout")
	err := c.Run(ctx, []string{"ansible-playbook", "--version"}, container.RunModeContainer, out)
	if err != nil {
		return fmt.Errorf("ansible-playbook not found - ensure ansible-core or ansible is installed")
	}
	return nil
}

// createAnsibleDirectories creates the directory structure for Ansible in the container
func (b *Builder) createAnsibleDirectories(ctx context.Context, c container.Container, baseDir string) error {
	dirs := []string{
		baseDir,
		filepath.Join(baseDir, "playbooks"),
		filepath.Join(baseDir, "inventory"),
		filepath.Join(baseDir, "roles"),
	}

	for _, dir := range dirs {
		cmd := []string{"mkdir", "-p", dir}
		out := container.NewBufLogWriter("stdout")
		if err := c.Run(ctx, cmd, container.RunModeContainer, out); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}
	return nil
}

// resolveConfigPath resolves a path relative to the config file directory
func (b *Builder) resolveConfigPath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	// Get the directory containing the config file
	// For now, we'll use the current working directory
	// In a real implementation, you'd track the config file path
	return path
}

// copyPlaybookToContainer copies the playbook file to the container
func (b *Builder) copyPlaybookToContainer(ctx context.Context, c container.Container, hostPath, containerBaseDir string) (string, error) {
	// Read the playbook file
	content, err := os.ReadFile(hostPath)
	if err != nil {
		return "", fmt.Errorf("read playbook: %w", err)
	}

	// Determine container path
	playbookName := filepath.Base(hostPath)
	containerPath := filepath.Join(containerBaseDir, "playbooks", playbookName)

	// Write to container
	if err := c.WriteFile(ctx, config.File{
		Path:    containerPath,
		Content: string(content),
	}); err != nil {
		return "", fmt.Errorf("write playbook to container: %w", err)
	}

	return containerPath, nil
}

// copyRolesDirectory copies the roles directory to the container if it exists
func (b *Builder) copyRolesDirectory(ctx context.Context, c container.Container, playbookPath, containerBaseDir string) error {
	// Look for roles directory in the same directory as the playbook
	playbookDir := filepath.Dir(playbookPath)
	rolesDir := filepath.Join(playbookDir, "roles")

	// Check if roles directory exists
	if _, err := os.Stat(rolesDir); os.IsNotExist(err) {
		// Roles directory doesn't exist, that's ok
		return nil
	}

	// Walk the roles directory and copy all files
	return filepath.Walk(rolesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip the root roles directory itself
		if path == rolesDir {
			return nil
		}

		// Get relative path from roles directory
		relPath, err := filepath.Rel(rolesDir, path)
		if err != nil {
			return err
		}

		containerPath := filepath.Join(containerBaseDir, "roles", relPath)

		// If it's a directory, create it in the container
		if info.IsDir() {
			cmd := []string{"mkdir", "-p", containerPath}
			out := container.NewBufLogWriter("stdout")
			return c.Run(ctx, cmd, container.RunModeContainer, out)
		}

		// If it's a file, copy it
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read file %s: %w", path, err)
		}

		return c.WriteFile(ctx, config.File{
			Path:    containerPath,
			Content: string(content),
		})
	})
}

// copyInventoryToContainer copies inventory files/directories to the container
func (b *Builder) copyInventoryToContainer(ctx context.Context, c container.Container, hostPath, containerBaseDir string) error {
	// Check if it's a file or directory
	info, err := os.Stat(hostPath)
	if err != nil {
		return fmt.Errorf("stat inventory: %w", err)
	}

	if info.IsDir() {
		// Copy entire directory
		return filepath.Walk(hostPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			// Skip the root directory
			if path == hostPath {
				return nil
			}

			relPath, err := filepath.Rel(hostPath, path)
			if err != nil {
				return err
			}

			containerPath := filepath.Join(containerBaseDir, "inventory", relPath)

			if info.IsDir() {
				cmd := []string{"mkdir", "-p", containerPath}
				out := container.NewBufLogWriter("stdout")
				return c.Run(ctx, cmd, container.RunModeContainer, out)
			}

			// Skip files with extensions (Ansible convention)
			if filepath.Ext(info.Name()) != "" {
				// Still copy them, but they won't be auto-loaded
				content, err := os.ReadFile(path)
				if err != nil {
					return fmt.Errorf("read file %s: %w", path, err)
				}

				return c.WriteFile(ctx, config.File{
					Path:    containerPath,
					Content: string(content),
				})
			}

			content, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read file %s: %w", path, err)
			}

			return c.WriteFile(ctx, config.File{
				Path:    containerPath,
				Content: string(content),
			})
		})
	}

	// Single inventory file
	content, err := os.ReadFile(hostPath)
	if err != nil {
		return fmt.Errorf("read inventory file: %w", err)
	}

	fileName := filepath.Base(hostPath)
	containerPath := filepath.Join(containerBaseDir, "inventory", fileName)

	return c.WriteFile(ctx, config.File{
		Path:    containerPath,
		Content: string(content),
	})
}

// generateLocalhostInventory generates a dynamic inventory file for localhost
// Returns the path to the generated inventory file
func (b *Builder) generateLocalhostInventory(ctx context.Context, c container.Container, groups []string, containerBaseDir string) (string, error) {
	// Build the inventory content in proper INI format
	var sb strings.Builder

	// Add group definitions first
	for _, group := range groups {
		sb.WriteString(fmt.Sprintf("[%s]\n", group))
		sb.WriteString("localhost ansible_connection=local\n\n")
	}

	// Use a filename that sorts first and won't conflict with user files
	// Ansible reads files in alphanumeric order, so 00- prefix ensures it's read first
	inventoryPath := filepath.Join(containerBaseDir, "inventory", "00-generated-localhost")
	if err := c.WriteFile(ctx, config.File{
		Path:    inventoryPath,
		Content: sb.String(),
	}); err != nil {
		return "", err
	}
	return inventoryPath, nil
}

// generateAnsibleConfig generates ansible.cfg in the container
func (b *Builder) generateAnsibleConfig(ctx context.Context, c container.Container, containerBaseDir string) error {
	log := slog.With("component", "builder", "subsystem", "ansible")
	
	// Use absolute paths for roles_path
	rolesPath := filepath.Join(containerBaseDir, "roles")
	configContent := fmt.Sprintf(`[defaults]
roles_path = %s
host_key_checking = False
`, rolesPath)

	configPath := filepath.Join(containerBaseDir, "ansible.cfg")
	log.Debug("Writing ansible.cfg", "path", configPath, "roles_path", rolesPath)
	
	return c.WriteFile(ctx, config.File{
		Path:    configPath,
		Content: configContent,
	})
}

// executeAnsiblePlaybook runs ansible-playbook with the specified options
func (b *Builder) executeAnsiblePlaybook(ctx context.Context, c container.Container, ansible *config.AnsibleCommand, playbookPath, ansibleDir, localhostInventoryPath string) error {
	// Build the command with absolute paths
	// Specify the generated localhost inventory first to ensure it's read before other inventory files
	cmd := []string{
		"ansible-playbook",
		"-i", localhostInventoryPath,
	}

	// Add the inventory directory if user provided one
	if ansible.Inventory != "" {
		cmd = append(cmd, "-i", filepath.Join(ansibleDir, "inventory"))
	}

	// Add verbosity
	if ansible.Verbose > 0 {
		cmd = append(cmd, strings.Repeat("-v", ansible.Verbose))
	}

	// Add extra vars
	for key, value := range ansible.ExtraVars {
		cmd = append(cmd, "-e", fmt.Sprintf("%s=%s", key, value))
	}

	// Add tags
	if ansible.Tags != "" {
		cmd = append(cmd, "--tags", ansible.Tags)
	}

	// Add skip-tags
	if ansible.SkipTags != "" {
		cmd = append(cmd, "--skip-tags", ansible.SkipTags)
	}

	// Add check mode
	if ansible.CheckMode {
		cmd = append(cmd, "--check")
	}

	// Add playbook path
	cmd = append(cmd, playbookPath)

	// Set ANSIBLE_CONFIG environment variable to point to our config
	configPath := filepath.Join(ansibleDir, "ansible.cfg")
	wrappedCmd := fmt.Sprintf("ANSIBLE_CONFIG=%s %s", configPath, strings.Join(cmd, " "))

	// Execute the command
	out := container.NewBufLogWriter("stdout")
	if err := c.RunScript(ctx, wrappedCmd, out); err != nil {
		return fmt.Errorf("ansible-playbook failed: %w", err)
	}

	return nil
}

// cleanupAnsibleFiles removes the temporary Ansible directory from the container
func (b *Builder) cleanupAnsibleFiles(ctx context.Context, c container.Container, ansibleDir string) error {
	cmd := []string{"rm", "-rf", ansibleDir}
	out := container.NewBufLogWriter("stdout")
	return c.Run(ctx, cmd, container.RunModeContainer, out)
}
