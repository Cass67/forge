package gui

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"

	"forge/internal/llm"
	"forge/internal/tui"
	forgeweb "forge/web"
)

// RunChatLiveWeb renders the chat event stream in a local web app. It has the
// same contract as tui.RunChatLiveBubbleTea: it blocks until the process is
// interrupted, feeding events to connected browsers over WebSocket.
func RunChatLiveWeb(events <-chan llm.Event, cfg tui.ChatLiveConfig, inputCh chan<- string, doneCh <-chan struct{}) tui.ChatLiveResult {
	s := newServer(cfg, inputCh)

	dist, err := fs.Sub(forgeweb.Dist, "dist")
	if err != nil {
		log.Printf("gui: embedded web assets unavailable: %v", err)
	}
	mux := http.NewServeMux()
	if dist != nil {
		mux.Handle("/", http.FileServer(http.FS(dist)))
	}
	mux.HandleFunc("/ws", s.handleWS)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Printf("gui: listen: %v", err)
		return tui.ChatLiveResult{}
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()

	url := fmt.Sprintf("http://%s", ln.Addr())
	fmt.Printf("forge gui: %s\n", url)
	_ = openBrowser(url)

	go s.pumpEvents(events)
	if cfg.ApprovalCh != nil {
		go s.pumpApprovals(cfg.ApprovalCh)
	}
	go func() {
		for range doneCh {
			s.send(doneFrame{Type: "done"})
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	<-ctx.Done()
	_ = srv.Close()
	return tui.ChatLiveResult{}
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
