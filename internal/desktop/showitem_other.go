//go:build !linux

package desktop

import (
	"context"
	"errors"
)

// showItems: FileManager1 is a freedesktop (D-Bus) interface; elsewhere the
// caller opens the folder instead.
func showItems(context.Context, string) error { return errors.ErrUnsupported }
