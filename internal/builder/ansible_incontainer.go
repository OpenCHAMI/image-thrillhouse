package builder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/travisbcotton/image-build/internal/config"
	"github.com/travisbcotton/image-build/internal/container"
)

// ansibleANSIRe matches CSI ANSI escape sequences that ansible emits when it
// thinks it's writing to a TTY. Mirrors the (unexported) ansiRe in
// internal/container/streamlog.go.
var ansibleANSIRe = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

// Patterns recognised by classifyAnsibleLine. Each captures the relevant
// piece of structured info (play/task name, host) so it can be added as an
// slog attribute. Compiled once at package load time.
var (
	playRe      = regexp.MustCompile(`^PLAY \[(.*?)\]\s*\*+\s*$`)
	taskRe      = regexp.MustCompile(`^TASK \[(.*?)\]\s*\*+\s*$`)
	handlerRe   = regexp.MustCompile(`^RUNNING HANDLER \[(.*?)\]\s*\*+\s*$`)
	okRe        = regexp.MustCompile(`^ok:\s+\[([^\]]+)\]`)
	changedRe   = regexp.MustCompile(`^changed:\s+\[([^\]]+)\]`)
	skippingRe  = regexp.MustCompile(`^skipping:\s+\[([^\]]+)\]`)
	fatalRe     = regexp.MustCompile(`^fatal:\s+\[([^\]]+)\]:\s+(FAILED|UNREACHABLE)`)
	failedRe    = regexp.MustCompile(`^failed:\s+\[([^\]]+)\]`)
	recapRe     = regexp.MustCompile(`^PLAY RECAP\s*\*+\s*$`)
	hostRecapRe = regexp.MustCompile(`^(\S+)\s*:\s+(ok=\d+.*)$`)
)

// classifyAnsibleLine inspects one line of ansible default-callback output
// and emits it via log with appropriate level + structured attributes.
// Unrecognized lines fall through to log.Info(line) so nothing is lost. The
// raw line is always preserved as the slog Message so text-mode output stays
// human-readable; structured attrs are added on top for JSON consumers.
func classifyAnsibleLine(log *slog.Logger, line string) {
	switch {
	case playRe.MatchString(line):
		m := playRe.FindStringSubmatch(line)
		log.Info(line, "event", "play", "name", m[1])
	case taskRe.MatchString(line):
		m := taskRe.FindStringSubmatch(line)
		log.Info(line, "event", "task", "name", m[1])
	case handlerRe.MatchString(line):
		m := handlerRe.FindStringSubmatch(line)
		log.Info(line, "event", "handler", "name", m[1])
	case fatalRe.MatchString(line):
		m := fatalRe.FindStringSubmatch(line)
		log.Error(line, "event", "result", "status", strings.ToLower(m[2]), "host", m[1])
	case failedRe.MatchString(line):
		m := failedRe.FindStringSubmatch(line)
		log.Error(line, "event", "result", "status", "failed", "host", m[1])
	case changedRe.MatchString(line):
		m := changedRe.FindStringSubmatch(line)
		log.Info(line, "event", "result", "status", "changed", "host", m[1])
	case okRe.MatchString(line):
		m := okRe.FindStringSubmatch(line)
		log.Debug(line, "event", "result", "status", "ok", "host", m[1])
	case skippingRe.MatchString(line):
		m := skippingRe.FindStringSubmatch(line)
		log.Debug(line, "event", "result", "status", "skipped", "host", m[1])
	case recapRe.MatchString(line):
		log.Info(line, "event", "recap")
	case hostRecapRe.MatchString(line):
		m := hostRecapRe.FindStringSubmatch(line)
		log.Info(line, "event", "host_summary", "host", strings.TrimSpace(m[1]))
	default:
		log.Info(line)
	}
}

// ansibleStreamWriter forwards Ansible playbook output to slog one line at a
// time as the playbook runs, instead of buffering the entire run and dumping
// it at the end. Partial trailing lines (no newline yet) are held in `pending`
// until the next Write completes the line or Flush is called.
//
// ANSI escapes are stripped and CR-progress redraws are folded so progress
// bars don't leave breadcrumbs in the log. Each emitted line becomes one slog
// record with component=ansible — in --log-format=json that's one JSON
// object per line, in text/textblock it's one human-readable line.
type ansibleStreamWriter struct {
	mu      sync.Mutex
	pending []byte
	log     *slog.Logger
}

func newAnsibleStreamWriter() *ansibleStreamWriter {
	return &ansibleStreamWriter{
		log: slog.With("component", "ansible"),
	}
}

func (w *ansibleStreamWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, p...)
	for {
		i := bytes.IndexByte(w.pending, '\n')
		if i < 0 {
			break
		}
		w.emit(w.pending[:i])
		w.pending = w.pending[i+1:]
	}
	return len(p), nil
}

func (w *ansibleStreamWriter) Flush(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) > 0 {
		w.emit(w.pending)
		w.pending = nil
	}
}

// emit cleans one line and routes it through classifyAnsibleLine, which
// picks an appropriate slog level (INFO for play/task/changed/recap, DEBUG
// for ok/skipped, ERROR for fatal/failed) and attaches structured attributes
// (event=, name=, host=, status=) so JSON consumers can filter without
// regex-matching the message. Empty lines after cleaning are dropped so
// progress-bar redraws don't generate noise records.
func (w *ansibleStreamWriter) emit(line []byte) {
	// Fold CR-progress redraws: keep only the segment after the last \r.
	if idx := bytes.LastIndexByte(line, '\r'); idx >= 0 {
		line = line[idx+1:]
	}
	cleaned := ansibleANSIRe.ReplaceAllString(string(line), "")
	if strings.TrimSpace(cleaned) == "" {
		return
	}
	classifyAnsibleLine(w.log, cleaned)
}

// Container-side paths used for the bind-mounted ansible payload. None of
// these are written to from the container — they exist only for the duration
// of the playbook run via host bind mounts, so nothing under stageRoot ends
// up in the committed image layer.
const (
	stageRoot    = "/run/image-build-ansible"
	stageEtcPath = stageRoot + "/etc"       // generated cfg + localhost inventory + playbook copy
	stageRoles   = stageRoot + "/roles"     // user roles directory (bind mount)
	stageInv     = stageRoot + "/inventory" // user inventory (bind mount)
)

// runAnsibleCommand executes an Ansible playbook against the container without
// copying the playbook, inventory, or roles into the container's filesystem.
// Instead, a host-side staging directory is created with the generated
// ansible.cfg + localhost inventory + a copy of the user's playbook, and that
// directory plus any user inventory/roles are bind-mounted into the container
// for the duration of the run. Everything is cleaned up via defer on the
// host, so no temporary state is committed into the image layer.
func (b *Builder) runAnsibleCommand(ctx context.Context, c container.Container, ansible *config.AnsibleCommand) error {
	log := slog.With("component", "builder", "subsystem", "ansible")

	// Step 1: Verify Ansible is installed in the container.
	log.Debug("Verifying Ansible installation")
	if err := b.verifyAnsibleInstalled(ctx, c); err != nil {
		return fmt.Errorf("ansible not installed: %w", err)
	}

	// Step 2: Stage the generated config + playbook on the host.
	stageDir, playbookBase, err := b.stageAnsiblePayload(ansible)
	if err != nil {
		return fmt.Errorf("stage ansible payload: %w", err)
	}
	defer func() {
		log.Debug("Cleaning up ansible stage dir", "dir", stageDir)
		_ = os.RemoveAll(stageDir)
	}()
	log.Debug("Staged ansible payload", "host_dir", stageDir, "container_dir", stageEtcPath)

	// Step 3: Resolve optional user-provided roles and inventory paths. Both
	// are bind-mounted directly from their host locations (no copy). OCI bind
	// mount sources must be absolute, so we run filepath.Abs on whatever
	// resolveConfigPath produced.
	rolesHost, err := absPath(b.resolveConfigPath(firstNonEmpty(ansible.Roles, "roles")))
	if err != nil {
		return fmt.Errorf("resolve roles path: %w", err)
	}
	hasRoles := false
	if info, err := os.Stat(rolesHost); err == nil && info.IsDir() {
		hasRoles = true
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat roles %s: %w", rolesHost, err)
	}

	var inventoryHost string
	if ansible.Inventory != "" {
		inventoryHost, err = absPath(b.resolveConfigPath(ansible.Inventory))
		if err != nil {
			return fmt.Errorf("resolve inventory path: %w", err)
		}
		if _, err := os.Stat(inventoryHost); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("inventory path not found: %s (paths are resolved relative to the config file)", inventoryHost)
			}
			return fmt.Errorf("stat inventory %s: %w", inventoryHost, err)
		}
	}

	// Step 4: Execute ansible-playbook with the bind mounts in place.
	log.Info("Running ansible-playbook", "playbook", ansible.Playbook, "groups", ansible.Groups)
	if err := b.executeAnsiblePlaybook(ctx, c, ansible, playbookBase, stageDir, rolesHost, hasRoles, inventoryHost); err != nil {
		return fmt.Errorf("execute ansible-playbook: %w", err)
	}

	log.Info("Ansible playbook execution completed successfully")
	return nil
}

// verifyAnsibleInstalled checks if Ansible is installed in the container.
func (b *Builder) verifyAnsibleInstalled(ctx context.Context, c container.Container) error {
	out := container.NewBufLogWriter("stdout")
	if err := c.Run(ctx, []string{"ansible-playbook", "--version"}, container.RunModeContainer, out); err != nil {
		return fmt.Errorf("ansible-playbook not found - ensure ansible-core or ansible is installed")
	}
	return nil
}

// resolveConfigPath resolves a path from the config file (e.g.
// ansible.playbook, ansible.inventory, ansible.roles) against the directory
// of the config file. Absolute paths are returned unchanged. If the Builder
// has no config path (cfgPath == ""), the path is returned unchanged so it
// resolves relative to CWD — matching the prior behavior.
func (b *Builder) resolveConfigPath(path string) string {
	if filepath.IsAbs(path) || b.cfgPath == "" {
		return path
	}
	return filepath.Join(filepath.Dir(b.cfgPath), path)
}

// stageAnsiblePayload creates a host-side temp directory and populates it with
// the generated ansible.cfg, the generated localhost inventory, and a copy of
// the user's playbook. Returns the absolute path of the staging directory and
// the playbook's basename (used to build the container-side argv).
//
// The roles_path inside ansible.cfg points at the container-side mount of the
// user's roles directory, not the host path — so the rendered file is only
// useful when the bind mount is in place.
func (b *Builder) stageAnsiblePayload(ansible *config.AnsibleCommand) (string, string, error) {
	playbookHost := b.resolveConfigPath(ansible.Playbook)
	info, err := os.Stat(playbookHost)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", fmt.Errorf("playbook file not found: %s (paths are resolved relative to the config file)", playbookHost)
		}
		return "", "", fmt.Errorf("stat playbook %s: %w", playbookHost, err)
	}
	if info.IsDir() {
		return "", "", fmt.Errorf("playbook must be a file, not a directory: %s", playbookHost)
	}

	stageDir, err := os.MkdirTemp("", "image-build-ansible-*")
	if err != nil {
		return "", "", fmt.Errorf("create stage dir: %w", err)
	}

	// On any error after this point, clean up the partial stage so the caller
	// doesn't have to.
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(stageDir)
		}
	}()

	// Copy the playbook into the stage directory. It's a small file; the
	// alternative (bind-mounting a single file) requires a pre-existing target
	// path in the container, which complicates the mount setup for no real win.
	playbookBase := filepath.Base(playbookHost)
	playbookContent, err := os.ReadFile(playbookHost)
	if err != nil {
		return "", "", fmt.Errorf("read playbook: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, playbookBase), playbookContent, 0o644); err != nil {
		return "", "", fmt.Errorf("stage playbook: %w", err)
	}

	// Generate the localhost inventory. The 00- prefix ensures it sorts first
	// when ansible processes the staged etc/ directory.
	var inv strings.Builder
	for _, group := range ansible.Groups {
		inv.WriteString(fmt.Sprintf("[%s]\n", group))
		inv.WriteString("localhost ansible_connection=local\n\n")
	}
	if err := os.WriteFile(filepath.Join(stageDir, "00-generated-localhost"), []byte(inv.String()), 0o644); err != nil {
		return "", "", fmt.Errorf("write localhost inventory: %w", err)
	}

	// Generate ansible.cfg. roles_path is the *container-side* mount path
	// where the user's roles will be exposed at run time.
	cfgContent := fmt.Sprintf("[defaults]\nroles_path = %s\nhost_key_checking = False\n", stageRoles)
	if err := os.WriteFile(filepath.Join(stageDir, "ansible.cfg"), []byte(cfgContent), 0o644); err != nil {
		return "", "", fmt.Errorf("write ansible.cfg: %w", err)
	}

	success = true
	return stageDir, playbookBase, nil
}

// executeAnsiblePlaybook runs ansible-playbook with the specified options.
//
// The command is passed as argv directly to the container (no /bin/sh
// wrapping), so values inside ExtraVars/Tags/SkipTags that contain spaces or
// shell metacharacters are forwarded verbatim to ansible. ANSIBLE_CONFIG is
// set via the environment instead of a shell prefix. The playbook, generated
// config, roles, and inventory are all exposed via host bind mounts and never
// touch the committed image layer.
func (b *Builder) executeAnsiblePlaybook(
	ctx context.Context,
	c container.Container,
	ansible *config.AnsibleCommand,
	playbookBase, stageDir, rolesHost string,
	hasRoles bool,
	inventoryHost string,
) error {
	log := slog.With("component", "builder", "subsystem", "ansible")

	// Container-side paths. localhostInv is read before any user inventory.
	localhostInv := stageEtcPath + "/00-generated-localhost"
	playbookPath := stageEtcPath + "/" + playbookBase
	configPath := stageEtcPath + "/ansible.cfg"

	cmd := []string{
		"ansible-playbook",
		"-i", localhostInv,
	}
	if inventoryHost != "" {
		cmd = append(cmd, "-i", stageInv)
	}
	if ansible.Verbose > 0 {
		cmd = append(cmd, strings.Repeat("-v", ansible.Verbose))
	}
	for key, value := range ansible.ExtraVars {
		cmd = append(cmd, "-e", fmt.Sprintf("%s=%s", key, value))
	}
	if ansible.Tags != "" {
		cmd = append(cmd, "--tags", ansible.Tags)
	}
	if ansible.SkipTags != "" {
		cmd = append(cmd, "--skip-tags", ansible.SkipTags)
	}
	if ansible.CheckMode {
		cmd = append(cmd, "--check")
	}
	cmd = append(cmd, playbookPath)

	// Assemble the bind mounts. The stage dir is always mounted; roles and
	// inventory are conditional on the user supplying them.
	opts := []container.RunOption{
		container.WithEnv("ANSIBLE_CONFIG=" + configPath),
		container.WithBindMount(stageDir, stageEtcPath, true),
	}
	if hasRoles {
		opts = append(opts, container.WithBindMount(rolesHost, stageRoles, true))
	}
	if inventoryHost != "" {
		opts = append(opts, container.WithBindMount(inventoryHost, stageInv, true))
	}

	log.Debug("Executing ansible command",
		"cmd", cmd,
		"ANSIBLE_CONFIG", configPath,
		"stage_host", stageDir,
		"roles_host", rolesHost,
		"inventory_host", inventoryHost,
	)

	out := newAnsibleStreamWriter()
	if err := c.Run(ctx, cmd, container.RunModeContainer, out, opts...); err != nil {
		return fmt.Errorf("ansible-playbook failed: %w", err)
	}
	return nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// absPath returns the absolute form of path. OCI bind mounts require absolute
// source paths; resolveConfigPath can return a relative path if the config
// file was itself supplied relatively (e.g. via `image-build build foo.yaml`).
func absPath(path string) (string, error) {
	if filepath.IsAbs(path) {
		return path, nil
	}
	return filepath.Abs(path)
}
