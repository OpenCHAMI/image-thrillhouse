// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

//go:build linux

package buildah

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// killStrayProcesses SIGKILLs any process still chrooted into root (the
// container's mount path) and waits briefly for them to exit. It returns
// the number of processes it signalled.
//
// Buildah's chroot isolation does not create a PID namespace, so daemons a
// package install spawns inside the rootfs — gpg-agent from GPG key
// imports is the classic one — survive the command that started them.
// Left alone they accumulate build after build on a shared runner (each
// one also pins the overlay mount, making Unmount fail) until the runner's
// process limit is exhausted and later builds crash with pthread_create
// EAGAIN. Sweeping right before Unmount reaps them while the mount path,
// their identifying mark, still exists.
//
// Reading /proc/<pid>/root requires owning the process (or CAP_SYS_PTRACE);
// strays we can't inspect aren't ours to kill, so permission errors are
// skipped silently.
func killStrayProcesses(root string, log *slog.Logger) int {
	if root == "" {
		return 0
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		log.Warn("stray process sweep: read /proc", "error", err)
		return 0
	}

	self := os.Getpid()
	var killed []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		link, err := os.Readlink("/proc/" + e.Name() + "/root")
		if err != nil {
			continue
		}
		if link != root && !strings.HasPrefix(link, root+"/") {
			continue
		}
		comm, _ := os.ReadFile("/proc/" + e.Name() + "/comm")
		log.Warn("killing stray process left inside container root",
			"pid", pid, "comm", strings.TrimSpace(string(comm)), "root", link)
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
			log.Warn("kill stray process", "pid", pid, "error", err)
			continue
		}
		killed = append(killed, pid)
	}

	// SIGKILL is asynchronous; give the kernel a moment to reap so the
	// Unmount that follows doesn't race a dying process still holding the
	// mount. Zombies still count as "existing" to kill(pid, 0) but no
	// longer pin the mount, so this is best-effort with a short deadline.
	deadline := time.Now().Add(2 * time.Second)
	for _, pid := range killed {
		for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
	}
	return len(killed)
}
