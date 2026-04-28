package container

import (
	"context"
	"io"

	"github.com/containers/buildah/define"
	"github.com/travisbcotton/image-build/internal/config"
)

type RunMode int

const (
	RunModeAuto RunMode = iota
	RunModeHost
	RunModeContainer
)

type Container interface {
	Run(ctx context.Context, cmd []string, mode RunMode, out OutputWriter) error
	RunScript(ctx context.Context, script string, out OutputWriter) error
	WriteFile(file config.File) error
	Commit(ctx context.Context, name, tag string) (string, error)
	GetID() string
	GetName() string
	Delete()
	MountPath() string
	GetIsolation() define.Isolation
	CommitToRegistry(ctx context.Context, ref string, tlsVerify bool) error
}

type OutputWriter interface {
	io.Writer
	Flush(err error)
}
