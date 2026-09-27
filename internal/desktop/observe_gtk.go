//go:build wails && gtk3

package desktop

/*
#cgo linux pkg-config: gtk+-3.0
#include <gtk/gtk.h>

// Go side, in clipboard_gtk_cb.go.
extern void spkNativeDrop(char **paths, int n);
extern void spkPasteKey(void);

// The paths of a file drop on the webview from another application, parsed the way Wails does
// (g_uri_list_extract_uris + g_filename_from_uri). Connected after Wails'
// own handler; it only reads the selection data (a void signal: every
// handler runs, nothing is stopped).
static void spk_on_drag_data(GtkWidget *w, GdkDragContext *ctx, gint x, gint y,
		GtkSelectionData *sd, guint info, guint time, gpointer d) {
	if (sd == NULL || gtk_selection_data_get_length(sd) <= 0) return;
	// A drag that starts in this process — the page's own drag, whose
	// text/uri-list script can set to any path — is not a native file drop.
	if (gtk_drag_get_source_widget(ctx) != NULL) return;
	gchar *target = gdk_atom_name(gtk_selection_data_get_target(sd));
	gboolean uris = g_strcmp0(target, "text/uri-list") == 0;
	g_free(target);
	if (!uris) return;
	gchar *text = g_strndup((const gchar *)gtk_selection_data_get_data(sd), gtk_selection_data_get_length(sd));
	gchar **list = g_uri_list_extract_uris(text);
	g_free(text);
	GPtrArray *paths = g_ptr_array_new_with_free_func(g_free);
	for (gint i = 0; list != NULL && list[i] != NULL; i++) {
		gchar *p = g_filename_from_uri(list[i], NULL, NULL);
		if (p != NULL) g_ptr_array_add(paths, p);
	}
	g_strfreev(list);
	spkNativeDrop((char **)paths->pdata, (int)paths->len);
	g_ptr_array_free(paths, TRUE);
}

// want in the key's own layout or in any other group of the same key
// (Ctrl+V with a Cyrillic layout gives Cyrillic_em).
static gboolean spk_key_is(GdkEventKey *e, guint want) {
	if (gdk_keyval_to_lower(e->keyval) == want) return TRUE;
	GdkKeymapKey *keys = NULL;
	guint *vals = NULL;
	gint n = 0;
	gboolean hit = FALSE;
	if (gdk_keymap_get_entries_for_keycode(gdk_keymap_get_for_display(gdk_window_get_display(e->window)),
			e->hardware_keycode, &keys, &vals, &n)) {
		for (gint i = 0; i < n; i++) if (gdk_keyval_to_lower(vals[i]) == want) hit = TRUE;
		g_free(keys);
		g_free(vals);
	}
	return hit;
}

// Notes a paste key; never consumes it (returns FALSE: WebKit still gets it).
static gboolean spk_on_key(GtkWidget *w, GdkEventKey *e, gpointer d) {
	GdkModifierType m = e->state & gtk_accelerator_get_default_mod_mask();
	if ((m == GDK_CONTROL_MASK || m == (GDK_CONTROL_MASK | GDK_SHIFT_MASK)) && spk_key_is(e, GDK_KEY_v)) {
		spkPasteKey();
	} else if (m == GDK_SHIFT_MASK && (e->keyval == GDK_KEY_Insert || e->keyval == GDK_KEY_KP_Insert)) {
		spkPasteKey();
	}
	return FALSE;
}

static GtkWidget *spk_find_webview(GtkWidget *w) {
	if (g_strcmp0(G_OBJECT_TYPE_NAME(w), "WebKitWebView") == 0) return w;
	if (!GTK_IS_CONTAINER(w)) return NULL;
	GList *kids = gtk_container_get_children(GTK_CONTAINER(w));
	GtkWidget *found = NULL;
	for (GList *l = kids; l != NULL && found == NULL; l = l->next) found = spk_find_webview(GTK_WIDGET(l->data));
	g_list_free(kids);
	return found;
}

// The window gets key presses before the focused webview does.
static int spk_observe(void *window) {
	if (window == NULL) return 0;
	g_signal_connect(G_OBJECT(window), "key-press-event", G_CALLBACK(spk_on_key), NULL);
	GtkWidget *wv = spk_find_webview(GTK_WIDGET(window));
	if (wv == NULL) return 0;
	g_signal_connect(G_OBJECT(wv), "drag-data-received", G_CALLBACK(spk_on_drag_data), NULL);
	return 1;
}
*/
import "C"

import "unsafe"

// observeNatively connects the native observers the page cannot fake: the
// paste keys pressed in window (pasteKeys) and the paths of file drops on
// its webview (nativeDrops). On the GTK main thread, once the window
// exists. false: the webview was not found — every drop is refused then.
func observeNatively(window unsafe.Pointer) bool {
	installDialogGuard()
	return C.spk_observe(window) == 1
}
