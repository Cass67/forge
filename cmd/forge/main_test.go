package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forge/internal/config"
	"forge/internal/mcp"
)

func TestStartsWithFlag(t *testing.T) {
	if !startsWithFlag("--model") {
		t.Fatal("expected leading flag to be treated as default chat args")
	}
	if startsWithFlag("make") {
		t.Fatal("expected subcommand name not to be treated as a flag")
	}
}

func TestRunMakeWithoutArgsUsesInteractiveMode(t *testing.T) {
	called := false
	prev := runMakeInteractiveFn
	runMakeInteractiveFn = func() {
		called = true
	}
	defer func() { runMakeInteractiveFn = prev }()

	runMake(nil)

	if !called {
		t.Fatal("expected forge make with no args to launch the legacy interactive pipeline")
	}
}

func TestRunMakeWithArgsUsesBatchMode(t *testing.T) {
	var gotCommand string
	var gotArgs []string
	prev := runImproveArgsFn
	runImproveArgsFn = func(commandName string, args []string) {
		gotCommand = commandName
		gotArgs = append([]string(nil), args...)
	}
	defer func() { runImproveArgsFn = prev }()

	runMake([]string{"./repo", "--prompt", "build app"})

	if gotCommand != "make" {
		t.Fatalf("runMake() command = %q, want make", gotCommand)
	}
	if len(gotArgs) != 3 || gotArgs[0] != "./repo" || gotArgs[1] != "--prompt" || gotArgs[2] != "build app" {
		t.Fatalf("runMake() args = %#v", gotArgs)
	}
}

func TestPrintHelpPromotesMakeAndKeepsImproveAlias(t *testing.T) {
	output := captureStdout(t, printHelp)
	if !strings.Contains(output, "forge                           Start interactive chat session") {
		t.Fatalf("expected bare forge help entry, got:\n%s", output)
	}
	if strings.Contains(output, "forge chat [flags]") {
		t.Fatalf("expected explicit chat alias to be removed from help, got:\n%s", output)
	}
	if !strings.Contains(output, "forge make                      Launch the legacy writer/auditor pipeline UI") {
		t.Fatalf("expected forge make help entry, got:\n%s", output)
	}
	if !strings.Contains(output, "forge improve <path> [flags]    Compatibility alias for forge make <path> [flags]") {
		t.Fatalf("expected forge improve alias help entry, got:\n%s", output)
	}
	if !strings.Contains(output, "forge mcp [list|get|add|remove|login|logout]") {
		t.Fatalf("expected forge mcp help entry, got:\n%s", output)
	}
	if !strings.Contains(output, "forge plugin install <source>") {
		t.Fatalf("expected forge plugin help entry, got:\n%s", output)
	}
}

func TestPrintChatHelpMentionsAdvancedDebugView(t *testing.T) {
	output := captureStdout(t, printHelp)
	if strings.Contains(output, "--live") {
		t.Fatalf("expected --live to be removed from chat help, got:\n%s", output)
	}
	if !strings.Contains(output, "-d") || !strings.Contains(output, "advanced debug view") {
		t.Fatalf("expected chat help to mention -d advanced debug view, got:\n%s", output)
	}
}

func TestShouldUseChatConsoleForPipedInput(t *testing.T) {
	t.Setenv("FORGE_CHAT_CONSOLE", "")
	if !shouldUseChatConsole(false) {
		t.Fatal("expected non-terminal stdin to use console chat")
	}
}

func TestShouldUseChatConsoleCanBeForced(t *testing.T) {
	t.Setenv("FORGE_CHAT_CONSOLE", "1")
	if !shouldUseChatConsole(true) {
		t.Fatal("expected FORGE_CHAT_CONSOLE=1 to force console chat")
	}
}

func TestShouldUseChatConsoleKeepsTerminalLiveByDefault(t *testing.T) {
	t.Setenv("FORGE_CHAT_CONSOLE", "")
	if shouldUseChatConsole(true) {
		t.Fatal("expected terminal stdin to keep live chat")
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	prev := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = prev }()

	fn()

	_ = w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("io.Copy: %v", err)
	}
	_ = r.Close()
	return buf.String()
}

func TestRunMCPAddListGetAndRemove(t *testing.T) {
	oldLoad := loadMainConfigFn
	oldSave := saveMainConfigFn
	oldPath := mainConfigPathFn
	defer func() {
		loadMainConfigFn = oldLoad
		saveMainConfigFn = oldSave
		mainConfigPathFn = oldPath
	}()

	cfg := &config.Config{}
	var savedPath string
	loadMainConfigFn = func() (*config.Config, error) {
		return cfg, nil
	}
	saveMainConfigFn = func(path string, in *config.Config) error {
		savedPath = path
		cfg = in
		return nil
	}
	mainConfigPathFn = func() string {
		return filepath.Join(t.TempDir(), "config.toml")
	}

	addOutput := captureStdout(t, func() {
		runMCPAdd([]string{"context7", "--url", "https://mcp.context7.com/mcp", "--timeout-ms", "5000"})
	})
	if !strings.Contains(addOutput, "Added MCP server context7.") {
		t.Fatalf("addOutput = %q", addOutput)
	}
	server, ok := cfg.MCPServers["context7"]
	if !ok {
		t.Fatal("expected MCP server to be saved")
	}
	if server.Type != "remote" || server.URL != "https://mcp.context7.com/mcp" || server.TimeoutMS != 5000 {
		t.Fatalf("saved server = %#v", server)
	}
	if savedPath == "" {
		t.Fatal("expected config path to be used")
	}

	listOutput := captureStdout(t, runMCPList)
	if !strings.Contains(listOutput, "context7") || !strings.Contains(listOutput, "remote") {
		t.Fatalf("listOutput = %q", listOutput)
	}

	getOutput := captureStdout(t, func() {
		runMCPGet("context7")
	})
	if !strings.Contains(getOutput, "name = context7") || !strings.Contains(getOutput, "url = https://mcp.context7.com/mcp") {
		t.Fatalf("getOutput = %q", getOutput)
	}

	removeOutput := captureStdout(t, func() {
		runMCPRemove("context7")
	})
	if !strings.Contains(removeOutput, "Removed MCP server context7.") {
		t.Fatalf("removeOutput = %q", removeOutput)
	}
	if _, ok := cfg.MCPServers["context7"]; ok {
		t.Fatal("expected MCP server to be removed")
	}
}

func TestRunPluginInstallLocalOpenCodePlugin(t *testing.T) {
	oldLoad := loadMainConfigFn
	oldSave := saveMainConfigFn
	oldPath := mainConfigPathFn
	oldInstall := runPluginInstallCmdFn
	defer func() {
		loadMainConfigFn = oldLoad
		saveMainConfigFn = oldSave
		mainConfigPathFn = oldPath
		runPluginInstallCmdFn = oldInstall
	}()

	tmp := t.TempDir()
	pluginPath := filepath.Join(tmp, "plugin.mjs")
	if err := os.WriteFile(pluginPath, []byte("export default { server: async () => ({ tool: {} }) }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	loadMainConfigFn = func() (*config.Config, error) { return cfg, nil }
	saveMainConfigFn = func(_ string, in *config.Config) error {
		cfg = in
		return nil
	}
	mainConfigPathFn = func() string { return filepath.Join(tmp, "forge", "config.toml") }
	runPluginInstallCmdFn = func(name string, args ...string) error {
		t.Fatalf("local plugin install should not run %s %#v", name, args)
		return nil
	}

	output := captureStdout(t, func() {
		runPlugin([]string{"install", "--id", "simple", "--auto-approve", "echo", pluginPath})
	})
	if !strings.Contains(output, "Installed OpenCode plugin simple") {
		t.Fatalf("install output = %q", output)
	}
	if len(cfg.Plugins) != 1 {
		t.Fatalf("plugins = %d, want 1", len(cfg.Plugins))
	}
	plugin := cfg.Plugins[0]
	if plugin.ID != "simple" || plugin.Kind != "opencode" || plugin.Source != pluginPath {
		t.Fatalf("plugin config = %#v", plugin)
	}
	if len(plugin.Command) < 4 || (plugin.Command[0] != "node" && plugin.Command[0] != "bun") || !strings.Contains(plugin.Command[1], "opencode-host.mjs") || plugin.Command[2] != "--module" || plugin.Command[3] != pluginPath {
		t.Fatalf("plugin command = %#v", plugin.Command)
	}
	if got := strings.Join(plugin.AutoApproveTools, ","); got != "echo" {
		t.Fatalf("auto approve = %q", got)
	}
	if _, err := os.Stat(plugin.Command[1]); err != nil {
		t.Fatalf("expected host script to be written: %v", err)
	}

	listOutput := captureStdout(t, runPluginList)
	if !strings.Contains(listOutput, "simple") || !strings.Contains(listOutput, "opencode") {
		t.Fatalf("plugin list = %q", listOutput)
	}
}

func TestRunPluginInstallPackageRunsNPM(t *testing.T) {
	oldLoad := loadMainConfigFn
	oldSave := saveMainConfigFn
	oldPath := mainConfigPathFn
	oldInstall := runPluginInstallCmdFn
	defer func() {
		loadMainConfigFn = oldLoad
		saveMainConfigFn = oldSave
		mainConfigPathFn = oldPath
		runPluginInstallCmdFn = oldInstall
	}()

	tmp := t.TempDir()
	cfg := &config.Config{}
	loadMainConfigFn = func() (*config.Config, error) { return cfg, nil }
	saveMainConfigFn = func(_ string, in *config.Config) error {
		cfg = in
		return nil
	}
	mainConfigPathFn = func() string { return filepath.Join(tmp, "forge", "config.toml") }
	var ran string
	runPluginInstallCmdFn = func(name string, args ...string) error {
		ran = name + " " + strings.Join(args, " ")
		return nil
	}

	runPlugin([]string{"install", "oh-my-openagent"})
	if !strings.Contains(ran, "npm install") || !strings.Contains(ran, "oh-my-openagent") {
		t.Fatalf("install command = %q", ran)
	}
	if len(cfg.Plugins) != 1 {
		t.Fatalf("plugins = %d, want 1", len(cfg.Plugins))
	}
	plugin := cfg.Plugins[0]
	if plugin.ID != "oh-my-openagent" || plugin.Kind != "opencode" {
		t.Fatalf("plugin = %#v", plugin)
	}
	if got := strings.Join(plugin.Command, " "); !strings.Contains(got, "--module oh-my-openagent") || !strings.Contains(got, "--install-dir") {
		t.Fatalf("command = %q", got)
	}
}

func TestRunMCPAddStdioCommand(t *testing.T) {
	oldLoad := loadMainConfigFn
	oldSave := saveMainConfigFn
	oldPath := mainConfigPathFn
	defer func() {
		loadMainConfigFn = oldLoad
		saveMainConfigFn = oldSave
		mainConfigPathFn = oldPath
	}()

	cfg := &config.Config{}
	loadMainConfigFn = func() (*config.Config, error) {
		return cfg, nil
	}
	saveMainConfigFn = func(path string, in *config.Config) error {
		cfg = in
		return nil
	}
	mainConfigPathFn = func() string { return filepath.Join(t.TempDir(), "config.toml") }

	captureStdout(t, func() {
		runMCPAdd([]string{"filesystem", "npx", "-y", "@modelcontextprotocol/server-filesystem", "/tmp"})
	})
	server := cfg.MCPServers["filesystem"]
	if server.Type != "stdio" {
		t.Fatalf("server.Type = %q", server.Type)
	}
	if len(server.Command) != 4 || server.Command[0] != "npx" {
		t.Fatalf("server.Command = %#v", server.Command)
	}
}

func TestRunMCPAddUsesContext7Preset(t *testing.T) {
	oldLoad := loadMainConfigFn
	oldSave := saveMainConfigFn
	oldPath := mainConfigPathFn
	defer func() {
		loadMainConfigFn = oldLoad
		saveMainConfigFn = oldSave
		mainConfigPathFn = oldPath
	}()

	cfg := &config.Config{}
	loadMainConfigFn = func() (*config.Config, error) {
		return cfg, nil
	}
	saveMainConfigFn = func(path string, in *config.Config) error {
		cfg = in
		return nil
	}
	mainConfigPathFn = func() string { return filepath.Join(t.TempDir(), "config.toml") }

	captureStdout(t, func() {
		runMCPAdd([]string{"context7"})
	})
	server := cfg.MCPServers["context7"]
	if server.Type != "remote" || server.URL != "https://mcp.context7.com/mcp" {
		t.Fatalf("server = %#v", server)
	}
}

func TestRunMCPLoginAndLogout(t *testing.T) {
	oldLoad := loadMainConfigFn
	oldSave := saveMainConfigFn
	oldPath := mainConfigPathFn
	oldPrompt := promptMCPTokenFn
	oldXDG := os.Getenv("XDG_CONFIG_HOME")
	defer func() {
		loadMainConfigFn = oldLoad
		saveMainConfigFn = oldSave
		mainConfigPathFn = oldPath
		promptMCPTokenFn = oldPrompt
		_ = os.Setenv("XDG_CONFIG_HOME", oldXDG)
	}()

	tmp := t.TempDir()
	if err := os.Setenv("XDG_CONFIG_HOME", tmp); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		MCPServers: map[string]config.MCPServerConfig{
			"context7": {Type: "remote", URL: "https://mcp.context7.com/mcp"},
		},
	}
	loadMainConfigFn = func() (*config.Config, error) {
		return cfg, nil
	}
	saveMainConfigFn = func(path string, in *config.Config) error {
		cfg = in
		return nil
	}
	mainConfigPathFn = func() string { return filepath.Join(tmp, "forge", "config.toml") }
	promptMCPTokenFn = func() (string, error) { return "secret-token", nil }

	loginOutput := captureStdout(t, func() {
		runMCPLogin("context7")
	})
	if !strings.Contains(loginOutput, "Stored MCP token for context7.") {
		t.Fatalf("loginOutput = %q", loginOutput)
	}
	if token, ok, err := mcp.BearerToken("context7"); err != nil || !ok || token != "secret-token" {
		t.Fatalf("BearerToken() = (%q, %t, %v)", token, ok, err)
	}

	getOutput := captureStdout(t, func() {
		runMCPGet("context7")
	})
	if !strings.Contains(getOutput, "auth = bearer_token") {
		t.Fatalf("getOutput = %q", getOutput)
	}

	logoutOutput := captureStdout(t, func() {
		runMCPLogout("context7")
	})
	if !strings.Contains(logoutOutput, "Cleared MCP token for context7.") {
		t.Fatalf("logoutOutput = %q", logoutOutput)
	}
	if _, ok, err := mcp.BearerToken("context7"); err != nil || ok {
		t.Fatalf("BearerToken() after logout = (%t, %v)", ok, err)
	}
}
