// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package builder

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/openchami/image-thrillhouse/internal/backend/dnf"
	"github.com/openchami/image-thrillhouse/internal/config"
	"github.com/openchami/image-thrillhouse/internal/container"
	"github.com/openchami/image-thrillhouse/internal/publisher"
)

type cacheTestContainer struct {
	fakeContainer
	failCommand string
}

func (c *cacheTestContainer) Run(ctx context.Context, cmd []string, mode container.RunMode, out container.OutputWriter, opts ...container.RunOption) error {
	if err := c.fakeContainer.Run(ctx, cmd, mode, out, opts...); err != nil {
		return err
	}
	if c.failCommand != "" && slices.Contains(cmd, c.failCommand) {
		return errors.New("injected command failure")
	}
	return nil
}

func TestBuildCacheCleanupBeforePublish(t *testing.T) {
	for _, failCommand := range []string{"", "install", "clean"} {
		t.Run("fail="+failCommand, func(t *testing.T) {
			c := &cacheTestContainer{failCommand: failCommand}
			pub := &fakePublisher{}
			cfg := &config.Config{
				Meta: config.Meta{Name: "test", Tags: []string{"1"}, From: "rockylinux:9"},
				Layer: config.Layer{
					Manager: config.Manager{Name: "dnf"},
					Actions: config.Actions{Install: config.Install{Packages: []string{"vim"}}},
				},
			}
			b := New(cfg, "", dnf.New(map[string]string{"clean": "true"}), []publisher.Publisher{pub})
			b.newContainer = func(context.Context, string, bool) (container.Container, error) { return c, nil }
			err := b.Build(context.Background())
			if failCommand == "" {
				if err != nil || pub.PublishCalls != 1 || len(c.RunCalls) != 2 || !slices.Contains(c.RunCalls[1], "clean") {
					t.Fatalf("expected install and cleanup before publication: err=%v calls=%v published=%d", err, c.RunCalls, pub.PublishCalls)
				}
			} else if err == nil || pub.PublishCalls != 0 {
				t.Fatalf("failed %s must prevent publication: err=%v published=%d", failCommand, err, pub.PublishCalls)
			}
			if failCommand == "install" && len(c.RunCalls) != 1 {
				t.Fatalf("cleanup ran after failed install: %v", c.RunCalls)
			}
		})
	}
}
