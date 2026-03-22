package prompts

import _ "embed"

//go:embed correctness.md
var Correctness string

//go:embed refactor.md
var Refactor string

//go:embed security.md
var Security string

//go:embed prod.md
var Prod string

//go:embed correctness-auditor.md
var CorrectnessAuditor string

//go:embed refactor-auditor.md
var RefactorAuditor string

//go:embed security-auditor.md
var SecurityAuditor string

//go:embed prod-auditor.md
var ProdAuditor string

//go:embed summarizer.md
var Summarizer string

//go:embed audit-log.md
var AuditLog string
