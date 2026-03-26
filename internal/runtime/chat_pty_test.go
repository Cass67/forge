package runtime

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"forge/internal/config"
	"forge/internal/llm"

	"github.com/creack/pty"
)

const ptyResponseSentinel = "FORGE_TEST_RESPONSE_DONE"

type ptyTestDriver struct{}

func (d *ptyTestDriver) Name() string { return "pty-test" }

func (d *ptyTestDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	out <- llm.Token{Text: ptyResponseSentinel}
	return nil
}

type ptyCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *ptyCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *ptyCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func TestChatPTYHelperProcess(t *testing.T) {
	if os.Getenv("FORGE_PTY_HELPER") != "1" {
		return
	}

	workDir, err := os.MkdirTemp("", "forge-pty-*")
	if err != nil {
		t.Fatalf("os.MkdirTemp: %v", err)
	}
	cfg := &config.Config{}
	cfg.Chat.MaxTurns = 4

	RunChatLive(&ChatSetup{
		Config:    cfg,
		ChatModel: "test-model",
		WorkDir:   workDir,
		Driver:    &ptyTestDriver{},
	})
	os.Exit(0)
}

func TestDefaultChatDoesNotEnterAltScreen(t *testing.T) {
	cmd, ptmx, capture := startChatPTYHelper(t)
	defer func() {
		_ = ptmx.Close()
	}()

	waitForPTYOutput(t, capture, "Type a message or /help", 10*time.Second)
	const prompt = "hello forge"
	if _, err := ptmx.Write([]byte(prompt + "\r")); err != nil {
		t.Fatalf("ptmx.Write: %v", err)
	}
	output := waitForPTYOutput(t, capture, ptyResponseSentinel, 10*time.Second)
	if strings.Contains(output, "\x1b[?1049h") {
		t.Fatalf("default chat entered alt screen: %q", output)
	}
	if !strings.Contains(output, prompt) {
		t.Fatalf("expected echoed user prompt in output:\n%s", output)
	}
	if userIdx, respIdx := strings.Index(output, prompt), strings.Index(output, ptyResponseSentinel); userIdx < 0 || respIdx < userIdx {
		t.Fatalf("expected durable prompt before response in output:\n%s", output)
	}

	if _, err := ptmx.Write([]byte("/quit\r")); err != nil {
		t.Fatalf("ptmx.Write quit: %v", err)
	}
	waitForPTYExit(t, cmd, 10*time.Second)
}

func TestDefaultChatDoesNotEnterAltScreenDuringBracketedPaste(t *testing.T) {
	cmd, ptmx, capture := startChatPTYHelper(t)
	defer func() {
		_ = ptmx.Close()
	}()

	waitForPTYOutput(t, capture, "Type a message or /help", 10*time.Second)
	paste := "\x1b[200~pasted first line\npasted second line\x1b[201~"
	if _, err := ptmx.Write([]byte(paste)); err != nil {
		t.Fatalf("ptmx.Write paste: %v", err)
	}
	if output, ok := waitForPTYOutputMaybe(capture, ptyResponseSentinel, 500*time.Millisecond); ok {
		t.Fatalf("expected bracketed paste to avoid submission before Enter, got:\n%s", output)
	}

	if _, err := ptmx.Write([]byte("\r")); err != nil {
		t.Fatalf("ptmx.Write enter: %v", err)
	}
	output := waitForPTYOutput(t, capture, ptyResponseSentinel, 10*time.Second)
	if strings.Contains(output, "\x1b[?1049h") {
		t.Fatalf("default chat entered alt screen during bracketed paste: %q", output)
	}
	if !strings.Contains(output, "pasted first line") {
		t.Fatalf("expected pasted text to remain visible, got:\n%s", output)
	}

	if _, err := ptmx.Write([]byte("/quit\r")); err != nil {
		t.Fatalf("ptmx.Write quit: %v", err)
	}
	waitForPTYExit(t, cmd, 10*time.Second)
}

func startChatPTYHelper(t *testing.T) (*exec.Cmd, *os.File, *ptyCapture) {
	t.Helper()

	cmd := exec.Command(os.Args[0], "-test.run=TestChatPTYHelperProcess")
	cmd.Env = append(os.Environ(),
		"FORGE_PTY_HELPER=1",
		"TERM=xterm-256color",
	)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 120, Rows: 32})
	if err != nil {
		t.Fatalf("pty.StartWithSize: %v", err)
	}
	capture := &ptyCapture{}
	go func() {
		_, _ = io.Copy(capture, ptmx)
	}()
	return cmd, ptmx, capture
}

func waitForPTYOutput(t *testing.T, capture *ptyCapture, needle string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		output := capture.String()
		if strings.Contains(output, needle) {
			return output
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q in PTY output:\n%s", needle, capture.String())
	return ""
}

func waitForPTYOutputMaybe(capture *ptyCapture, needle string, timeout time.Duration) (string, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		output := capture.String()
		if strings.Contains(output, needle) {
			return output, true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return capture.String(), false
}

func waitForPTYExit(t *testing.T, cmd *exec.Cmd, timeout time.Duration) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("helper process exited with error: %v", err)
		}
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		t.Fatal("timed out waiting for PTY helper process to exit")
	}
}
