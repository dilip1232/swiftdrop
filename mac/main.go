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

	core "github.com/dilip1232/swiftdrop/core"

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

// initCore sets up identity, peer registry, tracker, and server — shared by
// both GUI and headless modes.
func initCore(port int) (core.Identity, *core.PeerRegistry, *core.Tracker, *core.Server) {
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
	return id, reg, trk, srv
}

// runApp starts the GUI: menu-bar tray icon + window. Left-click toggles the
// window; right-click shows the menu. Transfers keep running when hidden.
func runApp(port int) {
	id, reg, _, srv := initCore(port)
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

	core.StartServer(srv)

	ctx := context.Background()
	core.StartNetworkWatcher(ctx, id, reg)
	core.StartKeepalive(ctx, reg, id)
	core.StartLANScan(ctx, id, reg) // find peers that can't use mDNS (e.g. Windows)

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "swiftdrop",
		Title:            "SwiftDrop",
		Width:            400,
		Height:           650,
		MinWidth:         340,
		MinHeight:        450,
		Hidden:           true,
		EnableFileDrop:   true,
		URL:              "/",
		BackgroundColour: application.NewRGB(15, 17, 21), // matches --bg: #0f1115
		Mac: application.MacWindow{
			CollectionBehavior: application.MacWindowCollectionBehaviorMoveToActiveSpace,
		},
	})

	// Native file picker via Wails dialog.
	srv.Pick = func() ([]string, error) {
		d := app.Dialog.OpenFile()
		d.CanChooseFiles(true)
		d.CanChooseDirectories(true)
		d.SetTitle("Choose files or folders to send")
		paths, err := d.PromptForMultipleSelection()
		return paths, err
	}

	// Hide-to-tray: closing the window hides it; the app keeps running.
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

	// Native macOS Accept/Reject dialog. ConsentDialog blocks until the user
	// responds, then feeds tr.Decision so the server handler unblocks.
	srv.ConsentHook = func(tr *core.Transfer, from, name string, size int64) {
		sizeStr := core.HumanSize(size)
		title := fmt.Sprintf("%s wants to send you a file", from)
		body := fmt.Sprintf("%s (%s)", name, sizeStr)
		accepted := core.ConsentDialog(title, body)
		select {
		case tr.Decision <- accepted:
		default:
		}
	}

	tray := app.SystemTray.New()
	tray.SetTemplateIcon(core.TrayIcon())
	tray.SetTooltip("SwiftDrop")

	// showOnActiveScreen moves the window to whichever monitor the mouse
	// cursor is on (i.e. where the user clicked the tray icon).
	showOnActiveScreen := func() {
		mx, _ := mouseLocation()
		screens := app.Screen.GetAll()
		for _, s := range screens {
			b := s.Bounds
			if int(mx) >= b.X && int(mx) < b.X+b.Width {
				window.SetScreen(s)
				break
			}
		}
		window.Show()
		window.Focus()
	}

	// Left-click toggles the window; right-click shows the menu.
	tray.OnClick(func() {
		if window.IsVisible() {
			window.Hide()
		} else {
			showOnActiveScreen()
		}
	})

	menu := app.NewMenu()
	menu.Add("Open SwiftDrop").OnClick(func(*application.Context) {
		showOnActiveScreen()
	})
	menu.AddSeparator()
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
	id, reg, _, srv := initCore(port)
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
