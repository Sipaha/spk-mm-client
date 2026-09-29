package desktop

import "testing"

func TestTrayClickHidesOnlyTheActiveWindow(t *testing.T) {
	for _, c := range []struct {
		name                       string
		visible, active, minimized bool
		hides                      bool
	}{
		{"hidden (closed to the tray)", false, false, false, false},
		{"visible behind other windows", true, false, false, false},
		{"visible and focused", true, true, false, true},
		{"minimized", true, false, true, false},
		// GTK may still report focus for an iconified window for a moment.
		{"minimized, focus not yet dropped", true, true, true, false},
		{"hidden, stale focus", false, true, false, false},
	} {
		if got := trayClickHides(c.visible, c.active, c.minimized); got != c.hides {
			t.Errorf("%s: trayClickHides(%v, %v, %v) = %v, want %v",
				c.name, c.visible, c.active, c.minimized, got, c.hides)
		}
	}
}
