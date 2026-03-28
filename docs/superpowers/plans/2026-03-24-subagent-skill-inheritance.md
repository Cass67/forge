# Subagent Skill Inheritance Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Forge's delegated agents work correctly with installed and activated skills, including Superpowers-style skills, without breaking the dispatch-orchestrator architecture.

**Architecture:** Keep top-level skill loading unchanged, but propagate the loaded skill catalog and the currently activated skill context into spawned subagents. Subagents should inherit only applicable active skills; skills that explicitly opt out of subagent execution must not be injected. The propagation should be local to the spawned child so dispatch, scout, architect, and builder remain role-constrained and do not mutate the parent's skill state.

**Tech Stack:** Go stdlib, existing `internal/skills`, `internal/chatstate`, and `internal/agent` packages, existing `go test` suite.

---

## File Structure

| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `internal/chatstate/skills.go` | Add a safe way to snapshot or clone active skill state for child agents |
| Modify | `internal/chatstate/skills_test.go` | Verify cloned skill state preserves active skills without aliasing parent state |
| Modify | `internal/skills/skills.go` | Add helper(s) for selecting active skills for subagents and honoring `<SUBAGENT-STOP>` |
| Modify | `internal/skills/skills_test.go` | Verify subagent applicability filtering and active-skill lookup behavior |
| Modify | `internal/agent/subagent.go` | Pass loaded skills into subagents, rebuild child system prompt with skills listed, inject active applicable skills before running |
| Modify | `internal/agent/agent_test.go` | Add regressions for subagent skill inheritance and subagent opt-out behavior |

## Chunk 1: Skill Context Primitives

### Task 1: Clone Active Skill State For Child Agents

**Files:**
- Modify: `internal/chatstate/skills.go`
- Modify: `internal/chatstate/skills_test.go`

- [ ] **Step 1: Write a failing clone test**

Add to `internal/chatstate/skills_test.go`:

```go
func TestCloneCopiesActiveSkillsWithoutAliasing(t *testing.T) {
	parent := New()
	parent.ActivateSkill("brainstorming")
	parent.ActivateSkill("writing-plans")

	child := parent.Clone()

	if !child.SkillActivated("brainstorming") || !child.SkillActivated("writing-plans") {
		t.Fatal("clone should preserve active skills")
	}

	child.ActivateSkill("subagent-only")
	if parent.SkillActivated("subagent-only") {
		t.Fatal("child mutation should not affect parent")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chatstate -run TestCloneCopiesActiveSkillsWithoutAliasing -v`
Expected: FAIL because `Clone` does not exist yet.

- [ ] **Step 3: Add the minimal clone implementation**

Implement in `internal/chatstate/skills.go`:

```go
func (s *State) Clone() *State {
	if s == nil {
		return New()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	cloned := New()
	for name := range s.activatedSkills {
		cloned.activatedSkills[name] = true
	}
	return cloned
}
```

- [ ] **Step 4: Run the chatstate test**

Run: `go test ./internal/chatstate -run TestCloneCopiesActiveSkillsWithoutAliasing -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chatstate/skills.go internal/chatstate/skills_test.go
git commit -m "Add clone support for active skill state"
```

### Task 2: Filter Active Skills For Subagent Use

**Files:**
- Modify: `internal/skills/skills.go`
- Modify: `internal/skills/skills_test.go`

- [ ] **Step 1: Write failing tests for subagent applicability**

Add to `internal/skills/skills_test.go`:

```go
func TestActiveForSubagentSkipsSubagentStopSkills(t *testing.T) {
	loaded := []Skill{
		{Name: "using-superpowers", Body: "<SUBAGENT-STOP>\nskip me\n</SUBAGENT-STOP>"},
		{Name: "test-driven-development", Body: "write the test first"},
	}
	active := []string{"using-superpowers", "test-driven-development"}

	got := ActiveForSubagent(loaded, active)
	if len(got) != 1 || got[0].Name != "test-driven-development" {
		t.Fatalf("got %#v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/skills -run TestActiveForSubagentSkipsSubagentStopSkills -v`
Expected: FAIL because `ActiveForSubagent` does not exist yet.

- [ ] **Step 3: Add the minimal helper**

Implement in `internal/skills/skills.go`:

```go
func ActiveForSubagent(loaded []Skill, active []string) []Skill {
	if len(active) == 0 || len(loaded) == 0 {
		return nil
	}
	byName := make(map[string]Skill, len(loaded))
	for _, s := range loaded {
		byName[s.Name] = s
	}
	var out []Skill
	for _, name := range active {
		s, ok := byName[name]
		if !ok {
			continue
		}
		if strings.Contains(s.Body, "<SUBAGENT-STOP>") {
			continue
		}
		out = append(out, s)
	}
	return out
}
```

- [ ] **Step 4: Run the skills tests**

Run: `go test ./internal/skills -run 'TestActiveForSubagentSkipsSubagentStopSkills|TestLoad' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/skills/skills.go internal/skills/skills_test.go
git commit -m "Add subagent skill applicability filtering"
```

## Chunk 2: Subagent Inheritance

### Task 3: Propagate Loaded Skills And Active Skill Context Into Child Agents

**Files:**
- Modify: `internal/agent/subagent.go`
- Modify: `internal/agent/agent_test.go`

- [ ] **Step 1: Write a failing test for inherited active skills**

Add to `internal/agent/agent_test.go` a focused subagent test using a mock driver:

```go
func TestSpawnSubAgentInheritsApplicableActiveSkills(t *testing.T) {
	parentDriver := &mockDriver{}
	childDriver := &mockDriver{responses: []string{"done"}}
	reg := tools.NewRegistry()
	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)

	loaded := []skills.Skill{
		{Name: "test-driven-development", Description: "Write tests first", Body: "Always start with a failing test."},
	}
	state := chatstate.New()
	parent := NewAgent(parentDriver, reg, YoloApproval(), t.TempDir(), 10, renderer, loaded, state)
	parent.InjectSkill(loaded[0])

	_, err := parent.SpawnSubAgent(context.Background(), "architect", "plan it", MultiAgentConfig{
		BaseTools:   reg,
		MakeDriver:  func(string) llm.Driver { return childDriver },
		RoleModels:  map[string]string{"architect": "child"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := childDriver.lastMessages(); !containsSkillMessage(got, "test-driven-development") {
		t.Fatalf("child messages missing active skill context: %#v", got)
	}
}
```

- [ ] **Step 2: Write a failing test for subagent opt-out**

Add to `internal/agent/agent_test.go`:

```go
func TestSpawnSubAgentSkipsSkillsMarkedSubagentStop(t *testing.T) {
	// same setup as above, but loaded skill body contains <SUBAGENT-STOP>
	// assert the child messages do not contain that skill body
}
```

- [ ] **Step 3: Run agent tests to verify they fail**

Run: `go test ./internal/agent -run 'TestSpawnSubAgentInheritsApplicableActiveSkills|TestSpawnSubAgentSkipsSkillsMarkedSubagentStop' -v`
Expected: FAIL because subagents currently do not inherit loaded skills or active skill injections.

- [ ] **Step 4: Update subagent construction**

In `internal/agent/subagent.go`:

```go
filteredTools := mac.BaseTools.Filter(roleDef.AllowTools)
childSkills := append([]skills.Skill(nil), a.skills...)
childState := a.state.Clone()
activeSkills := skills.ActiveForSubagent(childSkills, childState.ActiveSkills())

system := BuildSystemPrompt(a.workDir, filteredTools, skills.Describe(childSkills)) + "\n\n" + roleDef.System

sub := &Agent{
	driver:     driver,
	tools:      filteredTools,
	approve:    a.approve,
	workDir:    a.workDir,
	maxTurns:   roleDef.MaxTurns,
	renderer:   subRenderer,
	system:     system,
	systemOverride: true,
	skills:     childSkills,
	state:      childState,
	isSubAgent: true,
	role:       role,
}
for _, s := range activeSkills {
	sub.InjectSkill(s)
}
```

Implementation notes:
- Preserve the role-specific fixed system prompt behavior.
- Do not share the parent `chatstate.State` pointer directly.
- Do not inject inactive skills.
- Do not inject skills filtered by `<SUBAGENT-STOP>`.

- [ ] **Step 5: Run the focused agent tests**

Run: `go test ./internal/agent -run 'TestSpawnSubAgentInheritsApplicableActiveSkills|TestSpawnSubAgentSkipsSkillsMarkedSubagentStop' -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/subagent.go internal/agent/agent_test.go
git commit -m "Propagate applicable skill context to subagents"
```

## Chunk 3: Regression Coverage And Verification

### Task 4: Lock Down Behavior Across The Existing Agent Flow

**Files:**
- Modify: `internal/agent/agent_test.go`
- Modify: `internal/skills/skills_test.go`
- Modify: `internal/chatstate/skills_test.go`

- [ ] **Step 1: Add one regression proving role constraints still win**

Example target:

```go
func TestDispatchSubAgentInheritanceDoesNotBreakDispatchRole(t *testing.T) {
	// active planning skill should not cause dispatch to emit prose or skip delegation
}
```

- [ ] **Step 2: Run the narrow regression suite**

Run: `go test ./internal/chatstate ./internal/skills ./internal/agent`
Expected: PASS.

- [ ] **Step 3: Run the full suite**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/chatstate/skills_test.go internal/skills/skills_test.go internal/agent/agent_test.go
git commit -m "Add regressions for skill-aware subagents"
```

## Risks

- Injecting every active skill blindly into subagents could overpower role prompts or make dispatch verbose again. Keep inheritance limited to active skills, and skip skills with explicit subagent opt-out markers.
- Sharing the parent chat state pointer would create hidden coupling between parent and child skill activation. Clone state instead.
- Rebuilding the subagent prompt incorrectly could drop role constraints. Keep `roleDef.System` appended after the base prompt, as it is today.

## Recommended Implementation Order

1. Add `chatstate.State.Clone()` and tests.
2. Add `skills.ActiveForSubagent(...)` and tests.
3. Update `SpawnSubAgent(...)` to copy skills/state and inject applicable active skills.
4. Add regressions for inheritance and `<SUBAGENT-STOP>`.
5. Run `go test ./internal/chatstate ./internal/skills ./internal/agent` and `go test ./...`.

Plan complete and saved to `docs/superpowers/plans/2026-03-24-subagent-skill-inheritance.md`. Ready to execute?
