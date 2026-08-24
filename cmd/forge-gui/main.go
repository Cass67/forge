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
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"forge/internal/bootstrap"
	"forge/internal/gui"
	"forge/internal/llm"
	runtimepkg "forge/internal/runtime"
	"forge/internal/shellenv"
	"forge/internal/tui"
	"forge/internal/workspace"
	forgeweb "forge/web"
	// Register native plugins (tools, slash commands, skills) in the GUI
	// binary, matching the CLI. Without this the sandbox plugin's /sandbox
	// command (and its tool/skill) never exist server-side.
	_ "forge/plugins"
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
	prompt := fs_.String("prompt", "", "send this message once the runtime is ready (used by multi-run)")
	if err := fs_.Parse(os.Args[1:]); err != nil {
		return err
	}

	// Launched from Finder/Dock the process inherits launchd's bare
	// environment; pull in the user's shell PATH and exported keys first so
	// config env references and MCP stdio servers resolve.
	shellenv.Hydrate()

	cfg, err := bootstrap.LoadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if os.Getenv("FORGE_CHAT_YOLO") == "1" || cfg.Chat.Yolo {
		*yolo = true
	}

	registry := workspace.LoadRegistry()
	startDir := startupWorkspace(*workDir, registry)
	_ = registry.Remember(startDir)
	enterWorkspace(startDir)

	setup, err := buildSetup(startDir, *model, *yolo)
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
	} else if threadID, err := runtimepkg.ResolveWorkspaceResumeThreadID(setup.Config, startDir); err != nil {
		return err
	} else {
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
		// Without this the process survives its last window on macOS, so the
		// app has to be quit from the Dock before it can be started again.
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		Icon:     appIcon,
		Services: []application.Service{application.NewService(service)},
		Assets:   application.AssetOptions{Handler: http.FileServer(http.FS(assets))},
	})

	service.Registry = registry
	service.PickDir = func() (string, error) {
		return app.Dialog.OpenFile().
			SetTitle("Choose a workspace folder").
			CanChooseDirectories(true).
			CanChooseFiles(false).
			PromptForSingleSelection()
	}

	win := newWindow(app, setup.WorkDir)

	// OS file drags arrive here rather than in the DOM.
	win.OnWindowEvent(events.Common.WindowFilesDropped, func(e *application.WindowEvent) {
		controller.FilesDropped(e.Context().DroppedFiles())
	})

	// A -prompt is delivered once, to the first runtime this process starts:
	// switching workspace later must not replay it.
	var sendPrompt sync.Once
	// started keys the runtimes this process launched by session id, so a
	// repeated activation of the same session never starts a second loop.
	started := make(map[string]bool)
	var startMu sync.Mutex

	// startRuntime builds and runs one chat session for dir in its own
	// goroutine and registers it with the service under sessionID. Sessions are
	// independent: several can be live in the same directory at once, and
	// starting or stopping one never touches another.
	startRuntime := func(dir, sessionID, resumeThreadID string) {
		startMu.Lock()
		if started[sessionID] {
			startMu.Unlock()
			return
		}
		started[sessionID] = true
		startMu.Unlock()
		controller.Starting(dir)

		go func() {
			setup, err := buildSetup(dir, *model, *yolo)
			if err != nil {
				log.Printf("forge-gui: cannot open %s: %v", dir, err)
				controller.ReportError(fmt.Sprintf("cannot open %s: %v", dir, err))
				startMu.Lock()
				delete(started, sessionID)
				startMu.Unlock()
				controller.Forget(sessionID)
				return
			}
			// A session opened on a specific thread resumes it; one opened as a
			// new conversation starts empty, whatever the workspace was last
			// left in.
			if strings.TrimSpace(resumeThreadID) != "" {
				setup.ResumeThreadID = resumeThreadID
			}

			setup.LiveRunner = func(events <-chan llm.Event, live tui.ChatLiveConfig, inputCh chan<- string, doneCh <-chan struct{}) tui.ChatLiveResult {
				// Tag events with the same cleaned key Attach registers under,
				// so the frontend's per-workspace matching never sees a
				// symlinked or uncleaned variant of the path.
				dir := filepath.Clean(strings.TrimSpace(live.WorkDir))
				stop := make(chan struct{})
				var stopOnce sync.Once
				controller.Attach(sessionID, live, inputCh, func() {
					stopOnce.Do(func() { close(stop) })
				})
				if strings.TrimSpace(*prompt) != "" {
					sendPrompt.Do(func() {
						go func() { inputCh <- *prompt }()
					})
				}
				if live.ApprovalCh != nil {
					go controller.PumpApprovals(sessionID, dir, live.ApprovalCh)
				}
				go controller.PumpDone(sessionID, dir, doneCh)
				streamed := make(chan struct{})
				go func() {
					controller.PumpEvents(sessionID, dir, events)
					close(streamed)
				}()
				// Returning ends the session and releases its MCP servers and
				// shells, so it waits for either the stream to end or the
				// window to close this session explicitly.
				select {
				case <-streamed:
				case <-stop:
				}
				return tui.ChatLiveResult{}
			}
			runtimepkg.RunChatLive(setup)
			startMu.Lock()
			delete(started, sessionID)
			startMu.Unlock()
			controller.Forget(sessionID)
		}()
	}

	service.StartRuntime = func(dir, sessionID, resumeThreadID string) {
		startRuntime(dir, sessionID, resumeThreadID)
		if wd, err := os.Getwd(); err != nil || wd != dir {
			enterWorkspace(dir)
		}
		win.SetTitle(windowTitle(dir))
		_ = registry.Remember(dir)
	}

	// The chat runtimes own blocking loops, so they run off the main thread;
	// the platform requires the window to stay on it.
	// The first session resumes whatever thread the workspace was last left in.
	startThread, err := runtimepkg.ResolveWorkspaceResumeThreadID(setup.Config, startDir)
	if err != nil {
		log.Printf("forge-gui: cannot resume %s: %v", startDir, err)
	}
	startRuntime(startDir, "s0", startThread)

	// Wails builds a signal handler and never starts it, and once AppKit owns
	// the process SIGINT stops reaching Go's default handler: Ctrl-C in the
	// launching terminal did nothing. Notify puts Go back in charge of the
	// signal; a second one exits the hard way if the first cannot finish.
	quitOnInterrupt(app)

	exit := app.Run()
	controller.Shutdown()
	return exit
}

func quitOnInterrupt(app *application.App) {
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		go app.Quit()
		<-sigCh
		os.Exit(1)
	}()
}

// startupWorkspace picks the directory to open on launch. An app bundle
// launched from Finder inherits "/" as its working directory, so the process
// cwd is only trusted when it is actually usable; otherwise the most recently
// opened workspace is reopened, falling back to the home directory.
func startupWorkspace(flagDir string, registry *workspace.Registry) string {
	if dir, err := workspace.Clean(flagDir); err == nil {
		return dir
	}
	if cwd, err := os.Getwd(); err == nil && workspace.Usable(cwd) {
		return cwd
	}
	if last := registry.MostRecent(); last != "" {
		return last
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return "."
}

// enterWorkspace makes the workspace the process working directory. Tools and
// any relative config paths resolve against it, and an app bundle otherwise
// starts at "/", where those resolve to unwritable root paths. A terminal
// launch gets this for free by virtue of where it was started.
func enterWorkspace(dir string) {
	if err := os.Chdir(dir); err != nil {
		log.Printf("forge-gui: cannot enter %s: %v", dir, err)
	}
}

// buildSetup constructs a chat runtime rooted at workDir. Config is reloaded
// each time so a switch picks up anything edited since startup.
func buildSetup(workDir, model string, yolo bool) (*runtimepkg.ChatSetup, error) {
	cfg, err := bootstrap.LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	if os.Getenv("FORGE_CHAT_YOLO") == "1" || cfg.Chat.Yolo {
		yolo = true
	}
	return runtimepkg.BuildChatSetup(cfg, nil, model, workDir, yolo)
}

func windowTitle(workDir string) string {
	if dir := strings.TrimSpace(workDir); dir != "" {
		return "Forge — " + dir
	}
	return "Forge"
}

func newWindow(app *application.App, workDir string) application.Window {
	title := windowTitle(workDir)
	return app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: title,
		// The webview's stock right-click menu offers Reload and Inspect
		// Element, which belong to a browser, not to this app. Set
		// FORGE_GUI_DEVTOOLS=1 to get them back when the UI itself misbehaves
		// (a blank window has no other way to report a frontend error).
		DefaultContextMenuDisabled: os.Getenv("FORGE_GUI_DEVTOOLS") != "1",
		// The webview swallows OS file drags: they never surface as DOM drop
		// events with files attached. Wails delivers them as a window event
		// instead, for elements marked data-file-drop-target.
		EnableFileDrop: true,
		Width:          1280,
		Height:         860,
		MinWidth:       900,
		MinHeight:      600,
	})
}
