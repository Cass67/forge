package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"forge/internal/auth"
	"forge/internal/bootstrap"
	"forge/internal/cli"
	"forge/internal/copilot"
	"forge/internal/history"
	"forge/internal/llm"
	"forge/internal/output"
	runtimepkg "forge/internal/runtime"
	"forge/internal/session"
	"forge/internal/tui"
)

func main() {
	cli.Dispatch(os.Args[1:], map[string]cli.Command{
		"-h":        {Name: "help", Run: func(args []string) { printHelp() }},
		"--help":    {Name: "help", Run: func(args []string) { printHelp() }},
		"help":      {Name: "help", Run: func(args []string) { printHelp() }},
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
		"chat":    {Name: "chat", Run: func(args []string) { runChat() }},
		"improve": {Name: "improve", Run: func(args []string) { runImprove() }},
		"list":    {Name: "list", Run: func(args []string) { runList() }},
		"ls":      {Name: "ls", Run: func(args []string) { runList() }},
		"show": {
			Name: "show",
			Run: func(args []string) {
				runShow(cli.RequireArg(args, "usage: forge show <session-id>"))
			},
		},
	}, func() {
		runInteractive()
	})
}

func runInteractive() {
	rt, err := bootstrap.LoadRuntime()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}
	cfg := rt.Config
	tokens := rt.Tokens
	reg := rt.Registry
	available := rt.Models
	app := tui.NewApp(tui.AppConfig{
		WriterModels:   available,
		AuditorModels:  available,
		DefaultWriter:  cfg.Models.Writer,
		DefaultAuditor: cfg.Models.Auditor,
	})

	p := tea.NewProgram(app, tea.WithAltScreen())
	go bootstrap.SendStartupChecks(p, bootstrap.Preflight(cfg, tokens, reg))
	retModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	finalApp, _ := retModel.(tui.App)
	if !finalApp.Started {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gate := session.NewTurnGate()
	tracker := llm.NewUsageTracker()

	var feedbackChan chan string
	if finalApp.LastStart().Interactive {
		feedbackChan = make(chan string)
	}

	events, outDir := runtimepkg.StartSession(ctx, cfg, tokens, reg, finalApp.LastStart(), gate, "", tracker, feedbackChan)

	lastStart := finalApp.LastStart()
	for {
		totalPasses := 4
		if len(cfg.Pipeline) > 0 {
			totalPasses = len(cfg.Pipeline)
		}
		result := tui.RunLive(events, totalPasses, lastStart.Rounds, tui.LiveConfig{
			WriterModel:  lastStart.WriterModel,
			AuditorModel: lastStart.AuditorModel,
			Gate:         gate,
		}, outDir)

		if result.FeedbackRequested && feedbackChan != nil {
			passLabel := result.FeedbackPassName
			if passLabel == "" {
				passLabel = llm.PassName(result.FeedbackPass)
			}
			fmt.Printf("\n--- %s pass complete --- feedback (enter to skip): ", passLabel)
			scanner := bufio.NewScanner(os.Stdin)
			feedback := ""
			if scanner.Scan() {
				feedback = strings.TrimSpace(scanner.Text())
			}
			feedbackChan <- feedback
			continue
		}

		aborted := result.Aborted
		reason := ""
		if result.Err != nil {
			reason = result.Err.Error()
		}

		post := tui.RunPostSession(outDir, aborted, reason, formatTokenSummary(tracker), lastStart, available, available)
		if !post.Fix {
			break
		}

		lastStart = tui.SessionStarted{
			Prompt:       post.Issue,
			WriterModel:  post.WriterModel,
			AuditorModel: post.AuditorModel,
			Rounds:       lastStart.Rounds,
			LangHint:     lastStart.LangHint,
			ContextFiles: lastStart.ContextFiles,
			Interactive:  lastStart.Interactive,
		}
		gate = session.NewTurnGate()
		tracker = llm.NewUsageTracker()
		feedbackChan = nil
		if lastStart.Interactive {
			feedbackChan = make(chan string)
		}
		events, outDir = runtimepkg.StartSession(ctx, cfg, tokens, reg, lastStart, gate, filepath.Join(outDir, "code"), tracker, feedbackChan)
	}
}

func formatTokenSummary(tracker *llm.UsageTracker) string {
	if tracker == nil {
		return ""
	}
	total := tracker.Total()
	if total.InputTokens == 0 && total.OutputTokens == 0 {
		return ""
	}
	formatK := func(n int) string {
		if n < 1000 {
			return fmt.Sprintf("%d", n)
		}
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%s in / %s out", formatK(total.InputTokens), formatK(total.OutputTokens))
}

func runList() {
	cfg, err := bootstrap.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}
	sessions, err := history.List(cfg.Session.OutputDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(history.FormatList(sessions))
}

func runShow(id string) {
	cfg, err := bootstrap.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}
	detail, err := history.Show(cfg.Session.OutputDir, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(history.FormatDetail(detail))
}

func runImprove() {
	fs := flag.NewFlagSet("improve", flag.ExitOnError)
	prompt := fs.String("prompt", "", "what to improve (required)")
	writer := fs.String("writer", "", "writer model override")
	auditor := fs.String("auditor", "", "auditor model override")
	rounds := fs.Int("rounds", 0, "rounds per pass (0 = use config default)")
	apply := fs.Bool("apply", false, "apply changes back without prompting")

	args := os.Args[2:]
	var targetDir string
	// Extract positional arg (the directory path) before flag parsing
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		targetDir = args[0]
		args = args[1:]
	}
	fs.Parse(args)

	if targetDir == "" {
		fmt.Fprintln(os.Stderr, "usage: forge improve <path> --prompt \"...\"")
		os.Exit(1)
	}
	if *prompt == "" {
		fmt.Fprintln(os.Stderr, "error: --prompt is required")
		os.Exit(1)
	}

	info, err := os.Stat(targetDir)
	if err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "error: %s is not a directory\n", targetDir)
		os.Exit(1)
	}
	absTarget, _ := filepath.Abs(targetDir)

	cfg, err := bootstrap.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	tokens, _ := bootstrap.LoadTokens()

	writerModel := cfg.Models.Writer
	if *writer != "" {
		writerModel = *writer
	}
	auditorModel := cfg.Models.Auditor
	if *auditor != "" {
		auditorModel = *auditor
	}
	rpp := cfg.Session.RoundsPerPass
	if *rounds > 0 {
		rpp = *rounds
	}

	reg := bootstrap.BuildRegistry(cfg, tokens, writerModel, auditorModel, cfg.Models.Summarizer)

	tracker := llm.NewUsageTracker()
	started := tui.SessionStarted{
		Prompt:       *prompt,
		WriterModel:  writerModel,
		AuditorModel: auditorModel,
		Rounds:       rpp,
		LangHint:     "auto",
	}

	fmt.Printf("forge improve: %s\n", absTarget)
	fmt.Printf("writer: %s  auditor: %s  rounds: %d\n", writerModel, auditorModel, rpp)
	fmt.Printf("prompt: %s\n\n", *prompt)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, outDir := runtimepkg.StartSession(ctx, cfg, tokens, reg, started, nil, absTarget, tracker, nil)

	// Drain events, print progress
	for ev := range events {
		switch ev.Kind {
		case llm.EventPassStart:
			name := ev.PassName
			if name == "" {
				name = llm.PassName(ev.Pass)
			}
			fmt.Printf("--- pass %d: %s ---\n", ev.Pass, name)
		case llm.EventRoundStart:
			fmt.Printf("  round %d\n", ev.Round)
		case llm.EventAgentDone:
			fmt.Printf("    %s done\n", ev.Agent)
		case llm.EventDone:
			fmt.Println("\nsession complete")
		case llm.EventAbort, llm.EventError:
			fmt.Fprintf(os.Stderr, "\nerror: %v\n", ev.Err)
			os.Exit(1)
		}
	}

	fmt.Printf("output: %s\n", outDir)
	if summary := formatTokenSummary(tracker); summary != "" {
		fmt.Printf("tokens: %s\n", summary)
	}

	// Show diff summary
	before := snapshotDir(absTarget)
	after := snapshotDir(filepath.Join(outDir, "code"))
	diffs := output.DiffSnapshots(before, after)
	if len(diffs) == 0 {
		fmt.Println("\nno changes detected")
		return
	}

	fmt.Printf("\n%d file(s) changed:\n", len(diffs))
	for _, d := range diffs {
		fmt.Printf("  %s  %s\n", d.Status, d.Filename)
	}

	if *apply {
		w := &output.Writer{}
		_ = w // ApplyBack is a method on Writer, use it directly
		if err := applyChanges(outDir, absTarget); err != nil {
			fmt.Fprintf(os.Stderr, "error applying changes: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("\nchanges applied")
		return
	}

	fmt.Print("\napply changes? [y/N] ")
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() && strings.TrimSpace(strings.ToLower(scanner.Text())) == "y" {
		if err := applyChanges(outDir, absTarget); err != nil {
			fmt.Fprintf(os.Stderr, "error applying changes: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("changes applied")
	} else {
		fmt.Println("changes not applied (available in output dir)")
	}
}

func applyChanges(outDir, targetDir string) error {
	codeDir := filepath.Join(outDir, "code")
	return filepath.WalkDir(codeDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(codeDir, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(targetDir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return err
		}
		return nil
	})
}

func snapshotDir(dir string) output.CodeSnapshot {
	snap := output.CodeSnapshot{Files: make(map[string]string)}
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		snap.Files[rel] = string(data)
		return nil
	})
	return snap
}

func printHelp() {
	fmt.Print(`forge — terminal-based LLM code generation with writer/auditor review loops

Usage:
  forge                           Launch interactive session
  forge chat [flags]               Start interactive agent session
  forge improve <path> [flags]    Improve existing codebase
  forge list                      List past sessions
  forge show <id>                 Show session details
  forge auth copilot              Authenticate with GitHub Copilot
  forge help                      Show this help
  forge version                   Show version

Improve flags:
  --prompt "..."     What to improve (required)
  --writer MODEL     Writer model override
  --auditor MODEL    Auditor model override
  --rounds N         Rounds per pass (default: from config)
  --apply            Apply changes back without prompting

Chat flags:
  --yolo            Skip all approval prompts
  --live            Use split-pane live view (not yet implemented)
  --model MODEL     Override chat model
  -C PATH           Set working directory (default: cwd)

Interactive session keys:
  tab         Switch between writer/auditor model selection
  left/right  Cycle models
  ^T          Toggle interactive mode (feedback between passes)
  enter       Start session
  ^C          Quit

Live view keys:
  left/right  Focus writer/auditor pane
  up/down     Scroll focused pane
  m           Toggle manual step-through mode
  space/enter Advance (in manual mode)
  q/esc       Abort session

Done screen keys:
  o           Open output directory
  r           Review files
  f           Fix (start new session with same code)
  n           New session
  q           Quit

Config: ~/.config/forge/config.toml
Output: ./output/<timestamp>/
`)
}

func runChat() {
	fs := flag.NewFlagSet("chat", flag.ExitOnError)
	yolo := fs.Bool("yolo", false, "skip all approval prompts")
	model := fs.String("model", "", "model override")
	workDir := fs.String("C", "", "working directory (default: cwd)")
	live := fs.Bool("live", false, "use split-pane live view")
	fs.Parse(os.Args[2:])

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

	setup, err := runtimepkg.BuildChatSetup(cfg, nil, *model, *workDir, *yolo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if setup == nil {
		return
	}

	if *live {
		runtimepkg.RunChatLive(setup)
	} else {
		runtimepkg.RunChatConsole(setup)
	}
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
