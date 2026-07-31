package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"

	"github.com/claw-works/agentmate/internal/auth"
	"github.com/claw-works/agentmate/internal/mcpauth"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var toolScopes = map[string]string{
	"knowledge_sources_list":   "knowledge:r",
	"knowledge_source_sync":    "knowledge:rw",
	"knowledge_documents_list": "knowledge:r",
	"knowledge_document_get":   "knowledge:r",
	"knowledge_catalog_list":   "knowledge:r",
	"knowledge_search":         "knowledge:r",
	// K4 discovery spans two domains. The middleware gates the knowledge half; the
	// skills:r half is enforced inside the tool handler because ScopeMiddleware
	// carries one scope per tool (context pack sets the same precedent).
	"knowledge_discover": "knowledge:r",
	// K4 resolution runs. Recording writes execution evidence and validates the
	// requirement against the compiled contract, so it additionally enforces
	// skills:r in-handler.
	"knowledge_resolution_record": "knowledge:rw",
	"knowledge_resolutions_list":  "knowledge:r",
	"knowledge_resolution_get":    "knowledge:r",
	"knowledge_index_active":      "knowledge:rw",
	"knowledge_document_links":    "knowledge:r",

	// K3 wiki layer.
	"knowledge_compile":        "knowledge:rw",
	"knowledge_builds_list":    "knowledge:r",
	"knowledge_build_get":      "knowledge:r",
	"knowledge_build_pages":    "knowledge:r",
	"knowledge_page_get":       "knowledge:r",
	"knowledge_build_diff":     "knowledge:r",
	"knowledge_build_events":   "knowledge:r",
	"knowledge_build_activate": "knowledge:rw",
	"knowledge_queue_stats":    "knowledge:r",
	"knowledge_wiki_search":    "knowledge:r",
	"knowledge_wiki_index":     "knowledge:rw",
	"knowledge_wiki_status":    "knowledge:r",

	// K3.7 lint. The lint run is write scope because it records a run row, not because
	// it changes a wiki: it cannot.
	"knowledge_wiki_lint":         "knowledge:rw",
	"knowledge_wiki_lint_runs":    "knowledge:r",
	"knowledge_wiki_lint_run_get": "knowledge:r",

	// K3.8 review. Running a review writes the verdict and spends money on a model, so it
	// is write scope even though it cannot change a page.
	"knowledge_build_review":     "knowledge:rw",
	"knowledge_build_review_get": "knowledge:r",

	// K3.9 validation. Reporting a signal records evidence; it gates nothing.
	"knowledge_validation_report":         "knowledge:rw",
	"knowledge_validation_summary":        "knowledge:r",
	"knowledge_validation_signals":        "knowledge:r",
	"knowledge_validation_skill_patterns": "knowledge:r",
}

func NewMCPServer(svc *Service, authSvc *auth.Service) http.Handler {
	s := server.NewMCPServer("agentmate-knowledge", "0.1.0", server.WithToolHandlerMiddleware(mcpauth.ScopeMiddleware(toolScopes)))

	// knowledge_sources_list
	s.AddTool(mcp.NewTool("knowledge_sources_list",
		mcp.WithDescription("List registered knowledge sources for the account. Sources are Git or local package registrations; each carries its active immutable revision pointer."),
		mcp.WithString("type", mcp.Description("Optional filter: git or local")),
		mcp.WithString("status", mcp.Description("Optional filter: active, disabled, or error")),
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
		sources, err := svc.ListSources(ctx, owner.Account(), KnowledgeSourceListParams{
			Type:   mcpauth.StrArg(args, "type"),
			Status: mcpauth.StrArg(args, "status"),
			Limit:  limit,
			Offset: offset,
		})
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(sources)
	})

	// knowledge_source_sync
	s.AddTool(mcp.NewTool("knowledge_source_sync",
		mcp.WithDescription("Sync a public GitHub or GitLab knowledge source. Resolves a ref to an immutable commit, ingests the package (root KNOWLEDGE.yaml required), and moves the active revision pointer."),
		mcp.WithString("source_id", mcp.Required(), mcp.Description("Registered Git knowledge source ID")),
		mcp.WithString("ref", mcp.Description("Optional branch, tag, or commit; defaults to source default_ref or repository default branch")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		result, err := svc.SyncGitSource(ctx, owner, mcpauth.StrArg(args, "source_id"), SyncGitSourceRequest{
			Ref: mcpauth.StrArg(args, "ref"),
		})
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(result)
	})

	// knowledge_documents_list
	s.AddTool(mcp.NewTool("knowledge_documents_list",
		mcp.WithDescription("List document metadata for one immutable knowledge source revision. Returns path, hash, size, and mime metadata only — never content bodies."),
		mcp.WithString("revision_id", mcp.Required(), mcp.Description("Knowledge source revision ID")),
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
		response, err := svc.ListRevisionDocuments(ctx, owner.Account(), mcpauth.StrArg(args, "revision_id"), DocumentListParams{Limit: limit, Offset: offset})
		if err != nil {
			return mcpauth.ErrResult("not found"), nil
		}
		return mcpauth.JSONResult(response)
	})

	// knowledge_document_get
	s.AddTool(mcp.NewTool("knowledge_document_get",
		mcp.WithDescription("Load one document (including its text content snapshot when available) from an account-owned revision."),
		mcp.WithString("revision_id", mcp.Required(), mcp.Description("Knowledge source revision ID")),
		mcp.WithString("document_id", mcp.Required(), mcp.Description("Document ID from knowledge_documents_list")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		document, err := svc.GetDocument(ctx, owner.Account(), mcpauth.StrArg(args, "revision_id"), mcpauth.StrArg(args, "document_id"))
		if err != nil {
			return mcpauth.ErrResult("not found"), nil
		}
		return mcpauth.JSONResult(document)
	})

	// knowledge_catalog_list
	s.AddTool(mcp.NewTool("knowledge_catalog_list",
		mcp.WithDescription("List K0 knowledge collection cards: sources with an active revision, with manifest metadata (name, description, profile, language, citation_policy), owning domain, document count, package hash, and index status. The response also lists every domain with its collection count, so a domain can be chosen before reading individual cards."),
		mcp.WithString("query", mcp.Description("Optional case-insensitive name/description filter")),
		mcp.WithString("domain", mcp.Description("Optional exact domain filter; domains come from the package directory layout")),
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
		response, err := svc.ListCatalog(ctx, owner.Account(), KnowledgeCatalogListParams{
			Query:  mcpauth.StrArg(args, "query"),
			Domain: mcpauth.StrArg(args, "domain"),
			Limit:  limit,
			Offset: offset,
		})
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(response)
	})

	// knowledge_search
	s.AddTool(mcp.NewTool("knowledge_search",
		mcp.WithDescription("Hybrid lexical + semantic search over indexed knowledge chunks. Each hit carries document/source/revision provenance, heading path, score, snippet, and 1-hop link neighbors (metadata only). Set include_content to load full chunk bodies."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query")),
		mcp.WithNumber("top_k", mcp.Description("Max results (default 5, max 20)")),
		mcp.WithString("domain", mcp.Description("Optional domain to restrict the search; combined with source_ids it narrows further, never widens")),
		mcp.WithArray("source_ids", mcp.Description("Optional knowledge source IDs to restrict the search (max 16)")),
		mcp.WithBoolean("include_content", mcp.Description("Include full chunk bodies in hits (default false)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		response, err := svc.Search(ctx, owner, SearchKnowledgeRequest{
			Query:          mcpauth.StrArg(args, "query"),
			TopK:           mcpauth.IntArg(args, "top_k"),
			Domain:         mcpauth.StrArg(args, "domain"),
			SourceIDs:      mcpauth.StrSliceArg(args, "source_ids"),
			IncludeContent: mcpauth.BoolArg(args, "include_content"),
		})
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(response)
	})

	// knowledge_discover
	s.AddTool(mcp.NewTool("knowledge_discover",
		mcp.WithDescription("Resolve a skill version's knowledge discovery contract against the account's K0 catalog. Returns per-requirement candidate collections with matched capabilities/languages/domain and a classified status (matched, ambiguous, no_metadata_match, no_authorized_knowledge, pinned_resolved, pinned_missing) plus the contract's fallback guidance. Read-only: selection stays with the caller. Requires both knowledge:r and skills:r."),
		mcp.WithString("skill_version_id", mcp.Required(), mcp.Description("Skill version whose compiled knowledge contract drives discovery")),
		mcp.WithString("requirement_id", mcp.Description("Optional: narrow discovery to one contract requirement")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		// Second-domain check: the middleware verified knowledge:r, but the contract
		// is skills data and a knowledge-only key must not read it.
		if !auth.HasScope(&auth.APIKey{Scopes: mcpauth.ScopesFromContext(ctx)}, "skills:r") {
			return mcpauth.ErrResult("insufficient scope: skills:r"), nil
		}
		args := req.GetArguments()
		response, err := svc.DiscoverForSkill(ctx, owner, DiscoverKnowledgeRequest{
			SkillVersionID: mcpauth.StrArg(args, "skill_version_id"),
			RequirementID:  mcpauth.StrArg(args, "requirement_id"),
		})
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(response)
	})

	// knowledge_resolution_record
	s.AddTool(mcp.NewTool("knowledge_resolution_record",
		mcp.WithDescription("Freeze one runtime knowledge resolution as execution evidence: which discovery it followed (fingerprint + status), which bases were selected (verified against the account), what was retrieved and cited (references only, never bodies), and why. Selected may be empty when the skill proceeded per its fallback. Supports idempotent replay via idempotency_key; a replay with different content is a conflict. Requires knowledge:rw and skills:r."),
		mcp.WithString("skill_version_id", mcp.Required(), mcp.Description("Skill version whose contract requirement this run satisfied")),
		mcp.WithString("requirement_id", mcp.Required(), mcp.Description("Contract requirement id this resolution answers")),
		mcp.WithString("discovery_fingerprint", mcp.Required(), mcp.Description("The fingerprint returned by knowledge_discover")),
		mcp.WithString("discovery_status", mcp.Required(), mcp.Description("The discovery status this resolution followed")),
		mcp.WithString("session_id", mcp.Description("Optional session anchor")),
		mcp.WithArray("candidates", mcp.Description("Candidate summaries seen: [{source_id, name, score, rank}] (max 20)")),
		mcp.WithArray("selected", mcp.Description("Bases actually used: [{source_id, revision_id?, build_id?}] (max 10, ownership verified)")),
		mcp.WithArray("retrieved", mcp.Description("Retrieved references: [{source_id?, document_id?, page_path?, chunk_key?}] (max 200)")),
		mcp.WithArray("citations", mcp.Description("Citations carried by the answer: [{source_id?, document_id?, path?}] (max 100)")),
		mcp.WithString("selection_reason", mcp.Description("Why these bases were chosen (max 2000 code points)")),
		mcp.WithNumber("confidence", mcp.Description("Selection confidence 0..1")),
		mcp.WithString("idempotency_key", mcp.Description("Stable retry key; replay the exact same content with the same key")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		if !auth.HasScope(&auth.APIKey{Scopes: mcpauth.ScopesFromContext(ctx)}, "skills:r") {
			return mcpauth.ErrResult("insufficient scope: skills:r"), nil
		}
		// The argument shape mirrors RecordResolutionRequest's JSON exactly, so decode
		// through JSON instead of hand-walking nested maps.
		encoded, err := json.Marshal(req.GetArguments())
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		var recordReq RecordResolutionRequest
		if err := json.Unmarshal(encoded, &recordReq); err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		response, err := svc.RecordResolution(ctx, owner, recordReq)
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(response)
	})

	// knowledge_resolutions_list
	s.AddTool(mcp.NewTool("knowledge_resolutions_list",
		mcp.WithDescription("List resolution run summaries, newest first: discovery status, fingerprint, and selected/retrieved/citation counts without the evidence arrays. Filter by skill_version_id, session_id, or source_id (runs whose selected set contains that base)."),
		mcp.WithString("skill_version_id", mcp.Description("Filter by skill version")),
		mcp.WithString("session_id", mcp.Description("Filter by session")),
		mcp.WithString("source_id", mcp.Description("Filter to runs that selected this knowledge base")),
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
		response, err := svc.ListResolutions(ctx, owner.Account(), ResolutionListParams{
			SkillVersionID: mcpauth.StrArg(args, "skill_version_id"),
			SessionID:      mcpauth.StrArg(args, "session_id"),
			SourceID:       mcpauth.StrArg(args, "source_id"),
			Limit:          limit,
			Offset:         offset,
		})
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(response)
	})

	// knowledge_resolution_get
	s.AddTool(mcp.NewTool("knowledge_resolution_get",
		mcp.WithDescription("Load one resolution run in full: contract identity, discovery fingerprint and status, candidate summaries, verified selected bases, retrieved references, citations, and the recorded selection reason."),
		mcp.WithString("run_id", mcp.Required(), mcp.Description("Resolution run ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		run, err := svc.GetResolution(ctx, owner.Account(), mcpauth.StrArg(req.GetArguments(), "run_id"))
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(run)
	})

	// knowledge_index_active
	s.AddTool(mcp.NewTool("knowledge_index_active",
		mcp.WithDescription("Chunk-index the active revision of one source (or every active source when source_id is omitted) into account-scoped retrieval, and rebuild the document link graph. Embedding failures keep the lexical fallback available."),
		mcp.WithString("source_id", mcp.Description("Optional knowledge source ID; empty indexes all active sources")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		response, err := svc.IndexActiveRevisions(ctx, owner, mcpauth.StrArg(args, "source_id"))
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(response)
	})

	// knowledge_document_links
	s.AddTool(mcp.NewTool("knowledge_document_links",
		mcp.WithDescription("List both directions of one document's package-internal links (metadata only: direction, path, resolved document ID). Outgoing links come before incoming links."),
		mcp.WithString("document_id", mcp.Required(), mcp.Description("Knowledge document ID")),
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
		response, err := svc.ListDocumentLinks(ctx, owner.Account(), mcpauth.StrArg(args, "document_id"), limit, offset)
		if err != nil {
			return mcpauth.ErrResult("not found"), nil
		}
		return mcpauth.JSONResult(response)
	})

	registerWikiTools(s, svc)
	registerWikiRetrievalTools(s, svc)
	registerWikiLintTools(s, svc)
	registerWikiReviewTools(s, svc)
	registerWikiValidationTools(s, svc)

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
