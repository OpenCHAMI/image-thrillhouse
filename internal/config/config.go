package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// top level config
type Config struct {
	Meta  Meta  `yaml:"meta"`
	Layer Layer `yaml:"layer"`
}

// meta info on layer
type Meta struct {
	Name string `yaml:"name"`
	From string `yaml:"from"`
	Tag  string `yaml:"tag"`
}

// layer specifics
type Layer struct {
	Manager Manager `yaml:"manager"`
	Repos   []Repo  `yaml:"repos"`
	Files   []File  `yaml:"files"`
	Actions Actions `yaml:"actions"`
}

type Manager struct {
	Name   string `yaml:"name"`
	Config string `yaml:"config"`
}

// File to add to layer
type File struct {
	Path    string `yaml:"path"`
	Content string `yaml:"content"`
	Src     string `yaml:"src"`
	URL     string `yaml:"url"`
}

// Repos
type Repo struct {
	Path    string `yaml:"name"`
	Content string `yaml:"content"`
	URL     string `yaml:"url"`
	Src     string `yaml:"src"`
}

// Actions on a layer
type Actions struct {
	Install  Install   `yaml:"install"`
	Commands []Command `yaml:"commands"`
}

// install stuff bro
type Install struct {
	Packages []string `yaml:"packages"`
	Groups   []string `yaml:"groups"`
	Modules  []Module `yaml:"modules"`
}

// fucking dnf
type Module struct {
	Name   string `yaml:"name"`
	Stream string `yaml:"stream"`
	Action string `yaml:"action"` // enable, install, disable etc
}

// Commands to run
type Command struct {
	Run    string `yaml:"run"`
	Script string `yaml:"script"`
}

// Used for switch-case so I can make things easier add in the future
type CommandType int

const (
	CommandRun CommandType = iota
	CommandScript
)

func (c *Command) Type() CommandType {
	if c.Script != "" {
		return CommandScript
	}
	return CommandRun
}

func LoadConfig(path string) (*Config, error) {
	c, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(c, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}
