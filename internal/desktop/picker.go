//go:build wails

package desktop

import (
	"context"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// filePicker is the 📎 dialog: Wails' native open-file dialog, several
// files at once. It is the one modal dialog of the app, opened only by
// the user's click (a binding call — never at startup) and never from the
// GTK main thread: Wails runs it there as a nested loop and the calling
// goroutine waits for it, so no lock may be held around it.
type filePicker struct {
	open   sync.Mutex // one dialog at a time: a second click while it is open does nothing
	window func() application.Window
	title  string
}

func (p *filePicker) PickFiles(context.Context) ([]string, error) {
	if !p.open.TryLock() {
		return nil, nil
	}
	defer p.open.Unlock()
	d := application.Get().Dialog.OpenFile().CanChooseFiles(true).SetTitle(p.title)
	if w := p.window(); w != nil {
		d.AttachToWindow(w)
	}
	return d.PromptForMultipleSelection()
}

func pickerTitle(lang string) string {
	if lang == "ru" {
		return "Прикрепить файлы"
	}
	return "Attach files"
}
