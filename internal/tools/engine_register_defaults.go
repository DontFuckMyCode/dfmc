package tools

// engine_register_defaults.go — the New() constructor + the long
// default-registry build-out. Sibling of engine.go which keeps the
// Engine type, Execute() entrypoint, and the small Setter/accessor
// surface. Splitting here keeps the type / lifecycle file readable;
// the registry list (~80 lines of `e.Register(NewXxx())` calls) is
// easier to maintain in its own file.
//
// Construction order is load-bearing in two places:
//
//   - tools that take SetEngine(e) (write_file, edit_file,
//     apply_patch, dependency_graph, patch_validation, benchmark,
//     symbol_rename, symbol_move) need the Engine pointer for the
//     read-gate / project-root resolution, so they're constructed
//     after `e` is allocated and registered last in their group.
//
//   - MCP bridge adapters register AFTER native tools so a native
//     tool with the same name keeps its slot — caller wins on name
//     collision so the local tool wraps the bridge's payload exactly.
//
// run_command's timeout falls back through cfg.Tools.Shell.Timeout →
// cfg.Security.Sandbox.Timeout → 30s default; the same precedence is
// preserved here unchanged.

import (
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func NewFromConfig(cfg *config.Config) *Engine {
	return New(ToToolsConfigSubset(cfg))
}

func New(cfg ToolsConfigSubset) *Engine {
	readCap := cfg.Agent.ReadSnapshotCap
	if readCap <= 0 {
		readCap = maxReadSnapshots
	}
	failCap := cfg.Agent.RecentFailureCap
	if failCap <= 0 {
		failCap = maxRecentFailures
	}
	e := &Engine{
		registry:         map[string]Tool{},
		cfg:              cfg,
		recentFailures:   map[string]int{},
		recentFailOrder:  []string{},
		readSnapshots:    map[string]string{},
		readSnapshotLRU:  []string{},
		readSnapshotCap:  readCap,
		recentFailureCap: failCap,
		disabled:         newDisabledState(cfg.Tools.Disabled),
	}
	e.Register(NewReadFileTool())
	writeTool := NewWriteFileTool()
	writeTool.SetEngine(e)
	e.Register(writeTool)
	editTool := NewEditFileTool()
	editTool.SetEngine(e)
	e.Register(editTool)
	e.Register(NewListDirTool())
	e.Register(NewGrepCodebaseTool())
	e.Register(NewGlobTool())
	e.Register(NewThinkTool())
	e.Register(NewTodoWriteTool())
	webFetch := NewWebFetchTool()
	webFetch.SetAllowedHosts(cfg.WebFetch.AllowedHosts)
	e.Register(webFetch)
	e.Register(NewWebSearchTool())
	e.Register(NewASTQueryTool())
	e.Register(NewFindSymbolTool())
	e.Register(NewCallGraphTool())
	hTool := NewHuntTool()
	hTool.SetEngine(e)
	e.Register(hTool)
	aTool := NewAuditTool()
	aTool.SetEngine(e)
	e.Register(aTool)
	dcTool := NewDeadCodeTool()
	dcTool.SetEngine(e)
	e.Register(dcTool)
	e.Register(NewCodemapTool())
	e.Register(NewSpecParseTool())
	e.Register(NewSpecToTodoTool())
	e.Register(NewSpecValidateTool())
	e.Register(NewTestDiscoveryTool())
	autoTestTool := NewAutoTestTool()
	autoTestTool.SetEngine(e)
	e.Register(autoTestTool)
	depAuditTool := NewDependencyAuditTool()
	depAuditTool.SetEngine(e)
	e.Register(depAuditTool)
	depTool := NewDependencyGraphTool()
	// SetEngine uses a deferred pattern: it stores the engine and will
	// re-read the codemap field when SetCodemap is called later (from
	// engine.Init). This avoids a circular import while still allowing
	// the engine to inject the codemap after tools.New() returns.
	depTool.SetEngine(e)
	e.Register(depTool)
	apTool := NewApplyPatchTool()
	apTool.SetEngine(e)
	e.Register(apTool)
	e.Register(NewGitStatusTool())
	e.Register(NewGitDiffTool())
	e.Register(NewGitBranchTool())
	e.Register(NewGitLogTool())
	e.Register(NewGitBlameTool())
	e.Register(NewGitWorktreeListTool())
	e.Register(NewGitWorktreeAddTool())
	e.Register(NewGitWorktreeRemoveTool())
	e.Register(NewGitCommitTool())
	e.Register(NewGHPullRequestTool())
	pvTool := NewPatchValidationTool()
	pvTool.SetEngine(e)
	e.Register(pvTool)
	benchTool := NewBenchmarkTool()
	benchTool.SetEngine(e)
	e.Register(benchTool)
	regressTool := NewBenchmarkRegressionTool()
	regressTool.SetEngine(e)
	e.Register(regressTool)
	intfDiffTool := NewInterfaceDiffTool()
	intfDiffTool.SetEngine(e)
	e.Register(intfDiffTool)
	docGenTool := NewDocGenerateTool()
	e.Register(docGenTool)
	e.Register(NewGitReviewTool())
	e.Register(NewChangelogGenerateTool())
	symRenameTool := NewSymbolRenameTool()
	symRenameTool.SetEngine(e)
	e.Register(symRenameTool)
	symMoveTool := NewSymbolMoveTool()
	symMoveTool.SetEngine(e)
	e.Register(symMoveTool)
	e.Register(NewSemanticSearchTool())
	e.Register(NewProjectInfoTool())
	e.Register(NewTaskSplitTool())
	e.delegateTool = NewDelegateTaskTool()
	e.Register(e.delegateTool)
	e.orchestrateTool = NewOrchestrateTool()
	e.orchestrateTool.SetMaxParallelCeiling(cfg.Agent.ParallelBatchSize)
	e.orchestrateTool.SetMaxAutoSubtasks(cfg.Agent.OrchestrateAutoSubtasks)
	e.Register(e.orchestrateTool)
	timeout, err := time.ParseDuration(strings.TrimSpace(cfg.Tools.Shell.Timeout))
	if err != nil || timeout <= 0 {
		timeout, _ = time.ParseDuration(strings.TrimSpace(cfg.Security.Sandbox.Timeout))
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	e.Register(NewRunCommandTool(runCommandConfig{
		allowShell: cfg.Security.Sandbox.AllowShell,
		timeout:    timeout,
		blocked:    append([]string(nil), cfg.Tools.Shell.BlockedCommands...),
	}))
	RegisterMetaTools(e)
	// Register MCP bridge tools after native tools so the bridge can
	// shadow a native tool with the same name (caller wins — web/CLI
	// that already has native tools gets a flat union, not a replacement).
	if e.mcpBridge != nil {
		for _, td := range e.mcpBridge.List() {
			e.Register(&mcpToolAdapter{bridge: e.mcpBridge, name: td.Name})
		}
	}
	return e
}
