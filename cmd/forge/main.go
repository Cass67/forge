package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"golang.org/x/term"

	"forge/internal/auth"
	"forge/internal/bootstrap"
	"forge/internal/cli"
	"forge/internal/config"
	"forge/internal/copilot"
	"forge/internal/fsutil"
	"forge/internal/mcp"
	pluginruntime "forge/internal/plugins"
	runtimepkg "forge/internal/runtime"
	"forge/internal/skills"
)

var (
	loadMainConfigFn      = bootstrap.LoadConfig
	saveMainConfigFn      = config.Save
	mainConfigPathFn      = config.DefaultPath
	promptMCPTokenFn      = promptMCPToken
	runPluginInstallCmdFn = runPluginInstallCommand
	osExit                = os.Exit
)

var mcpServerPresets = map[string]config.MCPServerConfig{
	"context7": {
		Type: "remote",
		URL:  "https://mcp.context7.com/mcp",
	},
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 || startsWithFlag(args[0]) {
		runChat(args)
		return
	}

	commands := map[string]cli.Command{
		"-h":        {Name: "help", Run: func(args []string) { runHelp(args) }},
		"--help":    {Name: "help", Run: func(args []string) { runHelp(args) }},
		"help":      {Name: "help", Run: func(args []string) { runHelp(args) }},
		"-v":        {Name: "version", Run: func(args []string) { fmt.Println("forge v0.1.0") }},
		"--version": {Name: "version", Run: func(args []string) { fmt.Println("forge v0.1.0") }},
		"version":   {Name: "version", Run: func(args []string) { fmt.Println("forge v0.1.0") }},
		"auth": {
			Name: "auth",
			Run: func(args []string) {
				if len(args) >= 1 && args[0] == "copilot" {
					runCopilotAuth()
					return
				}
				fmt.Fprintln(os.Stderr, "usage: forge auth copilot")
				os.Exit(1)
			},
		},
		"mcp": {Name: "mcp", Run: func(args []string) { runMCP(args) }},
		"plugin": {
			Name: "plugin",
			Run: func(args []string) {
				runPlugin(args)
			},
		},
		"plugins": {
			Name: "plugins",
			Run: func(args []string) {
				runPlugin(args)
			},
		},
		"skills": {Name: "skills", Run: func(args []string) { runSkills(args) }},
		"status": {Name: "status", Run: func(args []string) { runStatus() }},
	}
	if cmd, ok := commands[args[0]]; ok {
		cmd.Run(args[1:])
		return
	}

	fmt.Fprintf(os.Stderr, "error: unknown command %q\n\n", args[0])
	printHelp()
	os.Exit(1)
}

func startsWithFlag(arg string) bool {
	return strings.HasPrefix(arg, "-")
}

func runMCP(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: forge mcp [list|get|add|remove|login|logout]")
		os.Exit(1)
	}
	switch args[0] {
	case "list":
		runMCPList()
	case "get":
		runMCPGet(cli.RequireArg(args[1:], "usage: forge mcp get <name>"))
	case "add":
		runMCPAdd(args[1:])
	case "remove", "rm":
		runMCPRemove(cli.RequireArg(args[1:], "usage: forge mcp remove <name>"))
	case "login":
		runMCPLogin(cli.RequireArg(args[1:], "usage: forge mcp login <name>"))
	case "logout":
		runMCPLogout(cli.RequireArg(args[1:], "usage: forge mcp logout <name>"))
	default:
		fmt.Fprintln(os.Stderr, "usage: forge mcp [list|get|add|remove|login|logout]")
		os.Exit(1)
	}
}

func runPlugin(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: forge plugin [install|list|validate|remove]")
		os.Exit(1)
	}
	switch args[0] {
	case "install", "add":
		runPluginInstall(args[1:])
	case "list", "ls":
		runPluginList()
	case "validate":
		runPluginValidate(cli.RequireArg(args[1:], "usage: forge plugin validate <path>"))
	case "remove", "rm":
		runPluginRemove(cli.RequireArg(args[1:], "usage: forge plugin remove <id>"))
	default:
		fmt.Fprintln(os.Stderr, "usage: forge plugin [install|list|validate|remove]")
		os.Exit(1)
	}
}

func runPluginValidate(path string) {
	manifest, err := pluginruntime.LoadManifest(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: plugin manifest invalid: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("valid plugin manifest: %s@%s\n", manifest.Name, manifest.Version)
}

type stringListFlag []string

func (f *stringListFlag) String() string {
	if f == nil {
		return ""
	}
	return strings.Join(*f, ",")
}

func (f *stringListFlag) Set(value string) error {
	*f = append(*f, strings.TrimSpace(value))
	return nil
}

func runPluginInstall(args []string) {
	fs := flag.NewFlagSet("plugin install", flag.ExitOnError)
	idFlag := fs.String("id", "", "plugin id in Forge config")
	runtimeFlag := fs.String("runtime", "opencode", "plugin runtime: opencode")
	moduleFlag := fs.String("module", "", "OpenCode module specifier to import after install")
	disabled := fs.Bool("disabled", false, "install plugin disabled")
	noInstall := fs.Bool("no-install", false, "skip npm install for package sources")
	var autoApprove stringListFlag
	fs.Var(&autoApprove, "auto-approve", "plugin tool to auto-approve; may be repeated")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "usage: forge plugin install [--id ID] [--module NAME] [--auto-approve TOOL] <npm-package|git-url|local-js-url|local-path>")
		os.Exit(1)
	}
	source := cli.RequireArg(fs.Args(), "usage: forge plugin install [--id ID] [--module NAME] <npm-package|git-url|local-js-url|local-path>")
	if _, err := os.Stat(filepath.Join(source, pluginruntime.ManifestFilename)); err == nil && strings.TrimSpace(*moduleFlag) == "" && len(autoApprove) == 0 {
		store := pluginruntime.NewInstallStore(fsutil.ForgeConfigDir())
		installed, err := store.InstallLocal(source, pluginruntime.InstallOptions{})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error installing plugin manifest: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Installed local plugin %s@%s.\n", installed.Name, installed.Version)
		return
	}
	kind := strings.ToLower(strings.TrimSpace(*runtimeFlag))
	if kind == "" {
		kind = "opencode"
	}
	if kind != "opencode" {
		fmt.Fprintln(os.Stderr, "error: forge plugin install currently supports OpenCode plugins through --runtime opencode")
		os.Exit(1)
	}
	id := strings.TrimSpace(*idFlag)
	if id == "" {
		id = inferPluginID(firstNonEmpty(*moduleFlag, source))
	}
	if !validCLIPluginID(id) {
		fmt.Fprintf(os.Stderr, "error: invalid plugin id %q; use only letters, digits, underscores, or hyphens\n", id)
		os.Exit(1)
	}
	command, err := prepareOpenCodePluginCommand(id, source, strings.TrimSpace(*moduleFlag), *noInstall)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error installing plugin: %v\n", err)
		os.Exit(1)
	}

	cfg, err := loadMainConfigFn()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}
	plugin := config.PluginConfig{
		ID:               id,
		Kind:             kind,
		Source:           source,
		Command:          command,
		AutoApproveTools: compactStrings(autoApprove),
		StartupTimeoutMS: 3000,
		RequestTimeoutMS: 10000,
	}
	if *disabled {
		plugin.Enabled = boolPtr(false)
	}
	cfg.Plugins = upsertPluginConfig(cfg.Plugins, plugin)
	if err := saveMainConfigFn(mainConfigPathFn(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error saving config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Installed OpenCode plugin %s from %s.\n", id, source)
	fmt.Println("Note: Forge OpenCode compatibility supports plugin tools, hooks, and agent registration. Session, provider, and model APIs are available via Node.js built-ins. The OpenCode shell helper ($) and SSE events are not supported.")
}

func runPluginList() {
	cfg, err := loadMainConfigFn()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}
	if len(cfg.Plugins) == 0 {
		fmt.Println("No plugins configured.")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "ID\tKIND\tENABLED\tSOURCE"); err != nil {
		fmt.Fprintf(os.Stderr, "error writing plugin list: %v\n", err)
		os.Exit(1)
	}
	for _, plugin := range cfg.Plugins {
		kind := strings.TrimSpace(plugin.Kind)
		if kind == "" {
			kind = "forge-stdio"
		}
		source := strings.TrimSpace(plugin.Source)
		if source == "" && len(plugin.Command) > 0 {
			source = strings.Join(plugin.Command, " ")
		}
		if _, err := fmt.Fprintf(w, "%s\t%s\t%t\t%s\n", plugin.ID, kind, plugin.IsEnabled(), source); err != nil {
			fmt.Fprintf(os.Stderr, "error writing plugin list: %v\n", err)
			os.Exit(1)
		}
	}
	_ = w.Flush()
}

func runPluginRemove(id string) {
	cfg, err := loadMainConfigFn()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}
	next := cfg.Plugins[:0]
	removed := false
	for _, plugin := range cfg.Plugins {
		if strings.EqualFold(strings.TrimSpace(plugin.ID), strings.TrimSpace(id)) {
			removed = true
			continue
		}
		next = append(next, plugin)
	}
	if !removed {
		fmt.Fprintf(os.Stderr, "error: unknown plugin %q\n", id)
		os.Exit(1)
	}
	cfg.Plugins = next
	if err := saveMainConfigFn(mainConfigPathFn(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error saving config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Removed plugin %s.\n", id)
}

func prepareOpenCodePluginCommand(id, source, moduleOverride string, noInstall bool) ([]string, error) {
	configDir := filepath.Dir(mainConfigPathFn())
	if configDir == "." || configDir == "" {
		configDir = fsutil.ForgeConfigDir()
	}
	pluginDir := filepath.Join(configDir, "plugins")
	hostPath := filepath.Join(pluginDir, pluginruntime.OpenCodeHostFileName)
	if err := pluginruntime.WriteOpenCodeHost(hostPath); err != nil {
		return nil, err
	}

	moduleRef := moduleOverride
	installDir := ""
	if localPath, ok := localPluginPath(source); ok {
		if moduleRef == "" {
			moduleRef = localPath
		}
	} else if isRawJavaScriptURL(source) {
		downloadDir := filepath.Join(pluginDir, "opencode", id)
		modulePath := filepath.Join(downloadDir, "plugin"+filepath.Ext(urlPath(source)))
		if err := downloadPluginModule(source, modulePath); err != nil {
			return nil, err
		}
		if moduleRef == "" {
			moduleRef = modulePath
		}
	} else {
		installDir = filepath.Join(pluginDir, "opencode", id)
		if err := os.MkdirAll(installDir, 0o700); err != nil {
			return nil, err
		}
		if err := ensurePluginPackageJSON(installDir); err != nil {
			return nil, err
		}
		if !noInstall {
			if err := runPluginInstallCmdFn("npm", "install", "--silent", "--ignore-scripts", "--prefix", installDir, source); err != nil {
				return nil, err
			}
		}
		if moduleRef == "" {
			moduleRef = inferInstalledModule(source, installDir)
		}
	}
	if strings.TrimSpace(moduleRef) == "" {
		return nil, fmt.Errorf("could not infer OpenCode module; pass --module")
	}
	command := []string{openCodeRuntime(), hostPath, "--module", moduleRef}
	if installDir != "" {
		command = append(command, "--install-dir", installDir)
	}
	return command, nil
}

func openCodeRuntime() string {
	if _, err := exec.LookPath("bun"); err == nil {
		return "bun"
	}
	return "node"
}

func runPluginInstallCommand(name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return nil
}

func upsertPluginConfig(plugins []config.PluginConfig, plugin config.PluginConfig) []config.PluginConfig {
	out := make([]config.PluginConfig, 0, len(plugins)+1)
	replaced := false
	for _, existing := range plugins {
		if strings.EqualFold(strings.TrimSpace(existing.ID), strings.TrimSpace(plugin.ID)) {
			out = append(out, plugin)
			replaced = true
			continue
		}
		out = append(out, existing)
	}
	if !replaced {
		out = append(out, plugin)
	}
	return out
}

func ensurePluginPackageJSON(dir string) error {
	path := filepath.Join(dir, "package.json")
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return os.WriteFile(path, []byte("{}\n"), 0o600)
}

func downloadPluginModule(source, destination string) error {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(source)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download failed with status %s", resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return err
	}
	return os.WriteFile(destination, body, 0o600)
}

func inferInstalledModule(source, installDir string) string {
	if name := npmPackageName(source); name != "" {
		return name
	}
	if name := singleInstalledPackageName(filepath.Join(installDir, "node_modules")); name != "" {
		return name
	}
	return source
}

func singleInstalledPackageName(nodeModules string) string {
	entries, err := os.ReadDir(nodeModules)
	if err != nil {
		return ""
	}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if strings.HasPrefix(name, "@") && entry.IsDir() {
			scopedEntries, err := os.ReadDir(filepath.Join(nodeModules, name))
			if err != nil {
				continue
			}
			for _, scoped := range scopedEntries {
				if scoped.IsDir() {
					names = append(names, name+"/"+scoped.Name())
				}
			}
			continue
		}
		if entry.IsDir() {
			names = append(names, name)
		}
	}
	if len(names) == 1 {
		return names[0]
	}
	return ""
}

func npmPackageName(source string) string {
	trimmed := strings.TrimSpace(source)
	if trimmed == "" || strings.Contains(trimmed, "://") || strings.HasPrefix(trimmed, "git+") || strings.HasPrefix(trimmed, ".") || strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "~") {
		return ""
	}
	if strings.HasPrefix(trimmed, "@") {
		parts := strings.Split(trimmed, "/")
		if len(parts) >= 2 {
			return parts[0] + "/" + stripPackageVersion(parts[1])
		}
		return ""
	}
	return stripPackageVersion(trimmed)
}

func stripPackageVersion(name string) string {
	if i := strings.LastIndex(name, "@"); i > 0 {
		return name[:i]
	}
	return name
}

func localPluginPath(source string) (string, bool) {
	path := expandTildePath(strings.TrimSpace(source))
	if path == "" {
		return "", false
	}
	if _, err := os.Stat(path); err != nil {
		return "", false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path, true
	}
	return abs, true
}

func expandTildePath(path string) string {
	if path == "~" {
		return fsutil.UserHomeDir()
	}
	if suffix, ok := strings.CutPrefix(path, "~/"); ok {
		return filepath.Join(fsutil.UserHomeDir(), suffix)
	}
	return path
}

func isRawJavaScriptURL(source string) bool {
	u, err := url.Parse(source)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	ext := strings.ToLower(filepath.Ext(u.Path))
	return ext == ".js" || ext == ".mjs"
}

func urlPath(source string) string {
	u, err := url.Parse(source)
	if err != nil {
		return source
	}
	return u.Path
}

func inferPluginID(source string) string {
	var name string
	if pkg := npmPackageName(source); pkg != "" {
		name = pkg
	} else if u, err := url.Parse(source); err == nil && u.Path != "" {
		name = strings.TrimSuffix(filepath.Base(u.Path), filepath.Ext(u.Path))
	} else {
		name = strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	}
	name = strings.TrimPrefix(name, "@")
	name = strings.ReplaceAll(name, "/", "-")
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case r == '_' || r == '-':
			b.WriteRune(r)
		case r == '.' || unicode.IsSpace(r):
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "plugin"
	}
	return out
}

func validCLIPluginID(id string) bool {
	if strings.TrimSpace(id) == "" {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func runMCPList() {
	cfg, err := loadMainConfigFn()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}
	if len(cfg.MCPServers) == 0 {
		fmt.Println("No MCP servers configured.")
		return
	}
	names := make([]string, 0, len(cfg.MCPServers))
	for name := range cfg.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "NAME\tTYPE\tENABLED\tTARGET"); err != nil {
		fmt.Fprintf(os.Stderr, "error writing MCP list: %v\n", err)
		os.Exit(1)
	}
	for _, name := range names {
		server := cfg.MCPServers[name]
		target := strings.TrimSpace(server.URL)
		if target == "" && len(server.Command) > 0 {
			target = strings.Join(server.Command, " ")
		}
		authState := ""
		if token, ok, _ := mcp.BearerToken(name); ok && token != "" {
			authState = " (auth)"
		}
		if _, err := fmt.Fprintf(w, "%s%s\t%s\t%t\t%s\n", name, authState, inferMCPServerType(server), server.IsEnabled(), target); err != nil {
			fmt.Fprintf(os.Stderr, "error writing MCP list: %v\n", err)
			os.Exit(1)
		}
	}
	_ = w.Flush()
}

func runMCPGet(name string) {
	cfg, err := loadMainConfigFn()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}
	server, ok := cfg.MCPServers[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "error: unknown MCP server %q\n", name)
		os.Exit(1)
	}
	fmt.Printf("name = %s\n", name)
	fmt.Printf("type = %s\n", inferMCPServerType(server))
	fmt.Printf("enabled = %t\n", server.IsEnabled())
	if server.URL != "" {
		fmt.Printf("url = %s\n", server.URL)
	}
	if len(server.Command) > 0 {
		fmt.Printf("command = %s\n", strings.Join(server.Command, " "))
	}
	if server.TimeoutMS > 0 {
		fmt.Printf("timeout_ms = %d\n", server.TimeoutMS)
	}
	if token, ok, _ := mcp.BearerToken(name); ok && token != "" {
		fmt.Println("auth = bearer_token")
	}
}

func runMCPAdd(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: forge mcp add <name> [--url URL] [--timeout-ms N] [--disabled] [-- command args...]")
		os.Exit(1)
	}
	name := args[0]

	fs := flag.NewFlagSet("mcp add", flag.ExitOnError)
	url := fs.String("url", "", "Streamable HTTP MCP endpoint")
	disabled := fs.Bool("disabled", false, "Add the server in a disabled state")
	timeoutMS := fs.Int("timeout-ms", 0, "Per-request timeout in milliseconds")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "usage: forge mcp add <name> [--url URL] [--timeout-ms N] [--disabled] [-- command args...]")
		os.Exit(1)
	}
	command := append([]string(nil), fs.Args()...)
	if *url == "" && len(command) == 0 {
		if preset, ok := mcpServerPresets[name]; ok {
			server := preset
			if *timeoutMS > 0 {
				server.TimeoutMS = *timeoutMS
			}
			if *disabled {
				server.Enabled = boolPtr(false)
			}
			saveMCPServer(name, server)
			fmt.Printf("Added MCP server %s.\n", name)
			return
		}
		fmt.Fprintln(os.Stderr, "error: provide --url for a remote MCP server or command args for a stdio server")
		os.Exit(1)
	}
	if *url != "" && len(command) > 0 {
		fmt.Fprintln(os.Stderr, "error: choose either --url or a stdio command, not both")
		os.Exit(1)
	}

	server := config.MCPServerConfig{
		URL:       strings.TrimSpace(*url),
		Command:   command,
		TimeoutMS: *timeoutMS,
	}
	if *disabled {
		server.Enabled = boolPtr(false)
	}
	if server.URL != "" {
		server.Type = "remote"
	} else {
		server.Type = "stdio"
	}
	saveMCPServer(name, server)
	fmt.Printf("Added MCP server %s.\n", name)
}

func runMCPRemove(name string) {
	cfg, err := loadMainConfigFn()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}
	if _, ok := cfg.MCPServers[name]; !ok {
		fmt.Fprintf(os.Stderr, "error: unknown MCP server %q\n", name)
		os.Exit(1)
	}
	delete(cfg.MCPServers, name)
	if err := saveMainConfigFn(mainConfigPathFn(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error saving config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Removed MCP server %s.\n", name)
}

func runMCPLogin(name string) {
	cfg, err := loadMainConfigFn()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}
	server, ok := cfg.MCPServers[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "error: unknown MCP server %q\n", name)
		os.Exit(1)
	}
	if inferMCPServerType(server) != "remote" {
		fmt.Fprintln(os.Stderr, "error: forge mcp login is only supported for remote MCP servers")
		os.Exit(1)
	}
	token, err := promptMCPTokenFn()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading MCP token: %v\n", err)
		os.Exit(1)
	}
	if strings.TrimSpace(token) == "" {
		fmt.Fprintln(os.Stderr, "error: empty token")
		os.Exit(1)
	}
	if err := mcp.SaveBearerToken(name, token); err != nil {
		fmt.Fprintf(os.Stderr, "error saving MCP token: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Stored MCP token for %s.\n", name)
}

func runMCPLogout(name string) {
	if err := mcp.DeleteBearerToken(name); err != nil {
		fmt.Fprintf(os.Stderr, "error clearing MCP token: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Cleared MCP token for %s.\n", name)
}

func inferMCPServerType(server config.MCPServerConfig) string {
	if strings.TrimSpace(server.Type) != "" {
		return strings.TrimSpace(server.Type)
	}
	if strings.TrimSpace(server.URL) != "" {
		return "remote"
	}
	if len(server.Command) > 0 {
		return "stdio"
	}
	return "unknown"
}

func boolPtr(v bool) *bool { return &v }

func promptMCPToken() (string, error) {
	if _, err := fmt.Fprint(os.Stdout, "MCP bearer token: "); err != nil {
		return "", err
	}
	tokenBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	if _, printErr := fmt.Fprintln(os.Stdout); printErr != nil && err == nil {
		err = printErr
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(tokenBytes)), nil
}

func saveMCPServer(name string, server config.MCPServerConfig) {
	cfg, err := loadMainConfigFn()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}
	if cfg.MCPServers == nil {
		cfg.MCPServers = make(map[string]config.MCPServerConfig)
	}
	cfg.MCPServers[name] = server
	if err := saveMainConfigFn(mainConfigPathFn(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error saving config: %v\n", err)
		os.Exit(1)
	}
}

func runStatus() {
	tokens, err := auth.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading auth: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("forge status")
	if strings.TrimSpace(tokens.CopilotToken) != "" {
		fmt.Println("copilot: authenticated")
	} else {
		fmt.Println("copilot: not authenticated")
	}

	if strings.TrimSpace(tokens.CopilotToken) != "" {
		live, err := copilot.FetchUserQuota(context.Background(), tokens.CopilotToken)
		if err == nil && live != nil {
			printLiveCopilotQuota(live)
			return
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: live Copilot quota lookup failed: %v\n", err)
		}
	}
	fmt.Println("allowance: unavailable")
	fmt.Println("hint: run `forge auth copilot` to authenticate")
}

func printLiveCopilotQuota(live *copilot.UserQuota) {
	if live == nil || len(live.Windows) == 0 {
		fmt.Println("allowance: unavailable")
		return
	}
	order := []string{"chat", "completions", "premium"}
	seen := map[string]bool{}
	for _, name := range order {
		q, ok := live.Windows[name]
		if !ok {
			continue
		}
		seen[name] = true
		fmt.Printf("allowance[%s]: %s\n", name, copilot.FormatQuota(q))
	}
	for name, q := range live.Windows {
		if seen[name] {
			continue
		}
		fmt.Printf("allowance[%s]: %s\n", name, copilot.FormatQuota(q))
	}
	fmt.Println("source: live github api /copilot_internal/user")
}

func runHelp(args []string) {
	if len(args) == 0 {
		printHelp()
		return
	}
	switch args[0] {
	case "skills":
		printSkillsHelp()
	default:
		printHelp()
	}
}

func printHelp() {
	fmt.Print(`forge — terminal-first coding agent

Usage:
  forge                           Start interactive chat session
  forge status                    Show auth and Copilot allowance status
  forge mcp [list|get|add|remove|login|logout]
                                  Manage MCP server configuration
  forge plugin install <source>   Install an OpenCode plugin package, URL, or local module
  forge plugin list               List configured plugins
  forge skills list               List loaded skills
  forge skills dir                Show global/project skill directories
  forge skills install [flags] <source>
                                  Install skill file(s) into Forge
  forge auth copilot              Authenticate with GitHub Copilot
  forge help                      Show this help
  forge version                   Show version

Skills:
  Skills are markdown files with frontmatter loaded from:
    project: ./.forge/skills/
    global:  ~/.config/forge/skills/
  Use /skills in chat to list them and /<name> to activate one.
  You can install a local .md file, a local directory of .md files,
  or an HTTP(S) URL to a raw skill markdown file.

Chat flags:
  --yolo            Skip all approval prompts
  -p "PROMPT"       Headless: run one prompt, print the final response, exit
  --resume ID       Resume a stored session by thread id
  --continue        Resume the most recent session
  --model MODEL     Override chat model
  --auto-skills M   Auto skill mode: off, suggest, or auto
  -d                Open advanced debug view and write a fresh debug log
  --debug-file PATH Write debug log to PATH (default: temp dir forge-chat-debug-<timestamp>.jsonl)
  -C PATH           Set working directory (default: cwd)

Config: ~/.config/forge/config.toml
`)
}

func printSkillsHelp() {
	fmt.Print(`forge skills — install and inspect Forge chat skills

Usage:
  forge skills list
  forge skills dir
  forge skills status
  forge skills search <query>
  forge skills remove <name>
  forge skills update superpowers [--scope global|project]
  forge skills install [--scope global|project] <source>
  forge skills install [--scope global|project] --git <repo-url> [--subdir <path>]
  forge skills install [--scope global|project] superpowers [skill-name ...]

Install sources:
  <source> can be:
    - a local .md skill file
    - a local directory containing .md skill files
    - an HTTP(S) URL to a raw markdown skill file

Git installs:
  --git clones a repository and installs .md skills from the repo root or --subdir.

Superpowers shortcut:
  forge skills install superpowers
    installs a curated starter set:
      brainstorming
      systematic-debugging
      test-driven-development

  forge skills install superpowers brainstorming systematic-debugging
    installs only the named superpowers skills from obra/superpowers.

Directories:
  project: ./.forge/skills/
  global:  ~/.config/forge/skills/

In chat:
  /skills     list available skills
  /<name>     activate a skill
`)
}

func newTabWriter() *tabwriter.Writer {
	return tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
}

func runSkills(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: forge skills [list|dir|status|search|remove|update|install]")
		os.Exit(1)
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	switch args[0] {
	case "list":
		loaded := skills.Load(cwd)
		if len(loaded) == 0 {
			fmt.Println("no skills loaded")
			return
		}
		w := newTabWriter()
		if _, err := fmt.Fprintln(w, "NAME\tDESCRIPTION\tSOURCE"); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		for _, s := range loaded {
			if _, err := fmt.Fprintf(w, "%s\t%s\t%s\n", s.Name, s.Description, s.Source); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
		}
		if err := w.Flush(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "dir":
		globalDir, err := skills.UserDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		w := newTabWriter()
		if _, err := fmt.Fprintln(w, "SCOPE\tPATH"); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if _, err := fmt.Fprintf(w, "global\t%s\n", globalDir); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if _, err := fmt.Fprintf(w, "project\t%s\n", skills.ProjectDir(cwd)); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if err := w.Flush(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "status":
		entries, err := skills.Status(cwd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if len(entries) == 0 {
			fmt.Println("no skills loaded")
			return
		}
		w := newTabWriter()
		if _, err := fmt.Fprintln(w, "SCOPE\tNAME\tPROVIDER\tTRACKING\tFILE\tSOURCE"); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		for _, e := range entries {
			provider := e.Provider
			if provider == "" {
				provider = "manual"
			}
			tracked := "untracked"
			if e.Tracked {
				tracked = "tracked"
			}
			if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", e.Scope, e.Name, provider, tracked, e.File, e.Source); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
		}
		if err := w.Flush(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "search":
		query := cli.RequireArg(args[1:], "usage: forge skills search <query>")
		matches := skills.Search(cwd, query)
		if len(matches) == 0 {
			fmt.Println("no matching skills")
			return
		}
		w := newTabWriter()
		if _, err := fmt.Fprintln(w, "NAME\tDESCRIPTION\tSOURCE"); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		for _, s := range matches {
			if _, err := fmt.Fprintf(w, "%s\t%s\t%s\n", s.Name, s.Description, s.Source); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
		}
		if err := w.Flush(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "remove":
		name := cli.RequireArg(args[1:], "usage: forge skills remove <name>")
		removed, err := skills.RemoveByName(cwd, name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("removed /%s from %s\n", name, removed)
	case "update":
		if len(args) < 2 || args[1] != "superpowers" {
			fmt.Fprintln(os.Stderr, "usage: forge skills update superpowers [--scope global|project]")
			os.Exit(1)
		}
		fs := flag.NewFlagSet("skills update", flag.ExitOnError)
		scope := fs.String("scope", "global", "install scope: global or project")
		if err := fs.Parse(args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		var destDir string
		switch *scope {
		case "global":
			destDir, err = skills.UserDir()
		case "project":
			destDir = skills.ProjectDir(cwd)
		default:
			fmt.Fprintln(os.Stderr, "error: --scope must be global or project")
			os.Exit(1)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		installed, err := skills.UpdateSuperpowers(cwd, destDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		for _, s := range installed {
			fmt.Printf("updated /%s -> %s\n", s.Name, s.Source)
		}
	case "install":
		fs := flag.NewFlagSet("skills install", flag.ExitOnError)
		scope := fs.String("scope", "global", "install scope: global or project")
		gitRepo := fs.String("git", "", "git repository URL to install from")
		subdir := fs.String("subdir", "", "subdirectory within a git repo to install from")
		if err := fs.Parse(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		var destDir string
		switch *scope {
		case "global":
			destDir, err = skills.UserDir()
		case "project":
			destDir = skills.ProjectDir(cwd)
		default:
			fmt.Fprintln(os.Stderr, "error: --scope must be global or project")
			os.Exit(1)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		var installed []skills.Skill
		rest := fs.Args()
		switch {
		case *gitRepo != "":
			installed, err = skills.InstallFromGitRepo(*gitRepo, *subdir, destDir)
		case len(rest) > 0 && rest[0] == "superpowers":
			installed, err = skills.InstallSuperpowers(destDir, rest[1:])
		default:
			source := cli.RequireArg(rest, "usage: forge skills install [--scope global|project] <source>\n       forge skills install [--scope global|project] --git <repo-url> [--subdir <path>]\n       forge skills install [--scope global|project] superpowers [skill-name ...]")
			installed, err = skills.InstallFromSource(source, destDir)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		for _, s := range installed {
			fmt.Printf("installed /%s -> %s\n", s.Name, s.Source)
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: forge skills [list|dir|status|search|remove|update|install]")
		os.Exit(1)
	}
}

func runChat(args []string) {
	fs := flag.NewFlagSet("forge", flag.ExitOnError)
	yolo := fs.Bool("yolo", false, "skip all approval prompts")
	prompt := fs.String("p", "", "headless: run one prompt, print the final response, and exit")
	resume := fs.String("resume", "", "resume a stored session by thread id")
	continueLast := fs.Bool("continue", false, "resume the most recent session")
	model := fs.String("model", "", "model override")
	workDir := fs.String("C", "", "working directory (default: cwd)")
	autoSkills := fs.String("auto-skills", "", "auto skill mode: off, suggest, or auto")
	debug := fs.Bool("d", false, "open advanced debug view and write a fresh chat debug log")
	debugFile := fs.String("debug-file", "", "chat debug log path (default: temp dir forge-chat-debug-<timestamp>.jsonl)")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	cfg, err := bootstrap.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	if os.Getenv("FORGE_CHAT_YOLO") == "1" {
		*yolo = true
	}
	if cfg.Chat.Yolo {
		*yolo = true
	}
	if *autoSkills != "" {
		cfg.Chat.AutoSkills = *autoSkills
	}

	setup, err := runtimepkg.BuildChatSetup(cfg, nil, *model, *workDir, *yolo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if setup == nil {
		return
	}
	if *resume != "" || *continueLast {
		threadID, err := runtimepkg.ResolveResumeThreadID(setup.Config, *resume, *continueLast)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		setup.ResumeThreadID = threadID
	}
	if *debug {
		if _, err := runtimepkg.EnableChatDebug(setup, *debugFile); err != nil {
			fmt.Fprintf(os.Stderr, "error enabling chat debug: %v\n", err)
			os.Exit(1)
		}
	}
	if strings.TrimSpace(*prompt) != "" {
		osExit(runtimepkg.RunChatHeadless(setup, *prompt))
		return
	}
	if shouldUseChatConsole(term.IsTerminal(int(os.Stdin.Fd()))) {
		runtimepkg.RunChatConsole(setup)
		return
	}
	runtimepkg.RunChatLive(setup)
}

func shouldUseChatConsole(stdinIsTerminal bool) bool {
	if os.Getenv("FORGE_CHAT_CONSOLE") == "1" {
		return true
	}
	return !stdinIsTerminal
}

func runCopilotAuth() {
	cfg, err := bootstrap.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	clientID := cfg.CopilotClientID()
	if clientID == "" {
		fmt.Fprintln(os.Stderr, "error: no GitHub OAuth App client ID available")
		os.Exit(1)
	}

	ctx := context.Background()

	fmt.Println("Requesting device code from GitHub...")
	dc, err := copilot.RequestDeviceCode(ctx, clientID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n  Visit:  %s\n  Code:   %s\n\nWaiting for authorization...\n", dc.VerificationURI, dc.UserCode)

	token, err := copilot.PollForToken(ctx, clientID, dc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	tokens, _ := bootstrap.LoadTokens()
	tokens.CopilotToken = token
	if err := auth.Save(tokens); err != nil {
		fmt.Fprintf(os.Stderr, "error saving token: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\nAuthenticated! Copilot models are now available.")
	fmt.Println("Use copilot/gpt-5, copilot/claude-sonnet-4.5, copilot/gemini-2.5-pro, etc. in your config.")
}
