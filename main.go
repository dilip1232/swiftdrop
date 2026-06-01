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

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// defaultPort is the LAN port SwiftDrop serves on for peer-to-peer transfers.
// Fixed so peers always know where to reach a device.
const defaultPort = 53317

var (
	identity  Identity
	registry  *peerRegistry
	transfers *tracker

	flagPort     = flag.Int("port", defaultPort, "port to serve on")
	flagName     = flag.String("name", "", "device name (defaults to hostname)")
	flagID       = flag.String("id", "", "override device id (for running multiple instances on one host)")
	flagHeadless = flag.Bool("headless", false, "run server + discovery without the UI (for testing)")
)

func main() {
	flag.Parse()

	registry = newPeerRegistry()
	registry.loadKnown()
	registry.loadManual()
	transfers = newTracker()
	initPairStore()
	identity = loadOrCreateIdentity(*flagPort)
	if *flagName != "" {
		identity.Name = *flagName
	}
	if *flagID != "" {
		identity.ID = *flagID
	}

	if *flagHeadless {
		runHeadless()
		return
	}
	runApp()
}

// startServer launches the peer-facing HTTP server (inbox + api) on the LAN
// port. If the preferred port is taken it tries up to 10 nearby ports so the
// app still starts instead of crashing.
func startServer(srv *Server) {
	var ln net.Listener
	var err error
	for offset := 0; offset < 10; offset++ {
		port := identity.Port + offset
		ln, err = net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err == nil {
			if offset > 0 {
				log.Printf("port %d busy; using %d instead", identity.Port, port)
				identity.Port = port
			}
			break
		}
	}
	if err != nil {
		log.Fatalf("listen :%d (tried 10 ports): %v", identity.Port, err)
	}
	go func() {
		log.Printf("SwiftDrop %q listening on :%d", identity.Name, identity.Port)
		if err := http.Serve(ln, srv.handler()); err != nil && err != http.ErrServerClosed {
			log.Printf("serve: %v", err)
		}
	}()
}

// runApp is the normal mode: menu-bar icon + a frameless popover drawer that
// hosts the whole UI. Transfers run as goroutines in this process, so closing
// the drawer never interrupts them.
func runApp() {
	srv := newServer(identity, registry, transfers)

	app := application.New(application.Options{
		Name:        "SwiftDrop",
		Description: "Fast LAN file transfer",
		// Serve the existing UI + API through the same Go mux the peers use.
		Assets: application.AssetOptions{Handler: srv.handler()},
		Mac: application.MacOptions{
			// Menu-bar only: no Dock icon, no app-switcher entry.
			ActivationPolicy: application.ActivationPolicyAccessory,
		},
	})

	srv.onQuit = func() { app.Quit() }

	startServer(srv)

	startNetworkWatcher(context.Background(), identity, registry)
	startKeepalive(context.Background(), registry, identity)

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
	srv.pick = func() ([]string, error) {
		d := app.Dialog.OpenFile()
		d.CanChooseFiles(true)
		d.CanChooseDirectories(false)
		d.SetTitle("Choose files to send")
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
		infos := fileInfos(paths)
		data, _ := json.Marshal(infos)
		window.ExecJS(fmt.Sprintf("window.swiftdropOnDrop && window.swiftdropOnDrop(%s)", string(data)))
	})

	tray := app.SystemTray.New()
	tray.SetTemplateIcon(trayIcon())
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
func runHeadless() {
	srv := newServer(identity, registry, transfers)
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", identity.Port))
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	startNetworkWatcher(context.Background(), identity, registry)
	startKeepalive(context.Background(), registry, identity)
	log.Printf("SwiftDrop %q listening on :%d (headless)", identity.Name, identity.Port)
	if err := http.Serve(ln, srv.handler()); err != nil {
		log.Fatal(err)
	}
	os.Exit(0)
}
