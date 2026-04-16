package config

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Repo struct {
	URL		string	`yaml:"url"`
}

type Packages struct {
	Manager	string		`yaml:"manager"`
	Repos	[]Repo		`yaml:"repos"`
	Install	[]string	`yaml:"install"`
}

type Config struct {
	Name		string   	`yaml:"name"`
	Tag     	string   	`yaml:"string"`
	Packages	[]string 	`yaml:"packages"`
	Arch		[]string 	`yaml:"arch"`
	Cmds		[]string 	`yaml:"cmds"`
}

func LoadConfig(path string) (Config, error) {
	c, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(c, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	return cfg
}