//go:build linux

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const prSetChildSubreaper = 36

func TestEscapeSystemdExecArg(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "https://example.com/a?b=1", want: "https://example.com/a?b=1"},
		{in: "${HOME}", want: "$${HOME}"},
		{in: "$HOME", want: "$$HOME"},
		{in: "$$", want: "$$$$"},
		{in: "%h", want: "%h"},
		{in: `a b "c" 'd'`, want: `a b "c" 'd'`},
		{in: "-leading-dash.pdf", want: "-leading-dash.pdf"},
		{in: "/tmp/${USER}/$HOME/$$/%h/file name.txt", want: "/tmp/$${USER}/$$HOME/$$$$/%h/file name.txt"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, escapeSystemdExecArg(tc.in), "input=%q", tc.in)
	}
}

func TestBuildSystemdRunArgs(t *testing.T) {
	env := map[string]string{
		"DISPLAY":             ":0",
		"WAYLAND_DISPLAY":     "wayland-0",
		"XAUTHORITY":          "/run/user/1000/Xauthority",
		"XDG_CURRENT_DESKTOP": "",
		"DESKTOP_SESSION":     "gnome",
		"SECRET_TOKEN":        "must-not-be-forwarded",
		"HOME":                "/home/alice",
	}
	o := linuxOpener{
		getenv: func(k string) string { return env[k] },
	}

	target := `https://example.com/path?a=${HOME}&b=$HOME&c=$$&d=%h&q="x" 'y'`
	args := o.buildSystemdRunArgs("spk-mm-client-open-deadbeef", "/usr/bin/xdg-open", target)

	want := []string{
		"--user",
		"--no-block",
		"--collect",
		"--quiet",
		"--property=ExitType=cgroup",
		"--unit=spk-mm-client-open-deadbeef",
		"--setenv=DISPLAY=:0",
		"--setenv=WAYLAND_DISPLAY=wayland-0",
		"--setenv=XAUTHORITY=/run/user/1000/Xauthority",
		"--setenv=DESKTOP_SESSION=gnome",
		"--",
		"/usr/bin/xdg-open",
		`https://example.com/path?a=$${HOME}&b=$$HOME&c=$$$$&d=%h&q="x" 'y'`,
	}
	assert.Equal(t, want, args)
	assert.NotContains(t, args, "--scope", "must use transient service, never --scope")
	assert.NotContains(t, args, "--expand-environment=no", "must work on systemd < 254 via $ -> $$ escaping")

	// Leading-dash targets appear strictly after "--" so systemd-run does not parse them as flags.
	dashArgs := o.buildSystemdRunArgs("spk-mm-client-open-01", "/usr/bin/xdg-open", "-weird-file.pdf")
	sepIdx := slices.Index(dashArgs, "--")
	require.NotEqual(t, -1, sepIdx)
	assert.Equal(t, []string{"/usr/bin/xdg-open", "-weird-file.pdf"}, dashArgs[sepIdx+1:])
}

func TestLinuxOpenerSuccessTimeoutAndFallback(t *testing.T) {
	t.Run("success_skips_fallback", func(t *testing.T) {
		var gotName string
		var gotArgs []string
		o := linuxOpener{
			lookPath: func(bin string) (string, error) { return "/usr/bin/" + bin, nil },
			getenv:   func(string) string { return "" },
			randHex:  func() (string, error) { return "0123456789abcdef", nil },
			run: func(_ context.Context, name string, args ...string) ([]byte, error) {
				gotName = name
				gotArgs = args
				return nil, nil
			},
			timeout: time.Second,
		}
		fallbackCalled := false
		err := openWithFallback("url", "https://example.com/$HOME", o.open, func(string) error {
			fallbackCalled = true
			return nil
		})
		require.NoError(t, err)
		assert.False(t, fallbackCalled)
		assert.Equal(t, "/usr/bin/systemd-run", gotName)
		assert.Contains(t, gotArgs, "--unit=spk-mm-client-open-0123456789abcdef")
		assert.Equal(t, "https://example.com/$$HOME", gotArgs[len(gotArgs)-1])
	})

	t.Run("missing_systemd_run_falls_back", func(t *testing.T) {
		o := linuxOpener{
			lookPath: func(bin string) (string, error) {
				if bin == "systemd-run" {
					return "", exec.ErrNotFound
				}
				return "/usr/bin/" + bin, nil
			},
			randHex: func() (string, error) { return "abc", nil },
			run:     func(context.Context, string, ...string) ([]byte, error) { return nil, nil },
		}
		var fbTarget string
		err := openWithFallback("url", "https://example.com", o.open, func(u string) error {
			fbTarget = u
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, "https://example.com", fbTarget)
	})

	t.Run("missing_xdg_open_falls_back", func(t *testing.T) {
		o := linuxOpener{
			lookPath: func(bin string) (string, error) {
				if bin == "xdg-open" {
					return "", exec.ErrNotFound
				}
				return "/usr/bin/" + bin, nil
			},
			randHex: func() (string, error) { return "abc", nil },
			run:     func(context.Context, string, ...string) ([]byte, error) { return nil, nil },
		}
		var fbTarget string
		err := openWithFallback("file", "/tmp/report.pdf", o.open, func(p string) error {
			fbTarget = p
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, "/tmp/report.pdf", fbTarget)
	})

	t.Run("nonzero_exit_falls_back", func(t *testing.T) {
		o := linuxOpener{
			lookPath: func(bin string) (string, error) { return "/usr/bin/" + bin, nil },
			getenv:   func(string) string { return "" },
			randHex:  func() (string, error) { return "abc", nil },
			run: func(context.Context, string, ...string) ([]byte, error) {
				return []byte("Failed to connect to bus"), errors.New("exit status 1")
			},
			timeout: time.Second,
		}
		var fbTarget string
		err := openWithFallback("file", "/tmp/a.txt", o.open, func(p string) error {
			fbTarget = p
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, "/tmp/a.txt", fbTarget)
	})

	t.Run("timeout_cancels_and_falls_back", func(t *testing.T) {
		o := linuxOpener{
			lookPath: func(bin string) (string, error) { return "/usr/bin/" + bin, nil },
			getenv:   func(string) string { return "" },
			randHex:  func() (string, error) { return "abc", nil },
			run: func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
			timeout: 50 * time.Millisecond,
		}
		start := time.Now()
		fallbackCalled := false
		err := openWithFallback("url", "https://example.com", o.open, func(string) error {
			fallbackCalled = true
			return nil
		})
		require.NoError(t, err)
		assert.True(t, fallbackCalled)
		assert.Less(t, time.Since(start), time.Second)
	})
}

func requireSystemdUser(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("systemd-run"); err != nil {
		t.Skip("systemd-run not found")
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		t.Skip("systemctl not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemd-run", "--user", "--wait", "--collect", "--quiet", "/bin/true").CombinedOutput()
	if err != nil {
		t.Skipf("systemd --user unavailable: %v (%s)", err, strings.TrimSpace(string(out)))
	}
}

func stopUserUnit(unit string) {
	if unit == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "systemctl", "--user", "stop", unit).Run()
	_ = exec.CommandContext(ctx, "systemctl", "--user", "reset-failed", unit).Run()
}

func procPPID(pid int) (int, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	// Format: pid (comm) state ppid ... — comm may contain spaces/parens, so find last ')'.
	s := string(data)
	idx := strings.LastIndexByte(s, ')')
	if idx < 0 || idx+2 >= len(s) {
		return 0, fmt.Errorf("malformed /proc/%d/stat", pid)
	}
	fields := strings.Fields(s[idx+2:])
	if len(fields) < 2 {
		return 0, fmt.Errorf("short /proc/%d/stat", pid)
	}
	return strconv.Atoi(fields[1])
}

func isDescendantOf(pid, ancestor int) bool {
	seen := map[int]bool{}
	cur := pid
	for cur > 1 && !seen[cur] {
		seen[cur] = true
		ppid, err := procPPID(cur)
		if err != nil || ppid <= 0 {
			return false
		}
		if ppid == ancestor {
			return true
		}
		cur = ppid
	}
	return false
}

// TestOpenLinuxHelperProcess is re-executed by the native systemd-run smoke
// tests below to avoid depending on external shells/interpreters.
func TestOpenLinuxHelperProcess(_ *testing.T) {
	args := flag.Args()
	if len(args) == 0 {
		return
	}
	switch args[0] {
	case "dump-args":
		if len(args) < 2 {
			os.Exit(2)
		}
		outFile := args[1]
		payload, err := json.Marshal(args[2:])
		if err != nil {
			os.Exit(3)
		}
		tmp := outFile + ".tmp"
		if err := os.WriteFile(tmp, payload, 0o600); err != nil {
			os.Exit(4)
		}
		if err := os.Rename(tmp, outFile); err != nil {
			os.Exit(5)
		}
		os.Exit(0)

	case "spawn-grandchild":
		if len(args) < 2 {
			os.Exit(2)
		}
		grandchildFile := args[1]
		exe, err := os.Executable()
		if err != nil {
			os.Exit(3)
		}
		cmd := exec.Command(exe, "-test.run=^TestOpenLinuxHelperProcess$", "--", "grandchild-sleep", grandchildFile)
		if err := cmd.Start(); err != nil {
			os.Exit(4)
		}
		// Exit immediately like xdg-open after spawning the target app;
		// ExitType=cgroup must keep the backgrounded grandchild alive.
		os.Exit(0)

	case "grandchild-sleep":
		if len(args) < 2 {
			os.Exit(2)
		}
		grandchildFile := args[1]
		tmp := grandchildFile + ".tmp"
		if err := os.WriteFile(tmp, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			os.Exit(3)
		}
		if err := os.Rename(tmp, grandchildFile); err != nil {
			os.Exit(4)
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)

	case "subreaper-parent":
		if len(args) < 3 {
			os.Exit(2)
		}
		unitFile, grandchildFile := args[1], args[2]
		if _, _, errno := syscall.Syscall(syscall.SYS_PRCTL, prSetChildSubreaper, 1, 0); errno != 0 {
			_, _ = fmt.Fprintf(os.Stderr, "prctl(PR_SET_CHILD_SUBREAPER): %v\n", errno)
			os.Exit(3)
		}
		exe, err := os.Executable()
		if err != nil {
			os.Exit(4)
		}
		unit, err := newLinuxOpener().launchCommand(
			context.Background(),
			exe,
			"-test.run=^TestOpenLinuxHelperProcess$",
			"--",
			"spawn-grandchild",
			grandchildFile,
		)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "launchCommand: %v\n", err)
			os.Exit(5)
		}
		if err := os.WriteFile(unitFile+".tmp", []byte(unit), 0o600); err != nil {
			os.Exit(6)
		}
		if err := os.Rename(unitFile+".tmp", unitFile); err != nil {
			os.Exit(7)
		}
		// Wait until the backgrounded grandchild reports its PID so we can check
		// while this subreaper is alive that the grandchild is not in our tree.
		deadline := time.Now().Add(5 * time.Second)
		var gpid int
		for time.Now().Before(deadline) {
			if raw, err := os.ReadFile(grandchildFile); err == nil {
				if p, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && p > 0 {
					gpid = p
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		if gpid == 0 {
			_, _ = fmt.Fprintln(os.Stderr, "grandchild never wrote pid")
			os.Exit(8)
		}
		if isDescendantOf(gpid, os.Getpid()) {
			_, _ = fmt.Fprintf(os.Stderr, "grandchild %d ended up inside subreaper %d tree\n", gpid, os.Getpid())
			os.Exit(9)
		}
		os.Exit(0)
	}
}

func TestNativeSystemdRunPreservesLiteralArgs(t *testing.T) {
	requireSystemdUser(t)

	exe, err := os.Executable()
	require.NoError(t, err)

	dir := t.TempDir()
	outFile := filepath.Join(dir, "args.json")
	want := []string{
		"${HOME}",
		"$HOME",
		"$$",
		"%h",
		`a b "c" 'd'`,
		"-flag",
	}

	cmdArgs := append([]string{"-test.run=^TestOpenLinuxHelperProcess$", "--", "dump-args", outFile}, want...)
	unit, err := newLinuxOpener().launchCommand(context.Background(), exe, cmdArgs...)
	require.NoError(t, err)
	t.Cleanup(func() { stopUserUnit(unit) })

	var got []string
	require.Eventually(t, func() bool {
		raw, err := os.ReadFile(outFile)
		if err != nil {
			return false
		}
		return json.Unmarshal(raw, &got) == nil
	}, 5*time.Second, 25*time.Millisecond, "transient systemd unit never wrote args file")
	assert.Equal(t, want, got, "all literal characters including $, %%, spaces, quotes and leading dash must arrive unexpanded")
}

func TestNativeSystemdRunDetachesFromSubreaper(t *testing.T) {
	requireSystemdUser(t)

	exe, err := os.Executable()
	require.NoError(t, err)

	dir := t.TempDir()
	unitFile := filepath.Join(dir, "unit.txt")
	grandchildFile := filepath.Join(dir, "grandchild.pid")

	var unitName string
	var grandchildPID int
	t.Cleanup(func() {
		if unitName == "" {
			if raw, err := os.ReadFile(unitFile); err == nil {
				unitName = strings.TrimSpace(string(raw))
			}
		}
		stopUserUnit(unitName)
		if grandchildPID == 0 {
			if raw, err := os.ReadFile(grandchildFile); err == nil {
				grandchildPID, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
			}
		}
		if grandchildPID > 0 {
			_ = syscall.Kill(grandchildPID, syscall.SIGKILL)
		}
	})

	cmd := exec.Command(exe, "-test.run=^TestOpenLinuxHelperProcess$", "--", "subreaper-parent", unitFile, grandchildFile)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "subreaper-parent failed: %s", string(out))
	subreaperPID := cmd.Process.Pid

	rawUnit, err := os.ReadFile(unitFile)
	require.NoError(t, err)
	unitName = strings.TrimSpace(string(rawUnit))
	require.True(t, strings.HasPrefix(unitName, openUnitPrefix))

	rawPID, err := os.ReadFile(grandchildFile)
	require.NoError(t, err)
	grandchildPID, err = strconv.Atoi(strings.TrimSpace(string(rawPID)))
	require.NoError(t, err)
	require.Greater(t, grandchildPID, 1)

	// Give systemd a brief window to confirm ExitType=cgroup keeps the
	// backgrounded grandchild alive after both spawn-grandchild and
	// subreaper-parent have exited.
	time.Sleep(150 * time.Millisecond)
	require.NoError(t, syscall.Kill(grandchildPID, 0), "grandchild must survive launcher exit under ExitType=cgroup")
	assert.False(t, isDescendantOf(grandchildPID, subreaperPID), "grandchild must not belong to subreaper tree")
	assert.False(t, isDescendantOf(grandchildPID, os.Getpid()), "grandchild must not belong to test process tree")
}
