package desktop

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"time"
)

// showItemTimeout bounds "Show in folder": the D-Bus connect has no timeout
// of its own, and a hung bus must not hang the action.
const showItemTimeout = 2 * time.Second

var errShowItemTimeout = errors.New("file manager did not answer")

// ShowItem asks the file manager to show path selected
// (org.freedesktop.FileManager1.ShowItems on the session bus: Nautilus,
// Nemo, Dolphin, Thunar…), bounded by showItemTimeout. Only on the user's
// action, never at startup. On an error the caller opens the folder
// instead (api.Service.RevealDownload).
func ShowItem(ctx context.Context, path string) error {
	return showItem(ctx, path, showItems, showItemTimeout)
}

func showItem(ctx context.Context, path string, call func(ctx context.Context, uri string) error, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel() // also ends an abandoned call's context
	ok, err := startWithTimeout(ctx, func(ctx context.Context) error { return call(ctx, fileURI(path)) }, timeout)
	switch {
	case ok:
		return nil
	case err != nil:
		return err
	}
	return errShowItemTimeout
}

// fileURI is the file:// URI of an absolute path, percent-escaped.
func fileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}
