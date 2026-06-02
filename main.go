package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"

	core "swiftdrop-core"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed web
var webFS embed.FS

func main() {
	headless := flag.Bool("headless", false, "run without UI (server + discovery only)")
	port := flag.Int("port", core.DefaultPort, "LAN port")
	flag.Parse()

	if *headless {
		runHeadless(*port)
	} else {
		runApp(*port)
	}
}

// runApp launches the full-window SwiftDrop application with a system tray
// icon. Clicking the tray icon shows/hides the main window.
func runApp(port int) {
	id := core.LoadOrCreateIdentity(port)
	reg := core.NewPeerRegistry()
	reg.LoadKnown()
	reg.LoadManual()
	trk := core.NewTracker()
	core.InitPairStore()

	// Best-effort Windows Firewall rules so mDNS and transfers work.
	ensureFirewall(id.Port)

	srv := core.NewServer(id, reg, trk)
	srv.WebFS = webFS
	core.StartServer(srv)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	core.StartNetworkWatcher(ctx, id, reg)
	core.StartKeepalive(ctx, reg, id)

	app := application.New(application.Options{
		Name:        "SwiftDrop",
		Description: "Fast LAN file transfers",
		// Serve the UI + API through the same Go mux the peers use.
		Assets: application.AssetOptions{Handler: srv.Handler()},
	})

	srv.OnQuit = func() {
		cancel()
		app.Quit()
	}

	// Create the main window — a standard resizable window (not a popover).
	// On Windows this is a normal windowed app, not a frameless drawer.
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:           "swiftdrop",
		Title:          "SwiftDrop",
		Width:          400,
		Height:         650,
		MinWidth:       340,
		MinHeight:      450,
		Hidden:         false,
		EnableFileDrop: true,
		URL:            "/",
	})

	// Native file picker via Wails dialog.
	srv.Pick = func() ([]string, error) {
		d := app.Dialog.OpenFile()
		d.CanChooseFiles(true)
		d.CanChooseDirectories(false)
		d.SetTitle("Choose files to send")
		paths, err := d.PromptForMultipleSelection()
		return paths, err
	}

	// Keep the window alive when "closed" — just hide it to the tray.
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		window.Hide()
		e.Cancel()
	})

	// Native drag-and-drop hands us real file paths → push them into the
	// UI's staging queue so Go can stream directly at full speed.
	window.RegisterHook(events.Common.WindowFilesDropped, func(e *application.WindowEvent) {
		paths := e.Context().DroppedFiles()
		if len(paths) == 0 {
			return
		}
		infos := core.FileInfos(paths)
		data, _ := json.Marshal(infos)
		window.ExecJS(fmt.Sprintf("window.swiftdropOnDrop && window.swiftdropOnDrop(%s)", string(data)))
	})

	// System tray — left-click toggles the window, right-click shows menu.
	tray := app.SystemTray.New()
	tray.SetIcon(core.TrayIconColored()) // colored icon for Windows system tray
	tray.SetTooltip("SwiftDrop")

	tray.OnClick(func() {
		if window.IsVisible() {
			window.Hide()
		} else {
			window.Show()
			window.Focus()
		}
	})

	menu := app.NewMenu()
	menu.Add("Open SwiftDrop").OnClick(func(*application.Context) {
		window.Show()
		window.Focus()
	})
	menu.Add("Quit SwiftDrop").OnClick(func(*application.Context) {
		cancel()
		app.Quit()
	})
	tray.SetMenu(menu)
	tray.OnRightClick(func() { tray.OpenMenu() })

	log.Printf("SwiftDrop for Windows starting (port %d)", id.Port)

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// ensureFirewall adds Windows Firewall inbound rules for the SwiftDrop app
// port (TCP) and mDNS (UDP 5353). Fails silently if not running as admin —
// the user will need to accept the Windows Firewall prompt on first launch
// or run as admin once.
func ensureFirewall(port int) {
	portStr := strconv.Itoa(port)
	// SwiftDrop TCP — required for file transfers and API.
	exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		"name=SwiftDrop TCP In",
		"dir=in", "action=allow", "protocol=TCP",
		"localport="+portStr,
		"profile=private",
	).Run()
	// mDNS UDP — required for automatic device discovery.
	exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		"name=SwiftDrop mDNS In",
		"dir=in", "action=allow", "protocol=UDP",
		"localport=5353",
		"profile=private",
	).Run()
	log.Printf("firewall: rules requested for TCP/%s and UDP/5353", portStr)
}

// runHeadless runs the core server and discovery without any UI — useful for
// CI testing and headless environments.
func runHeadless(port int) {
	id := core.LoadOrCreateIdentity(port)
	reg := core.NewPeerRegistry()
	reg.LoadKnown()
	reg.LoadManual()
	trk := core.NewTracker()
	core.InitPairStore()

	srv := core.NewServer(id, reg, trk)

	// Start the HTTP server manually (no Wails needed in headless mode).
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", id.Port))
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	core.StartNetworkWatcher(ctx, id, reg)
	core.StartKeepalive(ctx, reg, id)

	log.Printf("SwiftDrop headless on :%d  id=%s", id.Port, id.ID)
	go func() {
		if err := http.Serve(ln, srv.Handler()); err != nil && err != http.ErrServerClosed {
			log.Printf("serve: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	cancel()
	log.Println("shutdown")
}
