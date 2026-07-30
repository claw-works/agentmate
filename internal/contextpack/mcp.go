package contextpack

import (
	"context"
	"net/http"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/claw-works/agentmate/internal/auth"
	"github.com/claw-works/agentmate/internal/mcpauth"
)

// context_pack spans five domains, so no single scope can gate it. The tool is
// admitted on one scope and each layer is then authorised individually inside
// the service: a caller holding only some read scopes receives those layers and
// an explicit note on the rest.
//
// memory:r is the entry requirement because the task layer's session slice is
// the one piece of context that is neither the caller's own input nor reachable
// through a narrower tool.
var toolScopes = map[string]string{
	"context_pack": "memory:r",
}

func NewMCPServer(svc *Service, authSvc *auth.Service) http.Handler {
	s := server.NewMCPServer(
		"agentmate-context",
		"0.1.0",
		server.WithToolHandlerMiddleware(mcpauth.ScopeMiddleware(toolScopes)),
	)

	s.AddTool(mcp.NewTool("context_pack",
		mcp.WithDescription("Assemble the minimal execution context for a task in one call: [SKILL] instructions, [KNOWLEDGE] evidence with citations, [MEMORY] relevant experience, [FACTS] live todos and notes, [TASK] the goal plus recent session activity. Every item carries a source label and a traceable ref, so authority can be told apart and claims traced back. The pack is budgeted in characters: each layer gets a bounded share, oversized content is truncated at a paragraph or sentence boundary and flagged, and the response reports char_budget, chars_used, dropped and truncated per layer. Layers the caller lacks scope for return empty with a note instead of failing the call."),
		mcp.WithString("task", mcp.Required(), mcp.Description("Goal statement. Also the default retrieval query, so a vague task yields a vague pack.")),
		mcp.WithString("query", mcp.Description("Override the retrieval query; useful when the task reads as a long imperative and makes a poor search string")),
		mcp.WithString("skill_name", mcp.Description("Pin the skill instead of selecting one by retrieval")),
		mcp.WithString("session_id", mcp.Description("Session to draw recent activity from for the task layer")),
		mcp.WithString("knowledge_domain", mcp.Description("Restrict evidence to one knowledge domain")),
		mcp.WithArray("knowledge_source_ids", mcp.Description("Restrict evidence to specific knowledge sources; intersects with knowledge_domain, never widens")),
		mcp.WithString("memory_scope_type", mcp.Enum("global", "project", "repository", "agent", "session"), mcp.Description("Memory scope type filter")),
		mcp.WithString("memory_scope_key", mcp.Description("Memory scope identifier; required for non-global scopes")),
		mcp.WithNumber("max_chars", mcp.Description("Total character budget (default 12000, min 500, max 200000). Characters rather than tokens: token cost is model specific, characters are exact and explainable.")),
		mcp.WithNumber("top_k", mcp.Description("Max items per retrieval-backed layer before budgeting (default 5, max 20)")),
		mcp.WithArray("layers", mcp.Description("Restrict to named layers: SKILL, KNOWLEDGE, MEMORY, FACTS, TASK. Omitted layers hand their budget share to the rest.")),
		mcp.WithBoolean("render", mcp.Description("Also return the labelled plain-text pack, ready to paste into a prompt (default false)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		response, err := svc.Pack(ctx, owner, mcpauth.ScopesFromContext(ctx), PackRequest{
			Task:               mcpauth.StrArg(args, "task"),
			Query:              mcpauth.StrArg(args, "query"),
			SkillName:          mcpauth.StrArg(args, "skill_name"),
			SessionID:          mcpauth.StrArg(args, "session_id"),
			KnowledgeDomain:    mcpauth.StrArg(args, "knowledge_domain"),
			KnowledgeSourceIDs: mcpauth.StrSliceArg(args, "knowledge_source_ids"),
			MemoryScopeType:    mcpauth.StrArg(args, "memory_scope_type"),
			MemoryScopeKey:     mcpauth.StrArg(args, "memory_scope_key"),
			MaxChars:           mcpauth.IntArg(args, "max_chars"),
			TopK:               mcpauth.IntArg(args, "top_k"),
			Layers:             mcpauth.StrSliceArg(args, "layers"),
			Render:             mcpauth.BoolArg(args, "render"),
		})
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(response)
	})

	return server.NewStreamableHTTPServer(s, server.WithHTTPContextFunc(mcpauth.HTTPContextFunc(authSvc)))
}
