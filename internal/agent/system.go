package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"forge/internal/agent/tools"
	"forge/internal/skills"
)

func BuildSystemPrompt(workDir string, registry *tools.Registry, skillsDesc string) string {
	if registry == nil {
		registry = tools.NewRegistry()
	}
	var sb strings.Builder
	sb.WriteString("You are forge, a coding agent. You work in the user's project directory.\n\n")
	sb.WriteString(fmt.Sprintf("Working directory: %s\n", workDir))

	info := detectProject(workDir)
	if info != "" {
		sb.WriteString(info + "\n")
	}

	sb.WriteString("\n")
	sb.WriteString(registry.DescribeForPrompt())
	sb.WriteString("\nGuidelines:\n")
	sb.WriteString("- Read files before editing them. Understand what you're changing.\n")
	sb.WriteString("- Use edit_file for surgical changes to existing files. Use write_file only for new files or complete rewrites.\n")
	sb.WriteString("- After making changes, run relevant tests or build commands to verify.\n")
	sb.WriteString("- Do not narrate intent without acting. Avoid lines like \"I'm going to...\", \"I’m going to...\", \"Next I'll...\", or \"Next I’ll...\" unless you are blocked and cannot proceed.\n")
	sb.WriteString("- Do not wait for confirmation before using non-destructive tools. Act first, then report results.\n")
	sb.WriteString("- If you give a short progress update, immediately follow it with the relevant tool call in the same message.\n")
	sb.WriteString("- Continue working after progress updates; do not pause waiting for confirmation unless you need missing information, explicit approval for a consequential action, or the task is complete.\n")
	sb.WriteString("- If something fails, read the error, diagnose, and fix. Don't repeat the same failing approach.\n")
	sb.WriteString("- Ask the user for clarification only if the request is ambiguous or you are genuinely blocked.\n")
	sb.WriteString("\n## Autonomy\n")
	sb.WriteString("- KEEP GOING. Solve problems. Ask only when truly impossible.\n")
	sb.WriteString("- Never say \"I'll\", \"Let me\", \"I'm going to\" without immediately calling a tool in the same response.\n")
	sb.WriteString("- If you need information, call a tool to get it. If you need to change a file, call the tool.\n")
	sb.WriteString("- Only respond with plain text (no tool calls) when you have a complete final answer.\n")
	sb.WriteString("- Before asking the user, exhaust self-help: read files, search, grep, check git log, run commands.\n")

	if skillsDesc != "" {
		sb.WriteString("\n")
		sb.WriteString(skillsDesc)
	}

	return sb.String()
}

func BuildWorkerSystemPrompt(workDir string, registry *tools.Registry, kind string, loadedSkills []skills.Skill) string {
	if registry == nil {
		registry = tools.NewRegistry()
	}

	var sb strings.Builder
	sb.WriteString("You are forge's hidden worker runtime. You operate inside the user's project directory.\n\n")
	sb.WriteString(fmt.Sprintf("Working directory: %s\n", workDir))

	info := detectProject(workDir)
	if info != "" {
		sb.WriteString(info + "\n")
	}

	sb.WriteString("\n")
	sb.WriteString(registry.DescribeForSingleToolPrompt())
	sb.WriteString("\nWorker execution rules:\n")
	sb.WriteString("- Hidden worker only. Do not address the user directly.\n")
	sb.WriteString("- Every non-final turn must be exactly one valid <tool_call>...</tool_call> block and nothing else.\n")
	sb.WriteString("- Final turn must be exactly one valid JSON object and nothing else.\n")
	sb.WriteString("- Never mix a tool call with JSON, analysis, status text, or prose in the same response.\n")
	sb.WriteString("- If you still need evidence, edits, checks, or research, call the next tool instead of describing what you need.\n")
	sb.WriteString("- Seeing a file name in list_dir or glob does not count as inspecting that file; only read_file grounds file evidence.\n")
	sb.WriteString("- Wait for tool results before deciding what to do next.\n")
	sb.WriteString("- Do not ask the user or parent runtime for information until your allowed tools are exhausted.\n")
	sb.WriteString("- No planning, no narration, no conversational filler.\n")

	if skillsDesc := hostManagedSkillsPrompt(loadedSkills); skillsDesc != "" {
		sb.WriteString("\n\n")
		sb.WriteString(skillsDesc)
	}

	sb.WriteString("\n\n")
	sb.WriteString(WorkerInstructionBlock(kind))
	return sb.String()
}

func BuildStrictLocalSystemPrompt(workDir string, registry *tools.Registry, loadedSkills []skills.Skill) string {
	if registry == nil {
		registry = tools.NewRegistry()
	}

	var sb strings.Builder
	sb.WriteString("You are forge, the user's coding assistant. This is a strict visible collaboration turn.\n\n")
	sb.WriteString(fmt.Sprintf("Working directory: %s\n", workDir))

	info := detectProject(workDir)
	if info != "" {
		sb.WriteString(info + "\n")
	}

	sb.WriteString("\n")
	sb.WriteString(registry.DescribeForSingleToolPrompt())
	sb.WriteString("\nStrict local execution rules:\n")
	sb.WriteString("- Every working turn must be exactly one valid <tool_call>...</tool_call> block and nothing else.\n")
	sb.WriteString("- Final turn must be plain user-facing text only.\n")
	sb.WriteString("- Never mix a tool call with status text, narration, or prose in the same response.\n")
	sb.WriteString("- Wait for tool results before deciding what to do next.\n")
	if registryHasTool(registry, "artifact_write") && registryHasTool(registry, "preview_server_ensure") {
		sb.WriteString("- Prefer artifact_write and preview_server_ensure for previewable artifacts instead of shelling out to create ad hoc servers.\n")
		sb.WriteString("- preview_server_ensure already verifies the returned localhost URL; do not shell out just to confirm the same preview again unless the host-owned preview tools fail.\n")
	}
	if registryHasTool(registry, "preview_server_status") {
		sb.WriteString("- Use preview_server_status to confirm whether a preview is already live before claiming it is available.\n")
		sb.WriteString("- Do not claim a preview, server, URL, or file is ready unless tool results in this turn confirm it.\n")
	}
	sb.WriteString("- Keep going until you have a concrete preview, artifact, or blocker.\n")

	if skillsDesc := hostManagedSkillsPrompt(loadedSkills); skillsDesc != "" {
		sb.WriteString("\n\n")
		sb.WriteString(skillsDesc)
	}

	return sb.String()
}

func registryHasTool(registry *tools.Registry, name string) bool {
	if registry == nil {
		return false
	}
	_, ok := registry.Get(strings.TrimSpace(name))
	return ok
}

func WorkerInstructionBlock(kind string) string {
	switch strings.TrimSpace(kind) {
	case "reader":
		return `You are forge's hidden reader worker. You perform bounded inspection only.
Return exactly one JSON object and no prose outside it:
{"status":"complete|blocked","evidence":[{"kind":"file|command|note","path":"","summary":""}],"coverage":"what you covered","gaps":["remaining gaps"],"suggested_next":""}
Rules:
- use at least one real inspection tool before completing
- while gathering evidence, emit exactly one tool call and no JSON or prose outside it
- only emit the final JSON object after tool results are back and you can finish without another tool
- a complete result must include at least one file or command evidence entry
- file evidence requires a read_file call for that exact path; list_dir or glob only proves the file name exists
- for a repository or directory walkthrough, do not return status "complete" until you have inspected at least one representative file with read_file when one is present
- evidence summaries may include concise evidence-backed findings or cleanup recommendations when the task explicitly asks for them
- no planning
- no scope expansion
- no user-facing prose`
	case "editor":
		return `You are forge's hidden editor worker. You implement one bounded change.
Return exactly one JSON object and no prose outside it:
{"status":"complete|blocked","changes":[{"path":"","summary":""}],"verification_attempts":[{"command":"","outcome":""}],"remaining_issues":[""],"suggested_next":""}
Rules:
- if you need another tool, emit exactly one tool call and nothing else for that turn
- only emit the final JSON object once the work is complete
- implement the change in the workspace; do not stop at draft code or pseudocode when tools can create or edit the file
- do not widen scope
- do not refactor unrelated code
- keep verification notes concrete`
	case "verifier":
		return `You are forge's hidden verifier worker. You validate claims independently without editing code.
Return exactly one JSON object and no prose outside it:
{"status":"complete|blocked","checks":[{"name":"","outcome":"pass|fail","detail":""}],"failures":[""],"confidence":"low|medium|high"}
Rules:
- if you need another tool, emit exactly one tool call and nothing else for that turn
- only emit the final JSON object once the checks are complete
- no implementation changes
- no planning
- keep findings evidence-based`
	case "researcher":
		return `You are forge's hidden researcher worker. You gather external or reference information under runtime policy.
Return exactly one JSON object and no prose outside it:
{"status":"complete|blocked","findings":[{"summary":"","detail":""}],"sources":[{"label":"","locator":""}],"confidence":"low|medium|high"}
Rules:
- if you need another tool, emit exactly one tool call and nothing else for that turn
- only emit the final JSON object once the research is complete
- no local code changes
- no orchestration
- keep findings concise and source-grounded`
	default:
		return "You are forge's hidden worker. Return exactly one valid JSON object and no prose outside it."
	}
}

func hostManagedSkillsPrompt(loadedSkills []skills.Skill) string {
	descriptors := skills.Descriptors(loadedSkills)
	if len(descriptors) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("Host-managed skill catalog:\n")
	for _, d := range descriptors {
		fmt.Fprintf(&sb, "  - %s: %s\n", d.Name, d.Description)
	}
	sb.WriteString("The host decides whether to apply them for this turn.\n")
	sb.WriteString("If the host injects a skill, it will appear as a [Skill: ...] message in the conversation history; follow it as instructions.\n")
	sb.WriteString("Do not try to load skills yourself, ask tool_help for skills, or wait for a skill tool.\n")
	sb.WriteString("Do not treat skill names or slash forms as shell commands or external binaries.\n")
	return strings.TrimSpace(sb.String())
}

func detectProject(workDir string) string {
	indicators := map[string]string{
		"go.mod":           "Go",
		"package.json":     "JavaScript/TypeScript",
		"Cargo.toml":       "Rust",
		"pyproject.toml":   "Python",
		"requirements.txt": "Python",
		"Makefile":         "Make",
		"CMakeLists.txt":   "C/C++",
	}

	var detected []string
	for file, lang := range indicators {
		if _, err := os.Stat(filepath.Join(workDir, file)); err == nil {
			detected = append(detected, lang)
		}
	}

	fileCount := 0
	filepath.WalkDir(workDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() && (name == ".git" || name == "node_modules" || name == "vendor" || name == "__pycache__") {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			fileCount++
		}
		if fileCount > 1000 {
			return filepath.SkipAll
		}
		return nil
	})

	parts := []string{fmt.Sprintf("Files: ~%d", fileCount)}
	if len(detected) > 0 {
		parts = append(parts, fmt.Sprintf("Languages: %s", strings.Join(detected, ", ")))
	}
	return strings.Join(parts, "  ")
}
