// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

// Tests for classifyAnsibleLine — the regex-heavy classifier that maps
// Ansible callback output lines to (slog.Level, attrs). High value to test
// because the regex patterns are easy to misregress and there's no way to
// notice a broken classification at runtime (the logs just become less
// useful, not failed).
package builder

import (
	"log/slog"
	"testing"
)

func TestClassifyAnsibleLine(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		wantLevel slog.Level
		wantEvent string // value of "event" attr; "" if none expected
	}{
		{
			name:      "play header",
			line:      "PLAY [Configure base image] *********************",
			wantLevel: slog.LevelInfo,
			wantEvent: "play",
		},
		{
			name:      "task header",
			line:      "TASK [Install packages] *********************",
			wantLevel: slog.LevelInfo,
			wantEvent: "task",
		},
		{
			name:      "handler header",
			line:      "RUNNING HANDLER [Restart service] *********************",
			wantLevel: slog.LevelInfo,
			wantEvent: "handler",
		},
		{
			name:      "ok result",
			line:      "ok: [localhost]",
			wantLevel: slog.LevelInfo,
			wantEvent: "result",
		},
		{
			name:      "changed result",
			line:      "changed: [localhost]",
			wantLevel: slog.LevelInfo,
			wantEvent: "result",
		},
		{
			name:      "skipped result is Debug (suppressed at INFO)",
			line:      "skipping: [localhost]",
			wantLevel: slog.LevelDebug,
			wantEvent: "result",
		},
		{
			name:      "fatal FAILED is Error",
			line:      "fatal: [localhost]: FAILED! => {...}",
			wantLevel: slog.LevelError,
			wantEvent: "result",
		},
		{
			name:      "fatal UNREACHABLE is Error",
			line:      "fatal: [host1]: UNREACHABLE! => {...}",
			wantLevel: slog.LevelError,
			wantEvent: "result",
		},
		{
			name:      "failed result is Error",
			line:      "failed: [localhost] (item=foo) => {...}",
			wantLevel: slog.LevelError,
			wantEvent: "result",
		},
		{
			name:      "play recap header",
			line:      "PLAY RECAP *********************",
			wantLevel: slog.LevelInfo,
			wantEvent: "recap",
		},
		{
			name:      "host summary in recap",
			line:      "localhost                  : ok=5    changed=2    unreachable=0    failed=0",
			wantLevel: slog.LevelInfo,
			wantEvent: "host_summary",
		},
		{
			name:      "buildah logrus line is downgraded to Debug",
			line:      `time="2025-06-07T13:23:00Z" level=info msg="something"`,
			wantLevel: slog.LevelDebug,
			wantEvent: "",
		},
		{
			name:      "buildah bracketed line is downgraded to Debug",
			line:      "INFO[0001] doing something",
			wantLevel: slog.LevelDebug,
			wantEvent: "",
		},
		{
			name:      "unrecognised line surfaces at Info with no event",
			line:      "some random ansible debug output",
			wantLevel: slog.LevelInfo,
			wantEvent: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLevel, gotAttrs := classifyAnsibleLine(tt.line)
			if gotLevel != tt.wantLevel {
				t.Errorf("level = %v, want %v", gotLevel, tt.wantLevel)
			}
			if tt.wantEvent == "" {
				// No event expected; either no attrs or none with key=event.
				for _, a := range gotAttrs {
					if a.Key == "event" {
						t.Errorf("expected no event attr, got event=%q", a.Value.String())
					}
				}
				return
			}
			var foundEvent string
			for _, a := range gotAttrs {
				if a.Key == "event" {
					foundEvent = a.Value.String()
				}
			}
			if foundEvent != tt.wantEvent {
				t.Errorf("event attr = %q, want %q (full attrs: %v)", foundEvent, tt.wantEvent, gotAttrs)
			}
		})
	}
}

func TestClassifyAnsibleLine_ResultCarriesHost(t *testing.T) {
	// Result events (ok/changed/failed/fatal/skipping) must include the
	// host name so log consumers can filter by host. Bug-prone because
	// the host capture is in the regex and could silently drop.
	tests := []struct {
		line     string
		wantHost string
	}{
		{"ok: [web1]", "web1"},
		{"changed: [db2.internal]", "db2.internal"},
		{"failed: [host-with-dashes] (item=x)", "host-with-dashes"},
		{"fatal: [worker]: FAILED! => {...}", "worker"},
		{"skipping: [bastion]", "bastion"},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			_, attrs := classifyAnsibleLine(tt.line)
			var host string
			for _, a := range attrs {
				if a.Key == "host" {
					host = a.Value.String()
				}
			}
			if host != tt.wantHost {
				t.Errorf("host attr = %q, want %q", host, tt.wantHost)
			}
		})
	}
}

func TestClassifyAnsibleLine_TaskNameCapture(t *testing.T) {
	// Task/play/handler lines must capture the bracketed name verbatim so
	// downstream tools can group by task.
	tests := []struct {
		line     string
		wantName string
	}{
		{"PLAY [Configure compute nodes] ******", "Configure compute nodes"},
		{"TASK [Set hostname] ******", "Set hostname"},
		{"RUNNING HANDLER [restart networking] ******", "restart networking"},
		{"TASK [Install nginx | check version] ******", "Install nginx | check version"},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			_, attrs := classifyAnsibleLine(tt.line)
			var name string
			for _, a := range attrs {
				if a.Key == "name" {
					name = a.Value.String()
				}
			}
			if name != tt.wantName {
				t.Errorf("name attr = %q, want %q", name, tt.wantName)
			}
		})
	}
}
