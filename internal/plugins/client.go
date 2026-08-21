package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"forge/internal/config"
)

type client struct {
	id     string
	config config.PluginConfig
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	enc    *json.Encoder
	dec    *json.Decoder

	mu        sync.Mutex
	nextID    int64
	closeOnce sync.Once
}

func startClient(ctx context.Context, workDir string, cfg config.PluginConfig) (*client, initializeResult, error) {
	if len(cfg.Command) == 0 {
		return nil, initializeResult{}, fmt.Errorf("plugin %q requires a command", cfg.ID)
	}
	cmd := exec.Command(cfg.Command[0], cfg.Command[1:]...)
	cmd.Dir = workDir
	cmd.Env = pluginEnv(cfg)
	cmd.Stderr = io.Discard

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, initializeResult{}, fmt.Errorf("plugin %q stdin: %w", cfg.ID, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, initializeResult{}, fmt.Errorf("plugin %q stdout: %w", cfg.ID, err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, initializeResult{}, fmt.Errorf("plugin %q start: %w", cfg.ID, err)
	}

	plugin := &client{
		id:     strings.TrimSpace(cfg.ID),
		config: cfg,
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		enc:    json.NewEncoder(stdin),
		dec:    json.NewDecoder(stdout),
	}

	initCtx, cancel := context.WithTimeout(withParent(ctx), startupTimeout(cfg))
	defer cancel()
	var result initializeResult
	err = plugin.call(initCtx, "initialize", initializeParams{
		ProtocolVersion: protocolVersion,
		PluginID:        plugin.id,
		CWD:             workDir,
		Capabilities:    []string{"tools", "hooks"},
		ForgeTools:      []string{},
	}, &result)
	if err != nil {
		_ = plugin.Close()
		return nil, initializeResult{}, fmt.Errorf("plugin %q initialize: %w", cfg.ID, err)
	}
	return plugin, result, nil
}

func (c *client) call(ctx context.Context, method string, params any, result any) error {
	ctx = withParent(ctx)
	done := make(chan error, 1)
	go func() {
		done <- c.callSync(method, params, result)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = c.Close()
		return ctx.Err()
	}
}

func (c *client) callSync(method string, params any, result any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	id := c.nextID
	if err := c.enc.Encode(requestEnvelope{ID: id, Method: method, Params: params}); err != nil {
		return err
	}
	for {
		var response responseEnvelope
		if err := c.dec.Decode(&response); err != nil {
			return err
		}
		if response.ID != id {
			continue
		}
		if response.Error != nil {
			msg := strings.TrimSpace(response.Error.Message)
			if msg == "" {
				msg = "plugin returned an error"
			}
			return errors.New(msg)
		}
		if result == nil || len(response.Result) == 0 || string(response.Result) == "null" {
			return nil
		}
		return json.Unmarshal(response.Result, result)
	}
}

func (c *client) Close() error {
	if c == nil {
		return nil
	}
	var waitErr error
	c.closeOnce.Do(func() {
		if c.stdin != nil {
			_ = c.stdin.Close()
		}
		if c.stdout != nil {
			_ = c.stdout.Close()
		}
		if c.cmd != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
			waitErr = c.cmd.Wait()
		}
	})
	return ignoreExpectedProcessExit(waitErr)
}

func ignoreExpectedProcessExit(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "signal: killed") {
		return nil
	}
	if strings.Contains(err.Error(), "file already closed") {
		return nil
	}
	return nil
}

func pluginEnv(cfg config.PluginConfig) []string {
	env := make([]string, 0, 1+len(cfg.Env)+len(cfg.InheritEnv))
	if path := os.Getenv("PATH"); path != "" {
		env = append(env, "PATH="+path)
	}
	for key, value := range cfg.Env {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		env = append(env, key+"="+value)
	}
	for _, key := range cfg.InheritEnv {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func withParent(parent context.Context) context.Context {
	if parent == nil {
		return context.Background()
	}
	return parent
}

func withRequestTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return withParent(parent), func() {}
	}
	return context.WithTimeout(withParent(parent), timeout)
}
