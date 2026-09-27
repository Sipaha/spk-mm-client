//go:build wails && gtk3

package desktop

/*
#cgo linux pkg-config: gtk+-3.0
#include <gtk/gtk.h>

// Set on quit (main thread only). A dialog that maps after it — Wails
// built and ran it just after the quit task looked — is cancelled from an
// idle callback, which runs inside gtk_dialog_run's own loop.
static gboolean spk_quitting = FALSE;

static gboolean spk_cancel_later(gpointer d) {
	if (GTK_IS_DIALOG(d)) gtk_dialog_response(GTK_DIALOG(d), GTK_RESPONSE_CANCEL);
	g_object_unref(d);
	return G_SOURCE_REMOVE;
}

static gboolean spk_on_map(GSignalInvocationHint *hint, guint n, const GValue *params, gpointer data) {
	GObject *o = g_value_get_object(&params[0]);
	if (spk_quitting && GTK_IS_FILE_CHOOSER_DIALOG(o)) g_idle_add(spk_cancel_later, g_object_ref(o));
	return TRUE; // stay installed
}

static void spk_install_dialog_guard(void) {
	static gboolean done = FALSE;
	if (done) return;
	done = TRUE;
	g_signal_add_emission_hook(g_signal_lookup("map", GTK_TYPE_WIDGET), 0, spk_on_map, NULL, NULL);
}

static void spk_cancel_file_dialogs(void) {
	spk_quitting = TRUE;
	GList *all = gtk_window_list_toplevels();
	for (GList *l = all; l != NULL; l = l->next) {
		if (GTK_IS_FILE_CHOOSER_DIALOG(l->data)) gtk_dialog_response(GTK_DIALOG(l->data), GTK_RESPONSE_CANCEL);
	}
	g_list_free(all);
}
*/
import "C"

// cancelFileDialogs cancels an open 📎 dialog — and any that opens after
// it (installDialogGuard) — on the GTK main thread (a Wails shutdown
// task). Wails runs the dialog with gtk_dialog_run, a nested main loop that
// quitting the application does not end: with the dialog open, a quit
// (tray, SIGTERM at logout) would hang until the user closed it.
func cancelFileDialogs() { C.spk_cancel_file_dialogs() }

// installDialogGuard watches dialogs mapping (main thread, once).
func installDialogGuard() { C.spk_install_dialog_guard() }
