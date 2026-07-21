package skills

import (
	"context"
	"fmt"
	"math"
	"net/http"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/wellxie/agentmate/internal/auth"
	"github.com/wellxie/agentmate/internal/mcpauth"
)

var toolScopes = map[string]string{
	"skill_log_add":              "skills:rw",
	"skill_version_publish":      "skills:rw",
	"skill_version_get_active":   "skills:r",
	"skill_stats":                "skills:r",
	"skill_signals":              "skills:r",
	"skill_logs_list":            "skills:r",
	"skill_search":               "skills:r",
	"skill_source_sync":          "skills:rw",
	"skill_index_active":         "skills:rw",
	"skill_catalog_list":         "skills:r",
	"skill_compile":              "skills:rw",
	"skill_version_instructions": "skills:r",
	"skill_version_resources":    "skills:r",
	"skill_resource_get":         "skills:r",
	"skill_quality_run":          "skills:rw",
	"skill_quality_get":          "skills:r",
}

func NewMCPServer(svc *Service, authSvc *auth.Service) http.Handler {
	s := server.NewMCPServer("agentmate-skills", "0.1.0", server.WithToolHandlerMiddleware(mcpauth.ScopeMiddleware(toolScopes)))

	// skill_log_add
	s.AddTool(mcp.NewTool("skill_log_add",
		mcp.WithDescription("Record a skill execution log entry. Call after every skill execution to track performance and learning signals. outcome must be: success/failure/partial/user_corrected. Set was_triggered=false for silent-bypass cases where skill was never invoked."),
		mcp.WithString("skill_name", mcp.Required(), mcp.Description("Skill name, e.g. 'agentmate', 'sa-weekly-kpi'")),
		mcp.WithString("outcome", mcp.Required(), mcp.Description("success/failure/partial/user_corrected")),
		mcp.WithString("agent_id", mcp.Description("Agent that executed the skill")),
		mcp.WithString("skill_version_id", mcp.Description("Optional immutable AgentMate skill version ID; must be account-owned and match skill_name")),
		mcp.WithString("skill_version", mcp.Description("Legacy version label; canonicalized from skill_version_id when provided")),
		mcp.WithString("session_id", mcp.Description("Session/conversation ID")),
		mcp.WithString("trigger_text", mcp.Description("Original user input that triggered this skill")),
		mcp.WithBoolean("was_triggered", mcp.Description("false if silent-bypass: skill appeared valid but was never invoked")),
		mcp.WithString("failure_reason", mcp.Description("Why it failed (for failure/partial outcomes)")),
		mcp.WithString("user_correction", mcp.Description("What the user corrected (for user_corrected outcome)")),
		mcp.WithNumber("duration_ms", mcp.Description("Execution duration in milliseconds")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		r := CreateLogRequest{
			SkillName:      mcpauth.StrArg(args, "skill_name"),
			Outcome:        mcpauth.StrArg(args, "outcome"),
			AgentID:        mcpauth.StrArg(args, "agent_id"),
			SkillVersionID: mcpauth.StrArg(args, "skill_version_id"),
			SkillVersion:   mcpauth.StrArg(args, "skill_version"),
			SessionID:      mcpauth.StrArg(args, "session_id"),
			TriggerText:    mcpauth.StrArg(args, "trigger_text"),
			FailureReason:  mcpauth.StrArg(args, "failure_reason"),
			UserCorrection: mcpauth.StrArg(args, "user_correction"),
			WasTriggered:   mcpauth.BoolPtrArg(args, "was_triggered"),
		}
		if v := mcpauth.FloatPtrArg(args, "duration_ms"); v != nil {
			d := int(*v)
			r.DurationMs = &d
		}
		l, err := svc.CreateLog(ctx, owner, r)
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(l)
	})

	// skill_version_publish
	s.AddTool(mcp.NewTool("skill_version_publish",
		mcp.WithDescription("Publish a new skill version. Stores the full skill content with version tag and optional activation."),
		mcp.WithString("skill_name", mcp.Required(), mcp.Description("Skill name")),
		mcp.WithString("version", mcp.Required(), mcp.Description("Version tag, e.g. 'v2'")),
		mcp.WithString("content", mcp.Required(), mcp.Description("Full skill content (prompt/config)")),
		mcp.WithString("agent_id", mcp.Description("Agent that published this version")),
		mcp.WithString("change_summary", mcp.Description("What changed from previous version")),
		mcp.WithNumber("eval_pass_rate", mcp.Description("Evaluation pass rate 0.0-1.0")),
		mcp.WithBoolean("activate", mcp.Description("Set as active version immediately (default false)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		r := CreateVersionRequest{
			SkillName:     mcpauth.StrArg(args, "skill_name"),
			Version:       mcpauth.StrArg(args, "version"),
			Content:       mcpauth.StrArg(args, "content"),
			AgentID:       mcpauth.StrArg(args, "agent_id"),
			ChangeSummary: mcpauth.StrArg(args, "change_summary"),
			EvalPassRate:  mcpauth.FloatPtrArg(args, "eval_pass_rate"),
			Activate:      mcpauth.BoolArg(args, "activate"),
		}
		ver, err := svc.CreateVersion(ctx, owner, r)
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(ver)
	})

	// skill_version_get_active
	s.AddTool(mcp.NewTool("skill_version_get_active",
		mcp.WithDescription("Get the currently active version of a skill. Returns full content."),
		mcp.WithString("skill_name", mcp.Required(), mcp.Description("Skill name")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		v, err := svc.GetActiveVersion(ctx, owner.Account(), mcpauth.StrArg(req.GetArguments(), "skill_name"))
		if err != nil {
			return mcpauth.ErrResult("not found"), nil
		}
		return mcpauth.JSONResult(v)
	})

	// skill_stats
	s.AddTool(mcp.NewTool("skill_stats",
		mcp.WithDescription("Get aggregated performance stats for a skill: total runs, success/failure/correction rates."),
		mcp.WithString("skill_name", mcp.Required(), mcp.Description("Skill name")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		stats, err := svc.GetSkillStats(ctx, owner.Account(), mcpauth.StrArg(req.GetArguments(), "skill_name"))
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(stats)
	})

	// skill_signals
	s.AddTool(mcp.NewTool("skill_signals",
		mcp.WithDescription("Get recent failure/correction signals for a skill. Use as learning input for skill evolution."),
		mcp.WithString("skill_name", mcp.Required(), mcp.Description("Skill name")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 10, max 50)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		limit := mcpauth.IntArg(args, "limit")
		if limit == 0 {
			limit = 10
		}
		signals, err := svc.SkillSignals(ctx, owner.Account(), mcpauth.StrArg(args, "skill_name"), limit)
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(signals)
	})

	// skill_search
	s.AddTool(mcp.NewTool("skill_search",
		mcp.WithDescription("Search active skill versions semantically. Use to find the best skill for a task without loading every skill into context."),
		mcp.WithString("query", mcp.Required(), mcp.Description("User task or capability needed")),
		mcp.WithNumber("top_k", mcp.Description("Max results (default 5, max 20)")),
		mcp.WithBoolean("include_content", mcp.Description("Return indexed content for selected skills")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		searchReq := SearchSkillsRequest{
			Query:          mcpauth.StrArg(args, "query"),
			TopK:           mcpauth.IntArg(args, "top_k"),
			IncludeContent: mcpauth.BoolArg(args, "include_content"),
		}
		result, err := svc.Search(ctx, owner, searchReq)
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(result)
	})

	// skill_source_sync
	s.AddTool(mcp.NewTool("skill_source_sync",
		mcp.WithDescription("Sync a public GitHub or GitLab skill source. Resolves a ref to an immutable commit, imports the configured package_path, and optionally activates and indexes the resulting version."),
		mcp.WithString("source_id", mcp.Required(), mcp.Description("Registered Git skill source ID")),
		mcp.WithString("ref", mcp.Description("Optional branch, tag, or commit; defaults to source default_ref or repository default branch")),
		mcp.WithBoolean("activate", mcp.Description("Activate the synced version (default true)")),
		mcp.WithBoolean("index", mcp.Description("Index the active version (default true)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		arguments := req.GetArguments()
		result, err := svc.SyncGitSource(ctx, owner, mcpauth.StrArg(arguments, "source_id"), SyncGitSourceRequest{
			Ref:      mcpauth.StrArg(arguments, "ref"),
			Activate: mcpauth.BoolPtrArg(arguments, "activate"),
			Index:    mcpauth.BoolPtrArg(arguments, "index"),
		})
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(result)
	})

	// skill_index_active
	s.AddTool(mcp.NewTool("skill_index_active",
		mcp.WithDescription("Index active skill versions into the retrieval store. Optionally restrict to one skill_name."),
		mcp.WithString("skill_name", mcp.Description("Optional skill name to reindex")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		result, err := svc.IndexActiveVersions(ctx, owner, mcpauth.StrArg(req.GetArguments(), "skill_name"))
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(result)
	})

	// skill_catalog_list
	s.AddTool(mcp.NewTool("skill_catalog_list",
		mcp.WithDescription("List active skill L0 catalog cards with stable pagination. Cards contain compiled routing metadata but no instruction or resource content."),
		mcp.WithString("query", mcp.Description("Optional name, description, trigger, or capability filter")),
		mcp.WithNumber("limit", mcp.Description("Page size (default 20, max 100)")),
		mcp.WithNumber("offset", mcp.Description("Non-negative page offset")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		limit, offset, paginationErr := strictMCPPagination(args)
		if paginationErr != nil {
			return mcpauth.ErrResult(paginationErr.Error()), nil
		}
		response, err := svc.ListCatalog(ctx, owner.Account(), SkillCatalogListParams{
			Query:  mcpauth.StrArg(args, "query"),
			Limit:  limit,
			Offset: offset,
		})
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(response)
	})

	// skill_compile
	s.AddTool(mcp.NewTool("skill_compile",
		mcp.WithDescription("Compile or recompile a skill version catalog artifact. Omit version_id to backfill all active versions."),
		mcp.WithString("version_id", mcp.Description("Optional skill version ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		response, err := svc.Compile(ctx, owner.Account(), mcpauth.StrArg(req.GetArguments(), "version_id"))
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(response)
	})

	// skill_version_instructions
	s.AddTool(mcp.NewTool("skill_version_instructions",
		mcp.WithDescription("Load L1 instructions for one account-owned skill version."),
		mcp.WithString("version_id", mcp.Required(), mcp.Description("Skill version ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		response, err := svc.GetInstructions(ctx, owner.Account(), mcpauth.StrArg(req.GetArguments(), "version_id"))
		if err != nil {
			return mcpauth.ErrResult("not found"), nil
		}
		return mcpauth.JSONResult(response)
	})

	// skill_version_resources
	s.AddTool(mcp.NewTool("skill_version_resources",
		mcp.WithDescription("Load the L2 resource manifest for one account-owned skill version. The manifest never includes resource content."),
		mcp.WithString("version_id", mcp.Required(), mcp.Description("Skill version ID")),
		mcp.WithNumber("limit", mcp.Description("Page size (default 20, max 100)")),
		mcp.WithNumber("offset", mcp.Description("Non-negative page offset")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		limit, offset, paginationErr := strictMCPPagination(args)
		if paginationErr != nil {
			return mcpauth.ErrResult(paginationErr.Error()), nil
		}
		response, err := svc.GetResources(ctx, owner.Account(), mcpauth.StrArg(args, "version_id"), SkillResourceListParams{Limit: limit, Offset: offset})
		if err != nil {
			return mcpauth.ErrResult("not found"), nil
		}
		return mcpauth.JSONResult(response)
	})

	// skill_resource_get
	s.AddTool(mcp.NewTool("skill_resource_get",
		mcp.WithDescription("Load one selected text resource using an account-scoped version_id and file_id pair."),
		mcp.WithString("version_id", mcp.Required(), mcp.Description("Skill version ID")),
		mcp.WithString("file_id", mcp.Required(), mcp.Description("Resource file ID from skill_version_resources")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		response, err := svc.GetResource(ctx, owner.Account(), mcpauth.StrArg(args, "version_id"), mcpauth.StrArg(args, "file_id"))
		if err != nil {
			return mcpauth.ErrResult("not found"), nil
		}
		return mcpauth.JSONResult(response)
	})

	// skill_logs_list
	s.AddTool(mcp.NewTool("skill_logs_list",
		mcp.WithDescription("List skill execution logs. Filter by skill_name, agent_id, or outcome."),
		mcp.WithString("skill_name", mcp.Description("Filter by skill name")),
		mcp.WithString("agent_id", mcp.Description("Filter by agent")),
		mcp.WithString("outcome", mcp.Description("Filter by outcome: success/failure/partial/user_corrected")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 20)")),
		mcp.WithNumber("offset", mcp.Description("Offset for pagination")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		params := LogListParams{
			SkillName: mcpauth.StrArg(args, "skill_name"),
			AgentID:   mcpauth.StrArg(args, "agent_id"),
			Outcome:   mcpauth.StrArg(args, "outcome"),
			Limit:     mcpauth.IntArg(args, "limit"),
			Offset:    mcpauth.IntArg(args, "offset"),
		}
		list, err := svc.ListLogs(ctx, owner.Account(), params)
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(list)
	})

	// skill_quality_run
	s.AddTool(mcp.NewTool("skill_quality_run",
		mcp.WithDescription("Run offline deterministic package lint, platform contract checks, same-skill release comparison, and version-bound telemetry snapshot. This does not publish, activate, index, call providers, or produce a composite score."),
		mcp.WithString("version_id", mcp.Required(), mcp.Description("Account-owned skill version ID")),
		mcp.WithString("baseline_version_id", mcp.Description("Optional account-owned baseline version ID for the same skill; defaults to the previous release")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		run, err := svc.RunQuality(ctx, owner.Account(), mcpauth.StrArg(args, "version_id"), CreateQualityRunRequest{
			BaselineVersionID: mcpauth.StrArg(args, "baseline_version_id"),
		})
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(run)
	})

	// skill_quality_get
	s.AddTool(mcp.NewTool("skill_quality_get",
		mcp.WithDescription("Get one account-scoped deterministic quality run by run_id."),
		mcp.WithString("run_id", mcp.Required(), mcp.Description("Quality run ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		run, err := svc.GetQualityRun(ctx, owner.Account(), mcpauth.StrArg(req.GetArguments(), "run_id"))
		if err != nil {
			return mcpauth.ErrResult("not found"), nil
		}
		return mcpauth.JSONResult(run)
	})

	return server.NewStreamableHTTPServer(s, server.WithHTTPContextFunc(mcpauth.HTTPContextFunc(authSvc)))
}

func strictMCPPagination(args map[string]interface{}) (int, int, error) {
	limit, err := strictMCPInteger(args, "limit", 20, 1, 100)
	if err != nil {
		return 0, 0, err
	}
	offset, err := strictMCPInteger(args, "offset", 0, 0, math.MaxInt)
	if err != nil {
		return 0, 0, err
	}
	return limit, offset, nil
}

func strictMCPInteger(args map[string]interface{}, key string, fallback, minimum, maximum int) (int, error) {
	raw, present := args[key]
	if !present {
		return fallback, nil
	}
	number, ok := raw.(float64)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number || number < float64(minimum) || number > float64(maximum) {
		if key == "limit" {
			return 0, fmt.Errorf("limit must be an integer between 1 and 100")
		}
		return 0, fmt.Errorf("offset must be a non-negative integer")
	}
	return int(number), nil
}
