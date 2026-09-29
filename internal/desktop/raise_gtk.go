//go:build wails && gtk3

package desktop

/*
#cgo linux pkg-config: gtk+-3.0
#include <gtk/gtk.h>
#include <gdk/gdkx.h>

// On X11, stamps the window with the X server's current time as its last
// user interaction (_NET_WM_USER_TIME) and returns that time; 0 elsewhere
// (Wayland) or before the window is realized. A tray click reaches us over
// D-Bus with no X event, so GTK would otherwise present the window with its
// last input's time — older than the input in the focused window — and the
// WM's focus-stealing prevention (Muffin/Mutter/Metacity) would map it
// behind that window and flag it "demands attention" instead.
static guint32 spk_raise_stamp(GtkWindow *w) {
	GdkWindow *gw = gtk_widget_get_window(GTK_WIDGET(w));
	if (gw == NULL || !GDK_IS_X11_WINDOW(gw)) return 0;
	guint32 t = gdk_x11_get_server_time(gw);
	gdk_x11_window_set_user_time(gw, t);
	return t;
}

// Brings a shown window to the front and focuses it: un-minimizes it and
// asks the WM to activate it (_NET_ACTIVE_WINDOW) with a current timestamp,
// which the WM honours. Off X11 it is a plain gtk_window_present, as before.
static void spk_raise_present(GtkWindow *w) {
	gtk_window_set_urgency_hint(w, FALSE);
	guint32 t = spk_raise_stamp(w);
	if (t != 0) gtk_window_present_with_time(w, t);
	else gtk_window_present(w);
}
*/
import "C"

import "unsafe"

// nativeRaise shows the GTK window p through show and brings it to the
// front with focus. Main thread only. False (nothing done) when the native
// window does not exist yet.
func nativeRaise(p unsafe.Pointer, show func()) bool {
	if p == nil {
		return false
	}
	w := (*C.GtkWindow)(p)
	C.spk_raise_stamp(w) // before the map, so the map itself may take focus
	show()
	C.spk_raise_present(w)
	return true
}
