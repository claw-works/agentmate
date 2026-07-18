package skills

import (
	"context"
	"net/http"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/wellxie/agentmate/internal/auth"
	"github.com/wellxie/agentmate/internal/mcpauth"
)

var toolScopes = map[string]string{
	"skill_log_add":            "skills:rw",
	"skill_version_publish":    "skills:rw",
	"skill_version_get_active": "skills:r",
	"skill_stats":              "skills:r",
	"skill_signals":            "skills:r",
	"skill_logs_list":          "skills:r",
	"skill_search":             "skills:r",
	"skill_source_sync":        "skills:rw",
	"skill_index_active":       "skills:rw",
}

func NewMCPServer(svc *Service, authSvc *auth.Service) http.Handler {
	s := server.NewMCPServer("agentmate-skills", "0.1.0", server.WithToolHandlerMiddleware(mcpauth.ScopeMiddleware(toolScopes)))

	// skill_log_add
	s.AddTool(mcp.NewTool("skill_log_add",
		mcp.WithDescription("Record a skill execution log entry. Call after every skill execution to track performance and learning signals. outcome must be: success/failure/partial/user_corrected. Set was_triggered=false for silent-bypass cases where skill was never invoked."),
		mcp.WithString("skill_name", mcp.Required(), mcp.Description("Skill name, e.g. 'agentmate', 'sa-weekly-kpi'")),
		mcp.WithString("outcome", mcp.Required(), mcp.Description("success/failure/partial/user_corrected")),
		mcp.WithString("agent_id", mcp.Description("Agent that executed the skill")),
		mcp.WithString("skill_version", mcp.Description("Skill version, e.g. 'v1'")),
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

	return server.NewStreamableHTTPServer(s, server.WithHTTPContextFunc(mcpauth.HTTPContextFunc(authSvc)))
}
