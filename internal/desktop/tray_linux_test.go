//go:build wails && linux

package desktop

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/prop"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/spk/spk-mm-client/internal/api"
)

func startIsolatedDBus(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("dbus-daemon not installed")
	}
	// Short path for Unix domain socket (<108 bytes).
	dir, err := os.MkdirTemp("", "spk-dbus-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	emptySvc := filepath.Join(dir, "services")
	require.NoError(t, os.MkdirAll(emptySvc, 0o700))
	sock := filepath.Join(dir, "bus")
	addr := "unix:path=" + sock
	conf := filepath.Join(dir, "dbus.conf")
	xml := fmt.Sprintf(`<!DOCTYPE busconfig PUBLIC "-//freedesktop//DTD D-Bus Bus Configuration 1.0//EN"
 "http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig>
  <type>session</type>
  <listen>%s</listen>
  <servicedir>%s</servicedir>
  <policy context="default">
    <allow send_destination="*" eavesdrop="true"/>
    <allow eavesdrop="true"/>
    <allow own="*"/>
  </policy>
</busconfig>
`, addr, emptySvc)
	require.NoError(t, os.WriteFile(conf, []byte(xml), 0o600))

	cmd := exec.Command("dbus-daemon", "--config-file="+conf, "--nofork", "--nopidfile")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	require.Eventually(t, func() bool {
		_, err := os.Stat(sock)
		return err == nil
	}, 2*time.Second, 10*time.Millisecond)
	return addr
}

type wailsPX struct {
	W, H int
	Pix  []byte
}

type wailsToolTip = struct {
	V0 string
	V1 []wailsPX
	V2 string
	V3 string
}

func exportFakeWailsSNI(t *testing.T, owner *dbus.Conn, dest string) *prop.Properties {
	t.Helper()
	reply, err := owner.RequestName(dest, dbus.NameFlagDoNotQueue)
	require.NoError(t, err)
	require.Equal(t, dbus.RequestNameReplyPrimaryOwner, reply)

	props, err := prop.Export(owner, sniObjectPath, map[string]map[string]*prop.Prop{
		sniInterface: {
			"Id": {
				Value:    "spk-mm-client",
				Writable: false,
				Emit:     prop.EmitTrue,
			},
			"ToolTip": {
				Value:    wailsToolTip{V2: "spk-mm-client"},
				Writable: true,
				Emit:     prop.EmitTrue,
			},
		},
	})
	require.NoError(t, err)
	return props
}

func TestLinuxSNIReadinessAndToolTipUpdate(t *testing.T) {
	addr := startIsolatedDBus(t)

	owner, err := dbus.Connect(addr)
	require.NoError(t, err)
	defer func() { _ = owner.Close() }()

	cli, err := dbus.Connect(addr)
	require.NoError(t, err)
	defer func() { _ = cli.Close() }()

	watcher, err := dbus.Connect(addr)
	require.NoError(t, err)
	defer func() { _ = watcher.Close() }()

	const dest = "org.kde.StatusNotifierItem-999999-1"
	ctx := context.Background()

	assert.False(t, sniExported(ctx, cli, dest), "not ready before SNI properties are exported")
	props := exportFakeWailsSNI(t, owner, dest)
	assert.True(t, sniExported(ctx, cli, dest), "ready once SNI Id property is readable")

	require.NoError(t, watcher.AddMatchSignal(
		dbus.WithMatchObjectPath(sniObjectPath),
		dbus.WithMatchInterface(sniInterface),
		dbus.WithMatchMember("NewToolTip"),
	))
	sigs := make(chan *dbus.Signal, 4)
	watcher.Signal(sigs)

	want := "spk-mm-client — есть непрочитанные"
	require.NoError(t, setSNIToolTip(ctx, cli, owner, dest, want))

	got := props.GetMust(sniInterface, "ToolTip").(wailsToolTip)
	assert.Equal(t, want, got.V2, "SNI ToolTip title updated in exported properties")
	assert.Empty(t, got.V3, "SNI ToolTip description remains empty to prevent duplicate tooltip lines")

	select {
	case sig := <-sigs:
		assert.Equal(t, sniInterface+".NewToolTip", sig.Name)
		assert.Equal(t, owner.Names()[0], sig.Sender, "NewToolTip must originate from the SNI owner connection")
	case <-time.After(time.Second):
		t.Fatal("did not receive org.kde.StatusNotifierItem.NewToolTip signal")
	}
}

// R1 regression over a real isolated D-Bus daemon: when SNI export is delayed
// past the initial warning threshold (and the initial bus address only appears
// after transient failures), the consumer keeps waiting, applies the latest
// held mention badge once SNI exports, and returns to plain when read.
func TestLinuxDelayedSNIExportPastWarningAppliesHeldBadgeAndRecoversToPlain(t *testing.T) {
	addr := startIsolatedDBus(t)

	owner, err := dbus.Connect(addr)
	require.NoError(t, err)
	defer func() { _ = owner.Close() }()

	const dest = "org.kde.StatusNotifierItem-999998-1"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		busOnline atomic.Bool
		warns     atomic.Int32
		cli       *dbus.Conn
		mu        sync.Mutex
		icons     []trayIcon
	)
	defer func() {
		if cli != nil {
			_ = cli.Close()
		}
	}()

	ch := make(chan api.Badge, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runTrayBadge(ctx, ch, "en", trayApplier{
			waitReady: func(c context.Context) bool {
				return waitUntilReady(c, 30*time.Millisecond, 5*time.Millisecond, 10*time.Millisecond,
					func(c context.Context) bool {
						if cli == nil {
							target := deadBusAddress
							if busOnline.Load() {
								target = addr
							}
							conn, err := dbus.Connect(target)
							if err != nil {
								return false
							}
							cli = conn
						}
						return sniExported(c, cli, dest)
					},
					func() { warns.Add(1) },
				)
			},
			setText: func(text string) {
				require.NoError(t, setSNIToolTip(ctx, cli, owner, dest, text))
			},
			setIcon: func(ic trayIcon) {
				mu.Lock()
				icons = append(icons, ic)
				mu.Unlock()
			},
		})
	}()

	// Queue unread -> mention before bus/SNI are available.
	offerLatest(ch, api.Badge{Unread: true, Mentions: 0})
	offerLatest(ch, api.Badge{Unread: true, Mentions: 1})

	// Let initial warning threshold expire while bus connection fails, then bring bus up while SNI is still unexported.
	require.Eventually(t, func() bool { return warns.Load() == 1 }, time.Second, 5*time.Millisecond)
	busOnline.Store(true)
	time.Sleep(35 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("consumer exited before SNI export arrived")
	default:
	}

	// Export SNI late: held mention badge must apply to both icon and D-Bus ToolTip.
	props := exportFakeWailsSNI(t, owner, dest)
	require.Eventually(t, func() bool {
		mu.Lock()
		gotIcons := append([]trayIcon(nil), icons...)
		mu.Unlock()
		tt := props.GetMust(sniInterface, "ToolTip").(wailsToolTip)
		return len(gotIcons) == 1 && gotIcons[0] == iconMention && tt.V2 == "spk-mm-client — 1 mention" && tt.V3 == ""
	}, time.Second, 5*time.Millisecond)

	// Subsequent read clears back to plain.
	offerLatest(ch, api.Badge{})
	require.Eventually(t, func() bool {
		mu.Lock()
		gotIcons := append([]trayIcon(nil), icons...)
		mu.Unlock()
		tt := props.GetMust(sniInterface, "ToolTip").(wailsToolTip)
		return len(gotIcons) == 2 && gotIcons[1] == iconPlain && tt.V2 == "spk-mm-client" && tt.V3 == ""
	}, time.Second, 5*time.Millisecond)

	cancel()
	<-done
}

func TestSetTrayTextOnLinuxKeepsSingleLineLabel(t *testing.T) {
	tray := &application.SystemTray{}
	setTrayText(tray, nil, "spk-mm-client — 2 mentions")
	assert.Equal(t, "spk-mm-client — 2 mentions", tray.Label())
	// In Wails 3.1.0, linuxSystemTray.updateToolTip publishes tooltip{V2: s.label, V3: s.tooltip};
	// keeping s.tooltip empty prevents xapp-sn-watcher from rendering "text\ntext".
	tooltipField := reflect.ValueOf(tray).Elem().FieldByName("tooltip")
	require.True(t, tooltipField.IsValid(), "expected SystemTray.tooltip field in Wails 3.1.0")
	assert.Empty(t, tooltipField.String())
}
