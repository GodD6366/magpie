package gui

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// A right-click on KDE opens the tray's menu, and Wails calls the click
// handler from the menu's "opened" event; that call must not open the
// panel, while the icon's own click (Activate) still does (#1430).
func TestTrayMenuOpeningIsNoClick(t *testing.T) {
	const app = "github.com/wailsapp/wails/v3/pkg/application."
	opened := []string{
		"magpie/internal/gui.Run.func12",
		app + "(*linuxSystemTray).Event",
		"reflect.Value.call",
		"github.com/godbus/dbus/v5.(*Conn).handleCall",
	}
	if !openedBy(opened) {
		t.Fatal("a click from the menu's opened event was counted a click")
	}
	activate := []string{
		"magpie/internal/gui.Run.func12",
		app + "(*linuxSystemTray).Activate",
		"reflect.Value.call",
	}
	if openedBy(activate) {
		t.Fatal("the icon's click (Activate) was taken for the menu opening")
	}
	if openedBy([]string{app + "(*linuxSystemTray).EventGroup"}) {
		t.Fatal("EventGroup is not Event")
	}
	if menuOpening() {
		t.Fatal("a plain call was taken for the menu opening")
	}
}

// The frame menuOpening looks for is the Wails function that calls the
// click handler on the menu's "opened" event; an upgrade that renames it
// or stops calling the handler there fails here.
func TestTrayMenuOpenFrameIsWails(t *testing.T) {
	// go list -m names no folder for a module not yet unpacked, as on CI;
	// go mod download unpacks it and names its folder
	out, err := exec.Command("go", "mod", "download", "-json", "github.com/wailsapp/wails/v3").Output()
	if err != nil {
		t.Skip("go mod download:", err)
	}
	var mod struct{ Dir string }
	if err := json.Unmarshal(out, &mod); err != nil || mod.Dir == "" {
		t.Fatalf("go mod download named no folder: %s", out)
	}
	src, err := os.ReadFile(filepath.Join(mod.Dir, "pkg", "application", "systemtray_linux.go"))
	if err != nil {
		t.Fatal(err)
	}
	fn := regexp.MustCompile(`(?s)func \(s \*linuxSystemTray\) Event\(.*?\n}\n`).Find(src)
	if fn == nil {
		t.Fatalf("Wails has no (*linuxSystemTray).Event; menuOpenFrame %q is stale", menuOpenFrame)
	}
	if !regexp.MustCompile(`(?s)case "opened":\s*if s\.parent\.clickHandler != nil \{\s*s\.parent\.clickHandler\(\)`).Match(fn) {
		t.Fatalf("Wails' Event no longer calls the click handler on \"opened\"; see menuOpening:\n%s", fn)
	}
}
