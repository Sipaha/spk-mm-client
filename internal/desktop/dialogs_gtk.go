//go:build wails && gtk3

package desktop

/*
#cgo linux pkg-config: gtk+-3.0
#include <gtk/gtk.h>

static void spk_cancel_file_dialogs(void) {
	GList *all = gtk_window_list_toplevels();
	for (GList *l = all; l != NULL; l = l->next) {
		if (GTK_IS_FILE_CHOOSER_DIALOG(l->data)) gtk_dialog_response(GTK_DIALOG(l->data), GTK_RESPONSE_CANCEL);
	}
	g_list_free(all);
}
*/
import "C"

// cancelFileDialogs cancels an open 📎 dialog, on the GTK main thread (a
// Wails shutdown task). Wails runs the dialog with gtk_dialog_run, a nested
// main loop that quitting the application does not end: with the dialog
// open, a quit (tray, SIGTERM at logout) would hang until the user closed
// it.
func cancelFileDialogs() { C.spk_cancel_file_dialogs() }
