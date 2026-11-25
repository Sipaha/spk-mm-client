//go:build linux

package desktop

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"
)

// openLaunchTimeout bounds the systemd-run D-Bus handoff. It is a short
// ceiling on enqueuing the transient unit, not on the lifetime of the browser
// or viewer spawned by xdg-open.
const (
	openLaunchTimeout = 2 * time.Second
	openWaitDelay     = 500 * time.Millisecond
	openUnitPrefix    = "spk-mm-client-open-"
)

// openForwardedEnv lists desktop/display variables passed explicitly to the
// transient user service so xdg-open reaches the active graphical session even
// if the user manager's environment lags behind the current login environment.
var openForwardedEnv = []string{
	"DISPLAY",
	"WAYLAND_DISPLAY",
	"XAUTHORITY",
	"XDG_CURRENT_DESKTOP",
	"DESKTOP_SESSION",
}

type linuxOpener struct {
	lookPath func(string) (string, error)
	getenv   func(string) string
	randHex  func() (string, error)
	run      func(ctx context.Context, name string, args ...string) ([]byte, error)
	timeout  time.Duration
}

func newLinuxOpener() linuxOpener {
	return linuxOpener{
		lookPath: exec.LookPath,
		getenv:   os.Getenv,
		randHex:  randomUnitSuffix,
		run:      runCombinedOutput,
		timeout:  openLaunchTimeout,
	}
}

func randomUnitSuffix() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func runCombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = openWaitDelay
	return cmd.CombinedOutput()
}

// escapeSystemdExecArg escapes literal '$' as '$$' for arguments passed after
// "--" to systemd-run. systemd expands $VAR and ${VAR} in ExecStart arguments
// across all versions (without needing --expand-environment=no from systemd
// 254+); literal '%' specifiers are not expanded by systemd-run.
func escapeSystemdExecArg(arg string) string {
	return strings.ReplaceAll(arg, "$", "$$")
}

// buildSystemdRunArgs builds the systemd-run invocation that starts cmdPath
// with cmdArgs inside a detached transient systemd user service.
//
// Why a transient service with ExitType=cgroup (never --scope):
//   - --scope runs the child inside the caller's process tree, so a
//     PR_SET_CHILD_SUBREAPER ancestor still adopts the spawned browser and its
//     subprocesses.
//   - A transient user service is spawned directly by systemd --user, outside
//     our process tree and subreaper. ExitType=cgroup keeps the unit tracked
//     for forked/backgrounded children of xdg-open, and --collect cleans up
//     the unit once the cgroup empties.
//
// Guarantee boundaries:
//   - --no-block confirms that systemd accepted and enqueued the transient
//     unit, not the exit status of xdg-open or the target application.
//   - On hosts without a working systemd --user bus (or if systemd-run /
//     xdg-open cannot be found or fails), callers fall back to Wails'
//     Browser.OpenURL / Browser.OpenFile, which does not detach from a
//     subreaper.
func (o linuxOpener) buildSystemdRunArgs(unitName, cmdPath string, cmdArgs ...string) []string {
	args := make([]string, 0, 7+len(openForwardedEnv)+1+len(cmdArgs))
	args = append(args,
		"--user",
		"--no-block",
		"--collect",
		"--quiet",
		"--property=ExitType=cgroup",
		"--unit="+unitName,
	)
	if o.getenv != nil {
		for _, k := range openForwardedEnv {
			if v := o.getenv(k); v != "" {
				args = append(args, "--setenv="+k+"="+v)
			}
		}
	}
	args = append(args, "--", escapeSystemdExecArg(cmdPath))
	for _, a := range cmdArgs {
		args = append(args, escapeSystemdExecArg(a))
	}
	return args
}

func (o linuxOpener) launchCommand(ctx context.Context, cmdPath string, cmdArgs ...string) (string, error) {
	if o.lookPath == nil || o.run == nil || o.randHex == nil {
		return "", errors.New("linux opener misconfigured")
	}
	systemdRun, err := o.lookPath("systemd-run")
	if err != nil {
		return "", fmt.Errorf("lookup systemd-run: %w", err)
	}
	suffix, err := o.randHex()
	if err != nil {
		return "", fmt.Errorf("generate unit suffix: %w", err)
	}
	unitName := openUnitPrefix + suffix
	args := o.buildSystemdRunArgs(unitName, cmdPath, cmdArgs...)

	timeout := o.timeout
	if timeout <= 0 {
		timeout = openLaunchTimeout
	}
	lctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	out, err := o.run(lctx, systemdRun, args...)
	if err != nil {
		msg := strings.TrimSpace(string(bytes.TrimSpace(out)))
		if msg != "" {
			return unitName, fmt.Errorf("systemd-run failed: %w (%s)", err, msg)
		}
		return unitName, fmt.Errorf("systemd-run failed: %w", err)
	}
	return unitName, nil
}

func (o linuxOpener) open(ctx context.Context, target string) error {
	if o.lookPath == nil {
		return errors.New("linux opener misconfigured")
	}
	xdgOpen, err := o.lookPath("xdg-open")
	if err != nil {
		return fmt.Errorf("lookup xdg-open: %w", err)
	}
	_, err = o.launchCommand(ctx, xdgOpen, target)
	return err
}

// openWithFallback tries detached launch first and falls back to the Wails
// browser opener on any error (missing binaries, unavailable systemd --user
// bus, timeout, or non-zero exit). Target is intentionally omitted from logs
// because URLs may carry SSO tokens in query parameters.
func openWithFallback(kind, target string, detached func(context.Context, string) error, fallback func(string) error) error {
	err := detached(context.Background(), target)
	if err == nil {
		return nil
	}
	slog.Warn("detached open failed, falling back to Wails browser", "kind", kind, "err", err)
	return fallback(target)
}
