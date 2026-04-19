package config

import "fmt"

func (c *Config) Validate() error {
	if err := c.Meta.Validate(); err != nil {
		return err
	}
	if err := c.Layer.Validate(); err != nil {
		return err
	}
	return nil
}

func (m *Meta) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("meta.name is required")
	}
	if m.Tag == "" {
		return fmt.Errorf("meta.tag is required")
	}
	// from is optional - absence means scratch
	return nil
}

func (l *Layer) Validate() error {
	if l.Manager.Name == "" {
		return fmt.Errorf("layer.manager is required")
	}
	validManagers := map[string]bool{
		"dnf": true,
	}
	if !validManagers[l.Manager.Name] {
		return fmt.Errorf("layer.manager %q is not supported", l.Manager)
	}
	for _, f := range l.Files {
		if err := f.Validate(); err != nil {
			return err
		}
	}
	if err := l.Actions.Validate(); err != nil {
		return err
	}
	return nil
}

func (f *File) Validate() error {
	if f.Path == "" {
		return fmt.Errorf("file.path is required")
	}
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
	if set > 1 {
		return fmt.Errorf("file %s: only one of content, src, or url may be set", f.Path)
	}
	if set == 0 {
		return fmt.Errorf("file %s: one of content, src, or url is required", f.Path)
	}
	return nil
}

func (a *Actions) Validate() error {
	if err := a.Install.Validate(); err != nil {
		return err
	}
	return nil
}

func (i *Install) Validate() error {
	for _, m := range i.Modules {
		if err := m.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("module.name is required")
	}
	validActions := map[string]bool{
		"enable":  true,
		"disable": true,
		"install": true,
		"reset":   true,
	}
	if m.Action == "" {
		return fmt.Errorf("module %s: action is required", m.Name)
	}
	if !validActions[m.Action] {
		return fmt.Errorf("module %s: action %q is not valid", m.Name, m.Action)
	}
	return nil
}

func (c *Command) Validate() error {
	if c.Run != "" && c.Script != "" {
		return fmt.Errorf("command: only one of run or script may be set")
	}
	if c.Run == "" && c.Script == "" {
		return fmt.Errorf("command: one of run or script is required")
	}
	return nil
}
