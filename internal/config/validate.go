// SPDX-FileCopyrightText: © 2025 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

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
//   - Environment configuration is valid
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

	// Validate layer-level environment configuration
	if l.Env != nil {
		if err := l.Env.Validate(); err != nil {
			return fmt.Errorf("layer.env: %w", err)
		}
	}

	// Validate all files
	for _, f := range l.Files {
		if err := f.Validate(); err != nil {
			return err
		}
	}

	// Validate all directories
	for _, d := range l.Directories {
		if err := d.Validate(); err != nil {
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
	return requireExactlyOneSource("repo", r.Path, r.Content, r.Src, r.URL)
}

// Validate checks a File configuration for correctness.
// Requirements:
//   - path must be specified
//   - exactly one of content, src, or url must be set
func (f *File) Validate() error {
	if f.Path == "" {
		return fmt.Errorf("file.path is required")
	}
	return requireExactlyOneSource("file", f.Path, f.Content, f.Src, f.URL)
}

// Validate checks a Directory configuration for correctness.
// Requirements:
//   - path must be specified
//   - src must be specified
//   - owner and preserve_ownership are mutually exclusive
//
// Src is intentionally NOT stat'd here. Validation runs anywhere a config is
// loaded (including dry-runs and tag computation on hosts that don't have the
// source tree), so the existence check is deferred to the builder where a
// missing src produces a clear runtime error.
func (d *Directory) Validate() error {
	if d.Path == "" {
		return fmt.Errorf("directory.path is required")
	}
	if d.Src == "" {
		return fmt.Errorf("directory %s: src is required", d.Path)
	}
	if d.Owner != "" && d.PreserveOwnership {
		return fmt.Errorf("directory %s: owner and preserve_ownership are mutually exclusive", d.Path)
	}
	return nil
}

// requireExactlyOneSource is the shared "exactly one of content/src/url"
// check that File and Repo both need. label distinguishes the error messages
// ("file foo: ..." vs "repo bar: ...") so callers don't have to wrap.
func requireExactlyOneSource(label, path, content, src, url string) error {
	set := 0
	if content != "" {
		set++
	}
	if src != "" {
		set++
	}
	if url != "" {
		set++
	}
	if set > 1 {
		return fmt.Errorf("%s %s: only one of content, src, or url may be set", label, path)
	}
	if set == 0 {
		return fmt.Errorf("%s %s: one of content, src, or url is required", label, path)
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
//   - if env is set, validate environment configuration
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

	// Validate command-level environment configuration
	if c.Env != nil {
		if err := c.Env.Validate(); err != nil {
			return fmt.Errorf("env: %w", err)
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

// Validate checks an EnvConfig configuration for correctness.
// Requirements:
//   - pass keys must not be empty
//   - set keys must not be empty
//   - a key cannot appear in both pass and set (conflict)
func (e *EnvConfig) Validate() error {
	if e == nil {
		return nil
	}

	// Create a set of "pass" keys for checking conflicts
	passKeys := make(map[string]bool)
	for _, key := range e.Pass {
		if key == "" {
			return fmt.Errorf("pass contains empty key")
		}
		if passKeys[key] {
			return fmt.Errorf("duplicate key %q in pass", key)
		}
		passKeys[key] = true
	}

	// Check if any "set" keys conflict with "pass" keys
	for key := range e.Set {
		if key == "" {
			return fmt.Errorf("set contains empty key")
		}
		if passKeys[key] {
			return fmt.Errorf("environment variable %q appears in both pass and set", key)
		}
	}

	return nil
}
