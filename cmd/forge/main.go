package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

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
	"forge/internal/skills"
	"forge/internal/tui"
)

var (
	runMakeInteractiveFn = runMakeInteractive
	runImproveArgsFn     = runImproveArgs
)

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
		"make": {Name: "make", Run: func(args []string) { runMake(args) }},
		"improve": {Name: "improve", Run: func(args []string) {
			runImproveArgsFn("improve", args)
		}},
		"list": {Name: "list", Run: func(args []string) { runList() }},
		"ls":   {Name: "ls", Run: func(args []string) { runList() }},
		"perf": {
			Name: "perf",
			Run: func(args []string) {
				runPerf(args)
			},
		},
		"show": {
			Name: "show",
			Run: func(args []string) {
				runShow(cli.RequireArg(args, "usage: forge show <session-id>"))
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

func runMake(args []string) {
	if len(args) == 0 {
		runMakeInteractiveFn()
		return
	}
	runImproveArgsFn("make", args)
}

func runMakeInteractive() {
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

func runStatus() {
	cfg, err := bootstrap.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}
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

	quota, sessionID, err := latestCopilotQuota(cfg.Session.OutputDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading session history: %v\n", err)
		os.Exit(1)
	}
	if quota == nil {
		fmt.Println("allowance: unavailable")
		fmt.Println("hint: run `forge auth copilot`, or run a Copilot-backed session to capture quota in session history")
		return
	}

	fmt.Printf("allowance: %s\n", copilot.FormatQuota(*quota))
	if sessionID != "" {
		fmt.Printf("source: session %s\n", sessionID)
	}
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

func latestCopilotQuota(outputDir string) (*llm.CopilotQuota, string, error) {
	sessions, err := history.List(outputDir)
	if err != nil {
		return nil, "", err
	}
	for _, s := range sessions {
		detail, err := history.Show(outputDir, s.ID)
		if err != nil {
			continue
		}
		for i := len(detail.Meta.TokenUsage) - 1; i >= 0; i-- {
			if q := detail.Meta.TokenUsage[i].Usage.CopilotQuota; q != nil {
				return q, detail.Meta.ID, nil
			}
		}
		if detail.Meta.TotalUsage.CopilotQuota != nil {
			return detail.Meta.TotalUsage.CopilotQuota, detail.Meta.ID, nil
		}
	}
	return nil, "", nil
}

type perfSummaryOptions struct {
	JSON      bool
	Sort      string
	Status    string
	Model     string
	Limit     int
	Completed bool
}

type perfShowOptions struct {
	JSON bool
}

type perfSessionRow struct {
	ID           string        `json:"id"`
	Status       string        `json:"status"`
	Writer       string        `json:"writer"`
	Auditor      string        `json:"auditor"`
	InputTokens  int           `json:"input_tokens"`
	OutputTokens int           `json:"output_tokens"`
	TotalTokens  int           `json:"total_tokens"`
	Calls        int           `json:"calls"`
	StartedAt    time.Time     `json:"started_at,omitempty"`
	CompletedAt  *time.Time    `json:"completed_at,omitempty"`
	Elapsed      time.Duration `json:"elapsed_ns,omitempty"`
	TokensPerSec float64       `json:"tokens_per_sec,omitempty"`
}

type perfSummaryResult struct {
	Sessions            []perfSessionRow `json:"sessions"`
	SessionCount        int              `json:"session_count"`
	SessionsWithUsage   int              `json:"sessions_with_usage"`
	InputTokens         int              `json:"input_tokens"`
	OutputTokens        int              `json:"output_tokens"`
	TotalTokens         int              `json:"total_tokens"`
	AvgInputTokens      int              `json:"avg_input_tokens"`
	AvgOutputTokens     int              `json:"avg_output_tokens"`
	AvgTotalTokens      int              `json:"avg_total_tokens"`
	TotalElapsed        time.Duration    `json:"total_elapsed_ns,omitempty"`
	AvgElapsed          time.Duration    `json:"avg_elapsed_ns,omitempty"`
	OverallTokensPerSec float64          `json:"overall_tokens_per_sec,omitempty"`
}

type perfCallRow struct {
	Index        int    `json:"index"`
	Agent        string `json:"agent"`
	Model        string `json:"model"`
	Pass         int    `json:"pass"`
	Round        int    `json:"round"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	TotalTokens  int    `json:"total_tokens"`
}

type perfShowResult struct {
	Session       perfSessionRow `json:"session"`
	Prompt        string         `json:"prompt"`
	RoundsPerPass int            `json:"rounds_per_pass"`
	Calls         []perfCallRow  `json:"calls,omitempty"`
}

func runPerf(args []string) {
	cfg, err := bootstrap.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	if len(args) == 0 {
		runPerfSummary(cfg.Session.OutputDir, perfSummaryOptions{Sort: "started"})
		return
	}

	switch args[0] {
	case "list", "summary":
		fs := flag.NewFlagSet("perf "+args[0], flag.ExitOnError)
		jsonOut := fs.Bool("json", false, "output JSON")
		sortBy := fs.String("sort", "started", "sort by: started|input|output|total|elapsed|tps")
		status := fs.String("status", "", "filter by session status")
		model := fs.String("model", "", "filter by writer or auditor model substring")
		limit := fs.Int("limit", 0, "max sessions to show (0 = all)")
		completed := fs.Bool("completed", false, "show only completed sessions")
		if err := fs.Parse(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		runPerfSummary(cfg.Session.OutputDir, perfSummaryOptions{
			JSON:      *jsonOut,
			Sort:      *sortBy,
			Status:    *status,
			Model:     *model,
			Limit:     *limit,
			Completed: *completed,
		})
		return
	case "show":
		fs := flag.NewFlagSet("perf show", flag.ExitOnError)
		jsonOut := fs.Bool("json", false, "output JSON")
		if err := fs.Parse(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		id := cli.RequireArg(fs.Args(), "usage: forge perf show [--json] <session-id>")
		runPerfShow(cfg.Session.OutputDir, id, perfShowOptions{JSON: *jsonOut})
		return
	default:
		fmt.Fprintln(os.Stderr, "usage: forge perf [summary|list] [--json] [--sort started|input|output|total|elapsed|tps] [--status STATUS] [--model SUBSTR] [--limit N] [--completed]")
		fmt.Fprintln(os.Stderr, "       forge perf show [--json] <session-id>")
		os.Exit(1)
	}
}

func runPerfSummary(outputDir string, opts perfSummaryOptions) {
	result := collectPerfSummary(outputDir, opts)
	if opts.JSON {
		writeJSON(result)
		return
	}
	if result.SessionCount == 0 {
		fmt.Println("No sessions found.")
		return
	}

	fmt.Printf("%-22s %10s %10s %10s %9s %9s %s\n", "ID", "INPUT", "OUTPUT", "TOTAL", "ELAPSED", "TOK/S", "STATUS")
	fmt.Println(strings.Repeat("─", 96))
	for _, s := range result.Sessions {
		fmt.Printf("%-22s %10s %10s %10s %9s %9s %s\n",
			s.ID,
			formatPerfCount(s.InputTokens),
			formatPerfCount(s.OutputTokens),
			formatPerfCount(s.TotalTokens),
			formatPerfDuration(s.Elapsed),
			formatPerfRate(s.TokensPerSec),
			s.Status,
		)
	}
	fmt.Println(strings.Repeat("─", 96))
	fmt.Printf("sessions: %d  with usage: %d\n", result.SessionCount, result.SessionsWithUsage)
	fmt.Printf("tokens:   in=%s  out=%s  total=%s\n", formatPerfCount(result.InputTokens), formatPerfCount(result.OutputTokens), formatPerfCount(result.TotalTokens))
	fmt.Printf("average:  in=%s  out=%s  total=%s\n", formatPerfCount(result.AvgInputTokens), formatPerfCount(result.AvgOutputTokens), formatPerfCount(result.AvgTotalTokens))
	if result.TotalElapsed > 0 {
		fmt.Printf("time:     total=%s  avg=%s  overall=%s tok/s\n", formatPerfDuration(result.TotalElapsed), formatPerfDuration(result.AvgElapsed), formatPerfRate(result.OverallTokensPerSec))
	}
	if result.SessionsWithUsage == 0 {
		fmt.Println("\nNo token usage recorded yet.")
	}
}

func runPerfShow(outputDir, id string, opts perfShowOptions) {
	detail, err := history.Show(outputDir, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	result := buildPerfShowResult(detail)
	if opts.JSON {
		writeJSON(result)
		return
	}

	fmt.Printf("Session: %s\n", result.Session.ID)
	fmt.Printf("Status:  %s\n", result.Session.Status)
	fmt.Printf("Writer:  %s\n", result.Session.Writer)
	fmt.Printf("Auditor: %s\n", result.Session.Auditor)
	if !result.Session.StartedAt.IsZero() {
		fmt.Printf("Started: %s\n", result.Session.StartedAt.Format("2006-01-02 15:04:05"))
	}
	if result.Session.CompletedAt != nil {
		fmt.Printf("Ended:   %s\n", result.Session.CompletedAt.Format("2006-01-02 15:04:05"))
	}
	if result.Session.Elapsed > 0 {
		fmt.Printf("Elapsed: %s\n", formatPerfDuration(result.Session.Elapsed))
	}
	if result.Session.TokensPerSec > 0 {
		fmt.Printf("Rate:    %s tok/s\n", formatPerfRate(result.Session.TokensPerSec))
	}
	fmt.Printf("Rounds:  %d per pass\n", result.RoundsPerPass)
	fmt.Printf("\nTotal usage:\n")
	fmt.Printf("  input:  %s\n", formatPerfCount(result.Session.InputTokens))
	fmt.Printf("  output: %s\n", formatPerfCount(result.Session.OutputTokens))
	fmt.Printf("  total:  %s\n", formatPerfCount(result.Session.TotalTokens))
	fmt.Printf("  calls:  %d\n", result.Session.Calls)
	if len(result.Calls) == 0 {
		fmt.Println("\nNo per-call token usage recorded.")
		return
	}
	fmt.Printf("\nCalls:\n")
	for _, entry := range result.Calls {
		label := entry.Model
		if label == "" {
			label = fmt.Sprintf("call %d", entry.Index)
		}
		if entry.Agent != "" {
			label = entry.Agent + "/" + label
		}
		fmt.Printf("  %-24s pass=%d round=%d in=%s out=%s total=%s\n",
			label,
			entry.Pass,
			entry.Round,
			formatPerfCount(entry.InputTokens),
			formatPerfCount(entry.OutputTokens),
			formatPerfCount(entry.TotalTokens),
		)
	}
}

func collectPerfSummary(outputDir string, opts perfSummaryOptions) perfSummaryResult {
	sessions, err := history.List(outputDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	var rows []perfSessionRow
	statusFilter := strings.ToLower(strings.TrimSpace(opts.Status))
	modelFilter := strings.ToLower(strings.TrimSpace(opts.Model))
	for _, s := range sessions {
		detail, err := history.Show(outputDir, s.ID)
		if err != nil {
			continue
		}
		row := buildPerfSessionRow(detail)
		if opts.Completed && row.Status != "complete" {
			continue
		}
		if statusFilter != "" && strings.ToLower(row.Status) != statusFilter {
			continue
		}
		if modelFilter != "" {
			models := strings.ToLower(row.Writer + " " + row.Auditor)
			if !strings.Contains(models, modelFilter) {
				continue
			}
		}
		rows = append(rows, row)
	}

	sortPerfRows(rows, opts.Sort)
	if opts.Limit > 0 && opts.Limit < len(rows) {
		rows = rows[:opts.Limit]
	}

	result := perfSummaryResult{Sessions: rows, SessionCount: len(rows)}
	var elapsedCount int
	for _, row := range rows {
		if row.InputTokens != 0 || row.OutputTokens != 0 {
			result.SessionsWithUsage++
		}
		result.InputTokens += row.InputTokens
		result.OutputTokens += row.OutputTokens
		result.TotalTokens += row.TotalTokens
		if row.Elapsed > 0 {
			result.TotalElapsed += row.Elapsed
			elapsedCount++
		}
	}
	if result.SessionCount > 0 {
		result.AvgInputTokens = result.InputTokens / result.SessionCount
		result.AvgOutputTokens = result.OutputTokens / result.SessionCount
		result.AvgTotalTokens = result.TotalTokens / result.SessionCount
	}
	if elapsedCount > 0 {
		result.AvgElapsed = result.TotalElapsed / time.Duration(elapsedCount)
		seconds := result.TotalElapsed.Seconds()
		if seconds > 0 {
			result.OverallTokensPerSec = float64(result.TotalTokens) / seconds
		}
	}
	return result
}

func buildPerfShowResult(detail *history.SessionDetail) perfShowResult {
	result := perfShowResult{
		Session:       buildPerfSessionRow(detail),
		Prompt:        detail.Meta.Prompt,
		RoundsPerPass: detail.Meta.RoundsPerPass,
	}
	for i, entry := range detail.Meta.TokenUsage {
		result.Calls = append(result.Calls, perfCallRow{
			Index:        i + 1,
			Agent:        entry.Agent,
			Model:        entry.Model,
			Pass:         entry.Pass,
			Round:        entry.Round,
			InputTokens:  entry.Usage.InputTokens,
			OutputTokens: entry.Usage.OutputTokens,
			TotalTokens:  entry.Usage.InputTokens + entry.Usage.OutputTokens,
		})
	}
	return result
}

func buildPerfSessionRow(detail *history.SessionDetail) perfSessionRow {
	row := perfSessionRow{
		ID:           detail.Meta.ID,
		Status:       detail.Meta.Status,
		Writer:       detail.Meta.Writer,
		Auditor:      detail.Meta.Auditor,
		InputTokens:  detail.Meta.TotalUsage.InputTokens,
		OutputTokens: detail.Meta.TotalUsage.OutputTokens,
		TotalTokens:  detail.Meta.TotalUsage.InputTokens + detail.Meta.TotalUsage.OutputTokens,
		Calls:        len(detail.Meta.TokenUsage),
		StartedAt:    detail.Meta.StartedAt,
		CompletedAt:  detail.Meta.CompletedAt,
	}
	if detail.Meta.CompletedAt != nil && !detail.Meta.StartedAt.IsZero() {
		row.Elapsed = detail.Meta.CompletedAt.Sub(detail.Meta.StartedAt)
		if row.Elapsed > 0 {
			row.TokensPerSec = float64(row.TotalTokens) / row.Elapsed.Seconds()
		}
	}
	return row
}

func sortPerfRows(rows []perfSessionRow, sortBy string) {
	switch sortBy {
	case "input":
		sort.Slice(rows, func(i, j int) bool { return rows[i].InputTokens > rows[j].InputTokens })
	case "output":
		sort.Slice(rows, func(i, j int) bool { return rows[i].OutputTokens > rows[j].OutputTokens })
	case "total":
		sort.Slice(rows, func(i, j int) bool { return rows[i].TotalTokens > rows[j].TotalTokens })
	case "elapsed":
		sort.Slice(rows, func(i, j int) bool { return rows[i].Elapsed > rows[j].Elapsed })
	case "tps":
		sort.Slice(rows, func(i, j int) bool { return rows[i].TokensPerSec > rows[j].TokensPerSec })
	default:
		sort.Slice(rows, func(i, j int) bool { return rows[i].ID > rows[j].ID })
	}
}

func writeJSON(v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(data))
}

func formatPerfCount(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1000000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%.1fM", float64(n)/1000000)
}

func formatPerfDuration(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	return d.Round(time.Second).String()
}

func formatPerfRate(v float64) string {
	if v <= 0 {
		return "-"
	}
	if v < 1000 {
		return fmt.Sprintf("%.1f", v)
	}
	if v < 1000000 {
		return fmt.Sprintf("%.1fk", v/1000)
	}
	return fmt.Sprintf("%.1fM", v/1000000)
}

func runImproveArgs(commandName string, args []string) {
	fs := flag.NewFlagSet(commandName, flag.ExitOnError)
	prompt := fs.String("prompt", "", "what to improve (required)")
	writer := fs.String("writer", "", "writer model override")
	auditor := fs.String("auditor", "", "auditor model override")
	rounds := fs.Int("rounds", 0, "rounds per pass (0 = use config default)")
	apply := fs.Bool("apply", false, "apply changes back without prompting")

	var targetDir string
	// Extract positional arg (the directory path) before flag parsing
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		targetDir = args[0]
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	if targetDir == "" {
		fmt.Fprintf(os.Stderr, "usage: forge %s <path> --prompt \"...\"\n", commandName)
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

	fmt.Printf("forge %s: %s\n", commandName, absTarget)
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
	fmt.Print(`forge — terminal-first coding agent with chat and writer/auditor pipeline modes

Usage:
  forge                           Start interactive chat session
  forge make                      Launch the legacy writer/auditor pipeline UI
  forge make <path> [flags]       Run the writer/auditor pipeline against a path
  forge improve <path> [flags]    Compatibility alias for forge make <path> [flags]
  forge list                      List past sessions
  forge show <id>                 Show session details
  forge status                    Show auth and Copilot allowance status
  forge perf [summary|list]       Show token/perf summary across sessions
  forge perf show <id>            Show token/perf details for a session
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

Pipeline flags:
  --prompt "..."     What to build or improve (required)
  --writer MODEL     Writer model override
  --auditor MODEL    Auditor model override
  --rounds N         Rounds per pass (default: from config)
  --apply            Apply changes back without prompting

Chat flags:
  --yolo            Skip all approval prompts
  --model MODEL     Override chat model
  --auto-skills M   Auto skill mode: off, suggest, or auto
  -d                Open advanced debug view and write a fresh debug log
  --debug-file PATH Write debug log to PATH (default: temp dir forge-chat-debug-<timestamp>.jsonl)
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
  q/esc       Abort session (Esc twice when idle)

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
	if *debug {
		path, err := runtimepkg.EnableChatDebug(setup, *debugFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error enabling chat debug: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "chat debug log: %s\n", path)
	}
	runtimepkg.RunChatLive(setup)
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
