package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"

	core "swiftdrop-core"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

var (
	flagPort     = flag.Int("port", core.DefaultPort, "port to serve on")
	flagName     = flag.String("name", "", "device name (defaults to hostname)")
	flagID       = flag.String("id", "", "override device id (for running multiple instances on one host)")
	flagHeadless = flag.Bool("headless", false, "run server + discovery without the UI (for testing)")
)

func main() {
	flag.Parse()

	if *flagHeadless {
		runHeadless(*flagPort)
		return
	}
	runApp(*flagPort)
}

// runApp is the normal mode: menu-bar icon + a frameless popover drawer that
// hosts the whole UI. Transfers run as goroutines in this process, so closing
// the drawer never interrupts them.
func runApp(port int) {
	id := core.LoadOrCreateIdentity(port)
	if *flagName != "" {
		id.Name = *flagName
	}
	if *flagID != "" {
		id.ID = *flagID
	}
	reg := core.NewPeerRegistry()
	reg.LoadKnown()
	reg.LoadManual()
	trk := core.NewTracker()
	core.InitPairStore()

	srv := core.NewServer(id, reg, trk)
	srv.WebFS = webFS

	app := application.New(application.Options{
		Name:        "SwiftDrop",
		Description: "Fast LAN file transfer",
		// Serve the existing UI + API through the same Go mux the peers use.
		Assets: application.AssetOptions{Handler: srv.Handler()},
		Mac: application.MacOptions{
			// Menu-bar only: no Dock icon, no app-switcher entry.
			ActivationPolicy: application.ActivationPolicyAccessory,
		},
	})

	srv.OnQuit = func() { app.Quit() }
	// ConsentHook is set after window creation (see below).

	core.StartServer(srv)

	ctx := context.Background()
	core.StartNetworkWatcher(ctx, id, reg)
	core.StartKeepalive(ctx, reg, id)
	core.StartLANScan(ctx, id, reg) // find peers that can't use mDNS (e.g. Windows)

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "swiftdrop",
		Width:            340,
		Height:           520,
		Frameless:        true,
		AlwaysOnTop:      true,
		Hidden:           true,
		DisableResize:    true,
		HideOnEscape:     true,
		HideOnFocusLost:  true,
		EnableFileDrop:   true,
		URL:              "/",
		BackgroundColour: application.NewRGBA(0, 0, 0, 0),
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 0,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
	})

	// Native file picker, invoked from the UI via /api/pick.
	// Declared here (after window creation) so we can re-show the window
	// after the dialog closes — HideOnFocusLost hides it when the picker
	// steals focus.
	srv.Pick = func() ([]string, error) {
		d := app.Dialog.OpenFile()
		d.CanChooseFiles(true)
		d.CanChooseDirectories(true)
		d.SetTitle("Choose files or folders to send")
		paths, err := d.PromptForMultipleSelection()
		// Re-show the window after the dialog closes.
		window.Show()
		return paths, err
	}

	// Keep the window alive when "closed" — just hide it (popover behaviour).
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		window.Hide()
		e.Cancel()
	})

	// Native drag-and-drop hands us real file paths → push them into the UI's
	// staging queue. This is what lets sends run at full speed (Go reads the
	// file directly) instead of through a browser upload.
	window.RegisterHook(events.Common.WindowFilesDropped, func(e *application.WindowEvent) {
		paths := e.Context().DroppedFiles()
		if len(paths) == 0 {
			return
		}
		infos := core.FileInfos(paths)
		data, _ := json.Marshal(infos)
		window.ExecJS(fmt.Sprintf("window.swiftdropOnDrop && window.swiftdropOnDrop(%s)", string(data)))
	})

	// Non-blocking consent: show the window so the user can accept/reject
	// in the web UI.  The notification is already sent by handleInbox.
	// This replaces the old blocking NSAlert that froze the app.
	srv.ConsentHook = func(tr *core.Transfer, from, name string, size int64) {
		window.Show()
		window.Focus()
	}

	tray := app.SystemTray.New()
	tray.SetTemplateIcon(core.TrayIcon())
	tray.SetTooltip("SwiftDrop")
	tray.AttachWindow(window).WindowOffset(6)

	// Right-click menu so the app is always quittable (left-click toggles the
	// drawer; the drawer also has a Quit button).
	menu := app.NewMenu()
	menu.Add("Open SwiftDrop").OnClick(func(*application.Context) { tray.ShowWindow() })
	menu.Add("Quit SwiftDrop").OnClick(func(*application.Context) { app.Quit() })
	tray.SetMenu(menu)
	tray.OnRightClick(func() { tray.OpenMenu() })

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// runHeadless starts the server + discovery with no UI. Used for automated
// testing and for running several instances on one host.
func runHeadless(port int) {
	id := core.LoadOrCreateIdentity(port)
	if *flagName != "" {
		id.Name = *flagName
	}
	if *flagID != "" {
		id.ID = *flagID
	}
	reg := core.NewPeerRegistry()
	reg.LoadKnown()
	reg.LoadManual()
	trk := core.NewTracker()
	core.InitPairStore()

	srv := core.NewServer(id, reg, trk)
	var ln net.Listener
	var err error
	for offset := 0; offset < 10; offset++ {
		p := id.Port + offset
		ln, err = net.Listen("tcp", fmt.Sprintf(":%d", p))
		if err == nil {
			if offset > 0 {
				log.Printf("port %d busy; using %d instead", id.Port, p)
				id.Port = p
			}
			break
		}
	}
	if err != nil {
		log.Fatalf("listen :%d (tried 10 ports): %v", id.Port, err)
	}
	core.StartNetworkWatcher(context.Background(), id, reg)
	core.StartKeepalive(context.Background(), reg, id)
	log.Printf("SwiftDrop %q listening on :%d (headless)", id.Name, id.Port)
	if err := http.Serve(ln, srv.LANHandler()); err != nil {
		log.Fatal(err)
	}
	os.Exit(0)
}
