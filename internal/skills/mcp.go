package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/wellxie/agentmate/internal/auth"
)

type userIDKeyType struct{}

var skillUserIDKey = userIDKeyType{}

func NewMCPServer(svc *Service, authSvc *auth.Service) http.Handler {
	s := server.NewMCPServer("agentmate-skills", "0.1.0")

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
		userID, ok := skillUserIDFromCtx(ctx)
		if !ok {
			return skillErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		r := CreateLogRequest{
			SkillName:      skillStrArg(args, "skill_name"),
			Outcome:        skillStrArg(args, "outcome"),
			AgentID:        skillStrArg(args, "agent_id"),
			SkillVersion:   skillStrArg(args, "skill_version"),
			SessionID:      skillStrArg(args, "session_id"),
			TriggerText:    skillStrArg(args, "trigger_text"),
			FailureReason:  skillStrArg(args, "failure_reason"),
			UserCorrection: skillStrArg(args, "user_correction"),
		}
		if v, ok := args["was_triggered"]; ok {
			if b, ok := v.(bool); ok {
				r.WasTriggered = &b
			}
		}
		if v, ok := args["duration_ms"]; ok {
			if f, ok := v.(float64); ok {
				d := int(f)
				r.DurationMs = &d
			}
		}
		l, err := svc.CreateLog(ctx, userID, r)
		if err != nil {
			return skillErrResult(err.Error()), nil
		}
		return skillJsonResult(l)
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
		userID, ok := skillUserIDFromCtx(ctx)
		if !ok {
			return skillErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		r := CreateVersionRequest{
			SkillName:     skillStrArg(args, "skill_name"),
			Version:       skillStrArg(args, "version"),
			Content:       skillStrArg(args, "content"),
			AgentID:       skillStrArg(args, "agent_id"),
			ChangeSummary: skillStrArg(args, "change_summary"),
		}
		if v, ok := args["eval_pass_rate"]; ok {
			if f, ok := v.(float64); ok {
				r.EvalPassRate = &f
			}
		}
		if v, ok := args["activate"]; ok {
			if b, ok := v.(bool); ok {
				r.Activate = b
			}
		}
		ver, err := svc.CreateVersion(ctx, userID, r)
		if err != nil {
			return skillErrResult(err.Error()), nil
		}
		return skillJsonResult(ver)
	})

	// skill_version_get_active
	s.AddTool(mcp.NewTool("skill_version_get_active",
		mcp.WithDescription("Get the currently active version of a skill. Returns full content."),
		mcp.WithString("skill_name", mcp.Required(), mcp.Description("Skill name")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := skillUserIDFromCtx(ctx)
		if !ok {
			return skillErrResult("unauthorized"), nil
		}
		v, err := svc.GetActiveVersion(ctx, userID, skillStrArg(req.GetArguments(), "skill_name"))
		if err != nil {
			return skillErrResult("not found"), nil
		}
		return skillJsonResult(v)
	})

	// skill_stats
	s.AddTool(mcp.NewTool("skill_stats",
		mcp.WithDescription("Get aggregated performance stats for a skill: total runs, success/failure/correction rates."),
		mcp.WithString("skill_name", mcp.Required(), mcp.Description("Skill name")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := skillUserIDFromCtx(ctx)
		if !ok {
			return skillErrResult("unauthorized"), nil
		}
		stats, err := svc.GetSkillStats(ctx, userID, skillStrArg(req.GetArguments(), "skill_name"))
		if err != nil {
			return skillErrResult(err.Error()), nil
		}
		return skillJsonResult(stats)
	})

	// skill_signals
	s.AddTool(mcp.NewTool("skill_signals",
		mcp.WithDescription("Get recent failure/correction signals for a skill. Use as learning input for skill evolution."),
		mcp.WithString("skill_name", mcp.Required(), mcp.Description("Skill name")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 10, max 50)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := skillUserIDFromCtx(ctx)
		if !ok {
			return skillErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		limit := 10
		if v, ok := args["limit"]; ok {
			if f, ok := v.(float64); ok {
				limit = int(f)
			}
		}
		signals, err := svc.SkillSignals(ctx, userID, skillStrArg(args, "skill_name"), limit)
		if err != nil {
			return skillErrResult(err.Error()), nil
		}
		return skillJsonResult(signals)
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
		userID, ok := skillUserIDFromCtx(ctx)
		if !ok {
			return skillErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		params := LogListParams{
			SkillName: skillStrArg(args, "skill_name"),
			AgentID:   skillStrArg(args, "agent_id"),
			Outcome:   skillStrArg(args, "outcome"),
			Limit:     skillIntArg(args, "limit"),
			Offset:    skillIntArg(args, "offset"),
		}
		list, err := svc.ListLogs(ctx, userID, params)
		if err != nil {
			return skillErrResult(err.Error()), nil
		}
		return skillJsonResult(list)
	})

	// Build HTTP handler with auth
	httpSrv := server.NewStreamableHTTPServer(s,
		server.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			apiKey := r.Header.Get("X-Api-Key")
			if apiKey == "" {
				apiKey = r.URL.Query().Get("api_key")
			}
			if apiKey == "" {
				if bearer := r.Header.Get("Authorization"); strings.HasPrefix(bearer, "Bearer ") {
					apiKey = strings.TrimPrefix(bearer, "Bearer ")
				}
			}
			if apiKey == "" {
				return ctx
			}
			ak, err := authSvc.ValidateAPIKey(ctx, apiKey)
			if err != nil {
				return ctx
			}
			return context.WithValue(ctx, skillUserIDKey, ak.UserID)
		}),
	)

	return httpSrv
}

func skillUserIDFromCtx(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(skillUserIDKey).(string)
	return id, ok && id != ""
}

func skillStrArg(args map[string]interface{}, key string) string {
	v, _ := args[key].(string)
	return v
}

func skillIntArg(args map[string]interface{}, key string) int {
	v, _ := args[key].(float64)
	return int(v)
}

func skillErrResult(msg string) *mcp.CallToolResult {
	r := mcp.NewToolResultText(msg)
	r.IsError = true
	return r
}

func skillJsonResult(v interface{}) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return skillErrResult(fmt.Sprintf("json marshal: %v", err)), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}
