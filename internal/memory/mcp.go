package memory

import (
	"context"
	"net/http"

	"github.com/claw-works/agentmate/internal/auth"
	"github.com/claw-works/agentmate/internal/mcpauth"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var toolScopes = map[string]string{
	"memory_record": "memory:rw",
	"memory_store":  "memory:rw",
	"memory_search": "memory:r",
	"memory_get":    "memory:r",

	"memory_timeline":    "memory:r",
	"memory_attribution": "memory:r",

	"memory_supersede":       "memory:rw",
	"memory_feedback":        "memory:rw",
	"memory_feedback_list":   "memory:r",
	"memory_checkpoint_save": "memory:rw",
	"memory_resume":          "memory:r",
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

	s.AddTool(mcp.NewTool("memory_supersede",
		mcp.WithDescription("Record that one durable memory replaces another. The replaced entry moves to status superseded, its validity window closes at the supersede time, and its retrieval projection is removed so it stops consuming search candidates. Chains are allowed (C replaces B which replaced A); cycles are rejected. Re-running the same supersede is idempotent, while pointing a replaced entry at a different replacement is a conflict."),
		mcp.WithString("superseding_id", mcp.Required(), mcp.Description("The entry that takes over")),
		mcp.WithString("superseded_id", mcp.Required(), mcp.Description("The entry being replaced")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		response, err := svc.SupersedeEntry(ctx, owner, SupersedeRequest{
			SupersedingID: mcpauth.StrArg(args, "superseding_id"),
			SupersededID:  mcpauth.StrArg(args, "superseded_id"),
		})
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(response)
	})

	s.AddTool(mcp.NewTool("memory_feedback",
		mcp.WithDescription("Report whether a durable memory actually helped. Signals nudge search ranking by a small bounded amount — feedback is a weak, biased signal, so it breaks ties and demotes repeatedly harmful memories rather than overriding semantic relevance. Pass session_id and skill_version_id so the signal can be attributed to the execution that produced it; without them it only supports coarse trends. One signal of each kind per memory per session: a retry does not count twice."),
		mcp.WithString("memory_id", mcp.Required(), mcp.Description("Durable memory ID")),
		mcp.WithString("signal", mcp.Required(), mcp.Enum("useful", "harmful"), mcp.Description("Whether the memory helped or misled")),
		mcp.WithString("reason", mcp.Description("Why, in the reporter's words (max 2000 chars)")),
		mcp.WithString("session_id", mcp.Description("Attribution anchor: the session the signal came from")),
		mcp.WithString("skill_version_id", mcp.Description("Attribution anchor: the skill version that used the memory")),
		mcp.WithObject("metadata", mcp.Description("Optional structured detail")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		response, err := svc.RecordFeedback(ctx, owner, FeedbackRequest{
			MemoryID:       mcpauth.StrArg(args, "memory_id"),
			Signal:         mcpauth.StrArg(args, "signal"),
			Reason:         mcpauth.StrArg(args, "reason"),
			SessionID:      mcpauth.StrArg(args, "session_id"),
			SkillVersionID: mcpauth.StrArg(args, "skill_version_id"),
			Metadata:       objectArg(args, "metadata"),
		})
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(response)
	})

	s.AddTool(mcp.NewTool("memory_feedback_list",
		mcp.WithDescription("List the usefulness signals recorded for one durable memory, newest first. The log is the durable record; the useful_count and harmful_count on the entry are a projection of it."),
		mcp.WithString("memory_id", mcp.Required(), mcp.Description("Durable memory ID")),
		mcp.WithNumber("limit", mcp.Description("Max signals (default 50, max 200)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		items, err := svc.ListFeedback(ctx, owner, mcpauth.StrArg(args, "memory_id"), mcpauth.IntArg(args, "limit"))
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(map[string]any{"items": items, "total": len(items)})
	})

	s.AddTool(mcp.NewTool("memory_checkpoint_save",
		mcp.WithDescription("Save a resumable snapshot of session intent: the goal, what is done, what is next, and open questions. Stored as a checkpoint event on the journal, so it inherits immutability, ordering, idempotency and skill attribution. Saving unchanged state is a no-op rather than a duplicate snapshot, because the default idempotency key is derived from the content."),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("Session this checkpoint belongs to")),
		mcp.WithString("goal", mcp.Required(), mcp.Description("What the session is trying to achieve; a checkpoint without it cannot be resumed from")),
		mcp.WithArray("done", mcp.Description("Steps already completed")),
		mcp.WithArray("next", mcp.Description("Steps planned next")),
		mcp.WithArray("open", mcp.Description("Unresolved questions or decisions")),
		mcp.WithString("notes", mcp.Description("Free-form context worth carrying over")),
		mcp.WithString("label", mcp.Description("Short human label for this checkpoint")),
		mcp.WithString("scope_type", mcp.Enum("global", "project", "repository", "agent", "session"), mcp.Description("Memory scope type")),
		mcp.WithString("scope_key", mcp.Description("Scope identifier; required for non-global scopes")),
		mcp.WithString("skill_version_id", mcp.Description("Skill version that saved the checkpoint")),
		mcp.WithString("idempotency_key", mcp.Description("Override the content-derived key")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		response, err := svc.SaveCheckpoint(ctx, owner, SaveCheckpointRequest{
			SessionID:      mcpauth.StrArg(args, "session_id"),
			ScopeType:      mcpauth.StrArg(args, "scope_type"),
			ScopeKey:       mcpauth.StrArg(args, "scope_key"),
			Label:          mcpauth.StrArg(args, "label"),
			Goal:           mcpauth.StrArg(args, "goal"),
			Done:           mcpauth.StrSliceArg(args, "done"),
			Next:           mcpauth.StrSliceArg(args, "next"),
			Open:           mcpauth.StrSliceArg(args, "open"),
			Notes:          mcpauth.StrArg(args, "notes"),
			SkillVersionID: mcpauth.StrArg(args, "skill_version_id"),
			IdempotencyKey: mcpauth.StrArg(args, "idempotency_key"),
		})
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(response)
	})

	s.AddTool(mcp.NewTool("memory_resume",
		mcp.WithDescription("Restore a session: returns its latest checkpoint plus everything recorded after it. The tail matters — a session is interrupted after its last checkpoint, so that activity is exactly the state the snapshot is missing. resolution is \"checkpoint\", \"journal_only\" (activity but never checkpointed) or \"empty\", so a fresh session can be told from an unsaved one."),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("Session to resume")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		response, err := svc.Resume(ctx, owner, mcpauth.StrArg(req.GetArguments(), "session_id"))
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
