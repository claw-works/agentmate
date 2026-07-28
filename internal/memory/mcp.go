package memory

import (
	"context"
	"net/http"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/wellxie/agentmate/internal/auth"
	"github.com/wellxie/agentmate/internal/mcpauth"
)

var toolScopes = map[string]string{
	"memory_record": "memory:rw",
	"memory_store":  "memory:rw",
	"memory_search": "memory:r",
	"memory_get":    "memory:r",

	"memory_timeline":    "memory:r",
	"memory_attribution": "memory:r",
}

func NewMCPServer(svc *Service, authSvc *auth.Service) http.Handler {
	s := server.NewMCPServer(
		"agentmate-memory",
		"0.1.0",
		server.WithToolHandlerMiddleware(mcpauth.ScopeMiddleware(toolScopes)),
	)

	s.AddTool(mcp.NewTool("memory_record",
		mcp.WithDescription("Append an immutable event to AgentMate's memory journal."),
		mcp.WithString("event_type", mcp.Required(), mcp.Enum(
			"goal", "observation", "action", "decision", "issue",
			"attempt", "outcome", "correction", "checkpoint", "note",
		), mcp.Description("Memory event type")),
		mcp.WithString("idempotency_key", mcp.Required(), mcp.Description("Stable retry key unique within the account")),
		mcp.WithObject("payload", mcp.Description("Structured event payload")),
		mcp.WithString("scope_type", mcp.Enum("global", "project", "repository", "agent", "session"), mcp.Description("Memory scope type")),
		mcp.WithString("scope_key", mcp.Description("Scope identifier; required for non-global scopes")),
		mcp.WithString("session_id", mcp.Description("External session identifier")),
		mcp.WithInteger("sequence_no", mcp.Min(0), mcp.Description("Monotonic sequence number within the session")),
		mcp.WithString("source_type", mcp.Description("Optional source resource type")),
		mcp.WithString("source_id", mcp.Description("Optional source resource ID")),
		mcp.WithString("skill_version_id", mcp.Description("Optional: the skill version whose execution produced this event. Pass it whenever the event comes from running a skill; session_id alone cannot tell which skill in a multi-skill session produced the event. Part of the idempotency hash.")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		record := RecordEventRequest{
			EventType:      mcpauth.StrArg(args, "event_type"),
			IdempotencyKey: mcpauth.StrArg(args, "idempotency_key"),
			Payload:        objectArg(args, "payload"),
			ScopeType:      mcpauth.StrArg(args, "scope_type"),
			ScopeKey:       mcpauth.StrArg(args, "scope_key"),
			SessionID:      mcpauth.StrArg(args, "session_id"),
			SourceType:     mcpauth.StrArg(args, "source_type"),
			SourceID:       mcpauth.StrArg(args, "source_id"),
			SkillVersionID: mcpauth.StrArg(args, "skill_version_id"),
		}
		if value, ok := args["sequence_no"].(float64); ok {
			sequence := int64(value)
			record.SequenceNo = &sequence
		}
		event, created, err := svc.RecordEvent(ctx, owner, record)
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(map[string]any{"event": event, "created": created})
	})

	s.AddTool(mcp.NewTool("memory_store",
		mcp.WithDescription("Store an evidence-backed durable memory and index it for retrieval."),
		mcp.WithString("memory_type", mcp.Required(), mcp.Enum("semantic", "episodic", "procedural"), mcp.Description("Durable memory type")),
		mcp.WithString("content", mcp.Required(), mcp.Description("Durable memory content")),
		mcp.WithString("title", mcp.Description("Short memory title")),
		mcp.WithString("summary", mcp.Description("Concise memory summary")),
		mcp.WithString("scope_type", mcp.Enum("global", "project", "repository", "agent", "session"), mcp.Description("Memory scope type")),
		mcp.WithString("scope_key", mcp.Description("Scope identifier; required for non-global scopes")),
		mcp.WithNumber("confidence", mcp.Min(0), mcp.Max(1), mcp.Description("Confidence from 0 to 1")),
		mcp.WithNumber("importance", mcp.Min(0), mcp.Max(1), mcp.Description("Importance from 0 to 1")),
		mcp.WithString("source_event_id", mcp.Description("Optional memory event ID used as evidence")),
		mcp.WithObject("metadata", mcp.Description("Structured memory metadata")),
		mcp.WithArray("evidence",
			mcp.Description("Evidence objects with source_type, source_id, excerpt, and optional metadata"),
			mcp.Items(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"source_type": map[string]any{"type": "string"},
					"source_id":   map[string]any{"type": "string"},
					"excerpt":     map[string]any{"type": "string"},
					"metadata":    map[string]any{"type": "object"},
				},
				"required": []string{"source_type", "source_id"},
			}),
		),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		store := CreateEntryRequest{
			MemoryType: mcpauth.StrArg(args, "memory_type"),
			Content:    mcpauth.StrArg(args, "content"),
			Title:      mcpauth.StrArg(args, "title"),
			Summary:    mcpauth.StrArg(args, "summary"),
			ScopeType:  mcpauth.StrArg(args, "scope_type"),
			ScopeKey:   mcpauth.StrArg(args, "scope_key"),
			Confidence: mcpauth.FloatPtrArg(args, "confidence"),
			Importance: mcpauth.FloatPtrArg(args, "importance"),
			Metadata:   objectArg(args, "metadata"),
			Evidence:   evidenceArg(args, "evidence"),
		}
		if value := mcpauth.StrArg(args, "source_event_id"); value != "" {
			store.SourceEventID = &value
		}
		entry, err := svc.CreateEntry(ctx, owner, store)
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(entry)
	})

	s.AddTool(mcp.NewTool("memory_search",
		mcp.WithDescription("Hybrid lexical and semantic search over active durable memories."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Task, fact, error, or experience to recall")),
		mcp.WithInteger("top_k", mcp.Min(1), mcp.Max(20), mcp.Description("Max results (default 8, max 20)")),
		mcp.WithString("scope_type", mcp.Enum("global", "project", "repository", "agent", "session"), mcp.Description("Optional scope filter")),
		mcp.WithString("scope_key", mcp.Description("Optional scope identifier")),
		mcp.WithString("memory_type", mcp.Enum("semantic", "episodic", "procedural"), mcp.Description("Optional durable memory type")),
		mcp.WithString("status", mcp.Enum("pending", "active", "superseded", "invalidated", "archived", "expired"), mcp.Description("Memory status; defaults to active")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		result, err := svc.SearchEntries(ctx, owner, SearchEntriesRequest{
			Query:      mcpauth.StrArg(args, "query"),
			TopK:       mcpauth.IntArg(args, "top_k"),
			ScopeType:  mcpauth.StrArg(args, "scope_type"),
			ScopeKey:   mcpauth.StrArg(args, "scope_key"),
			MemoryType: mcpauth.StrArg(args, "memory_type"),
			Status:     mcpauth.StrArg(args, "status"),
		})
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(result)
	})

	s.AddTool(mcp.NewTool("memory_get",
		mcp.WithDescription("Get a durable memory and all of its evidence by ID."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Memory entry ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		entry, err := svc.GetEntry(ctx, owner.Account(), mcpauth.StrArg(req.GetArguments(), "id"))
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(entry)
	})

	s.AddTool(mcp.NewTool("memory_timeline",
		mcp.WithDescription("Time-ordered merge of skill executions and memory events for one session or one skill version. Use it to see what ran and what it recorded, in order. Requires session_id or skill_version_id: an unfiltered timeline is a data dump, not attribution. The response reports skill_log_count, memory_event_count, unattributed_count and truncated so the coverage of an attribution conclusion is explicit."),
		mcp.WithString("session_id", mcp.Description("External session identifier")),
		mcp.WithString("skill_version_id", mcp.Description("Skill version ID; returns everything this version touched")),
		mcp.WithNumber("limit", mcp.Description("Max merged items (default 200, max 500)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		response, err := svc.SessionTimeline(ctx, owner, SessionTimelineParams{
			SessionID:      mcpauth.StrArg(args, "session_id"),
			SkillVersionID: mcpauth.StrArg(args, "skill_version_id"),
			Limit:          mcpauth.IntArg(args, "limit"),
		})
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(response)
	})

	s.AddTool(mcp.NewTool("memory_attribution",
		mcp.WithDescription("Resolve which skill execution produced a durable memory. Walks entry -> source event -> skill version and reports how far the chain resolved via resolution: skill_version, session_only, event_only, or none. Includes the surrounding session timeline when a session is known."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Memory entry ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		response, err := svc.EntryAttribution(ctx, owner, mcpauth.StrArg(req.GetArguments(), "id"))
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(response)
	})

	return server.NewStreamableHTTPServer(s, server.WithHTTPContextFunc(mcpauth.HTTPContextFunc(authSvc)))
}

func objectArg(args map[string]any, key string) map[string]any {
	value, _ := args[key].(map[string]any)
	return value
}

func evidenceArg(args map[string]any, key string) []EvidenceInput {
	raw, ok := args[key].([]any)
	if !ok {
		return nil
	}
	items := make([]EvidenceInput, 0, len(raw))
	for _, value := range raw {
		object, ok := value.(map[string]any)
		if !ok {
			continue
		}
		items = append(items, EvidenceInput{
			SourceType: stringValue(object["source_type"]),
			SourceID:   stringValue(object["source_id"]),
			Excerpt:    stringValue(object["excerpt"]),
			Metadata:   mapValue(object["metadata"]),
		})
	}
	return items
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func mapValue(value any) map[string]any {
	object, _ := value.(map[string]any)
	return object
}
