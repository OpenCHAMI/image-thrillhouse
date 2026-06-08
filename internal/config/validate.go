package config

import "fmt"

// Validate checks the entire configuration for errors.
// It recursively validates all sections: Meta, Layer, and their subsections.
// Returns an error if any validation fails.
func (c *Config) Validate() error {
	if err := c.Meta.Validate(); err != nil {
		return err
	}
	if err := c.Layer.Validate(); err != nil {
		return err
	}
	return nil
}

// Validate checks the Meta section for required fields.
// Required fields:
//   - name: Image name
//   - tags: At least one image tag (slice; the first tag is the "primary")
//
// Optional:
//   - from: Base image (defaults to scratch if not specified)
//   - labels: Custom OCI labels merged into the generated label set
func (m *Meta) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("meta.name is required")
	}
	if len(m.Tags) == 0 {
		return fmt.Errorf("meta.tags is required and must contain at least one tag")
	}
	// from is optional - absence means scratch
	return nil
}

// Validate checks the Layer section for required fields and valid values.
// It validates:
//   - Manager name is specified and supported
//   - All files are valid
//   - All actions are valid
func (l *Layer) Validate() error {
	if l.Manager.Name == "" {
		return fmt.Errorf("layer.manager is required")
	}

	// Check if the package manager is supported
	validManagers := map[string]bool{
		"dnf":        true, // Red Hat, Rocky, AlmaLinux, Fedora
		"mmdebstrap": true, // Debian, Ubuntu (scratch builds only)
		"apt":        true, // Debian, Ubuntu (parent builds only)
		"zypper":     true, // openSUSE, SLES
	}
	if !validManagers[l.Manager.Name] {
		return fmt.Errorf("layer.manager %q is not supported", l.Manager.Name)
	}

	// Validate all files
	for _, f := range l.Files {
		if err := f.Validate(); err != nil {
			return err
		}
	}

	// Validate all repos (same source rules as files)
	for _, r := range l.Repos {
		if err := r.Validate(); err != nil {
			return err
		}
	}

	// Validate all actions
	if err := l.Actions.Validate(); err != nil {
		return err
	}
	return nil
}

// Validate checks a Repo configuration for correctness.
// Requirements:
//   - path must be specified
//   - exactly one of content, src, or url must be set
func (r *Repo) Validate() error {
	if r.Path == "" {
		return fmt.Errorf("repo.path is required")
	}

	set := 0
	if r.Content != "" {
		set++
	}
	if r.Src != "" {
		set++
	}
	if r.URL != "" {
		set++
	}

	if set > 1 {
		return fmt.Errorf("repo %s: only one of content, src, or url may be set", r.Path)
	}
	if set == 0 {
		return fmt.Errorf("repo %s: one of content, src, or url is required", r.Path)
	}
	return nil
}

// Validate checks a File configuration for correctness.
// Requirements:
//   - path must be specified
//   - exactly one of content, src, or url must be set
func (f *File) Validate() error {
	if f.Path == "" {
		return fmt.Errorf("file.path is required")
	}

	// Count how many sources are specified
	set := 0
	if f.Content != "" {
		set++
	}
	if f.Src != "" {
		set++
	}
	if f.URL != "" {
		set++
	}

	// Must have exactly one source
	if set > 1 {
		return fmt.Errorf("file %s: only one of content, src, or url may be set", f.Path)
	}
	if set == 0 {
		return fmt.Errorf("file %s: one of content, src, or url is required", f.Path)
	}
	return nil
}

// Validate checks the Actions section.
// Validates both Install and Commands subsections.
func (a *Actions) Validate() error {
	if err := a.Install.Validate(); err != nil {
		return err
	}
	// Validate all commands
	for i, cmd := range a.Commands {
		if err := cmd.Validate(); err != nil {
			return fmt.Errorf("command %d: %w", i, err)
		}
	}
	return nil
}

// Validate checks the Install section.
// Currently only validates module configurations.
func (i *Install) Validate() error {
	for _, m := range i.Modules {
		if err := m.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Validate checks a Module configuration for correctness.
// Requirements:
//   - name must be specified
//   - action must be specified and valid (enable, disable, install, reset)
func (m *Module) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("module.name is required")
	}

	// Valid DNF module actions
	validActions := map[string]bool{
		"enable":  true, // Enable a module stream
		"disable": true, // Disable a module
		"install": true, // Install packages from a module
		"reset":   true, // Reset module state
	}

	if m.Action == "" {
		return fmt.Errorf("module %s: action is required", m.Name)
	}
	if !validActions[m.Action] {
		return fmt.Errorf("module %s: action %q is not valid", m.Name, m.Action)
	}
	return nil
}

// Validate checks a Command configuration for correctness.
// Requirements:
//   - exactly one of run, script, or ansible must be set
//   - if ansible is set, validate ansible configuration
func (c *Command) Validate() error {
	// Count which command type is set
	set := 0
	if c.Run != "" {
		set++
	}
	if c.Script != "" {
		set++
	}
	if c.Ansible != nil {
		set++
	}

	if set == 0 {
		return fmt.Errorf("command must specify one of run, script, or ansible")
	}
	if set > 1 {
		return fmt.Errorf("command can only specify one of run, script, or ansible")
	}

	// Validate Ansible-specific configuration
	if c.Ansible != nil {
		if err := c.Ansible.Validate(); err != nil {
			return err
		}
	}

	return nil
}

// Validate checks an AnsibleCommand configuration for correctness.
// Requirements:
//   - playbook must be specified
//   - groups must contain at least one group
//   - verbose must be between 0 and 4
func (a *AnsibleCommand) Validate() error {
	if a.Playbook == "" {
		return fmt.Errorf("ansible.playbook is required")
	}
	if len(a.Groups) == 0 {
		return fmt.Errorf("ansible.groups is required and must contain at least one group")
	}
	if a.Verbose < 0 || a.Verbose > 4 {
		return fmt.Errorf("ansible.verbose must be between 0 and 4, got %d", a.Verbose)
	}
	return nil
}
