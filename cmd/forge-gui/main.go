// Command forge-gui runs the forge chat runtime in a native desktop window.
//
// The window is the operating system's own webview (WKWebView on macOS,
// WebView2 on Windows, WebKitGTK on Linux) driven by Wails, so the binary
// carries the UI without bundling a browser engine. The frontend calls the
// bound gui.Service by method name and receives streamed output as
// application events; nothing listens on a network port.
package main

import (
	_ "embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"forge/internal/bootstrap"
	"forge/internal/gui"
	"forge/internal/llm"
	runtimepkg "forge/internal/runtime"
	"forge/internal/tui"
	forgeweb "forge/web"
)

// The window and about-box icon. macOS takes its Dock icon from the .app
// bundle instead (see build/macapp.sh); this covers Linux and Windows.
//
//go:embed assets/icon.png
var appIcon []byte

func main() {
	if err := run(); err != nil {
		log.Fatalf("forge-gui: %v", err)
	}
}

func run() error {
	fs_ := flag.NewFlagSet("forge-gui", flag.ExitOnError)
	yolo := fs_.Bool("yolo", false, "skip all approval prompts")
	model := fs_.String("model", "", "model override")
	workDir := fs_.String("C", "", "workspace directory (default: cwd)")
	resume := fs_.String("resume", "", "resume a stored thread by id")
	continueLast := fs_.Bool("continue", false, "resume the most recent thread")
	if err := fs_.Parse(os.Args[1:]); err != nil {
		return err
	}

	cfg, err := bootstrap.LoadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if os.Getenv("FORGE_CHAT_YOLO") == "1" || cfg.Chat.Yolo {
		*yolo = true
	}

	setup, err := runtimepkg.BuildChatSetup(cfg, nil, *model, *workDir, *yolo)
	if err != nil {
		return err
	}
	if setup == nil {
		return nil
	}
	if *resume != "" || *continueLast {
		threadID, err := runtimepkg.ResolveResumeThreadID(setup.Config, *resume, *continueLast)
		if err != nil {
			return err
		}
		setup.ResumeThreadID = threadID
	}

	assets, err := fs.Sub(forgeweb.Dist, "dist")
	if err != nil {
		return fmt.Errorf("embedded frontend: %w", err)
	}

	// The app pointer is captured by the emit closure, which the service uses
	// before Run starts; events emitted that early are simply dropped.
	var app *application.App
	service, controller := gui.New(func(name string, data any) {
		if app != nil {
			app.Event.Emit(name, data)
		}
	})

	app = application.New(application.Options{
		Name:        "Forge",
		Description: "Coding agent",
		Icon:        appIcon,
		Services:    []application.Service{application.NewService(service)},
		Assets:      application.AssetOptions{Handler: http.FileServer(http.FS(assets))},
	})

	service.PickDir = func() (string, error) {
		return app.Dialog.OpenFile().
			SetTitle("Choose a workspace folder").
			CanChooseDirectories(true).
			CanChooseFiles(false).
			PromptForSingleSelection()
	}
	service.OpenWorkspace = openWorkspace

	newWindow(app, setup.WorkDir)

	// The chat runtime owns a blocking loop, so it runs off the main thread;
	// the platform requires the window to stay on it.
	go func() {
		setup.LiveRunner = func(events <-chan llm.Event, live tui.ChatLiveConfig, inputCh chan<- string, doneCh <-chan struct{}) tui.ChatLiveResult {
			controller.Attach(live, inputCh)
			if live.ApprovalCh != nil {
				go controller.PumpApprovals(live.ApprovalCh)
			}
			go controller.PumpDone(doneCh)
			controller.PumpEvents(events)
			return tui.ChatLiveResult{}
		}
		runtimepkg.RunChatLive(setup)
	}()

	return app.Run()
}

func newWindow(app *application.App, workDir string) {
	title := "Forge"
	if dir := strings.TrimSpace(workDir); dir != "" {
		title = "Forge — " + dir
	}
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     title,
		Width:     1280,
		Height:    860,
		MinWidth:  900,
		MinHeight: 600,
	})
}

// openWorkspace starts a second forge-gui rooted at dir. Each workspace gets
// its own process because the chat runtime's tools, sandbox rules and thread
// store are all bound to the directory it started in.
func openWorkspace(dir string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "-C", dir)
	cmd.Dir = dir
	return cmd.Start()
}
