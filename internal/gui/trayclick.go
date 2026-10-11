package gui

import (
	"runtime"
	"strings"
)

// menuOpenFrame is the Wails function that tells the tray's click handler
// when Linux's tray host opens the icon's menu (com.canonical.dbusmenu's
// "opened" event). KDE Plasma opens the menu on a right-click, so a click
// counted there opened the quick panel too, which took the focus and shut
// the menu on Wayland at once (#1430).
const menuOpenFrame = "pkg/application.(*linuxSystemTray).Event"

// menuOpening says the click handler was called because the tray's menu is
// opening, not because the icon was clicked: Wails calls the same handler
// for both, and only the call stack tells them apart.
func menuOpening() bool {
	pcs := make([]uintptr, 32)
	n := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	var names []string
	for {
		f, more := frames.Next()
		names = append(names, f.Function)
		if !more {
			break
		}
	}
	return openedBy(names)
}

// openedBy says one of the callers is the menu's "opened" event.
func openedBy(callers []string) bool {
	for _, c := range callers {
		if strings.HasSuffix(c, menuOpenFrame) {
			return true
		}
	}
	return false
}
