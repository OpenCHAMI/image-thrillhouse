package container

import (
	"context"

	"github.com/travisbcotton/image-build/internal/config"
)

type RunMode int

const (
	RunModeAuto RunMode = iota
	RunModeHost
	RunModeContainer
)

type Container interface {
	Run(ctx context.Context, cmd []string, mode RunMode) error
	RunScript(ctx context.Context, script string) error
	WriteFile(file config.File) error
	Commit(ctx context.Context, name, tag string) (string, error)
	GetID() string
	GetName() string
	Delete()
	MountPath() string
}
