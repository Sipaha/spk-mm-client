//go:build wails && !linux

package desktop

// sessionBusState: Windows (named mutex + message window, shell tray) and
// macOS (NSDistributedNotificationCenter, NSStatusItem) don't use D-Bus, so
// there is nothing to probe.
func sessionBusState() busState { return busOK }

func cutOffSessionBus() {}
