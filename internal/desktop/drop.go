package desktop

import (
	"context"
	"log/slog"
	"strconv"
)

// dropTarget takes files dropped onto a channel (api.Service).
type dropTarget interface {
	AttachDropped(ctx context.Context, id int64, channelID string, paths []string) int
}

// filesDropped handles Wails' WindowFilesDropped: attrs are the attributes
// of the page element under the drop that carries data-file-drop-target
// (drops elsewhere never get here); its data-srv and data-channel say
// which channel's next message the files go to. The paths come from the
// native drop but pass through the page on their way (Wails' runtime), so
// the service takes regular files only and just stages them — shown as
// chips, sent only by the user.
func filesDropped(t dropTarget, attrs map[string]string, paths []string) {
	srv, err := strconv.ParseInt(attrs["data-srv"], 10, 64)
	ch := attrs["data-channel"]
	if err != nil || ch == "" || len(paths) == 0 {
		slog.Warn("file drop ignored: no channel target", "files", len(paths))
		return
	}
	t.AttachDropped(context.Background(), srv, ch, paths)
}
