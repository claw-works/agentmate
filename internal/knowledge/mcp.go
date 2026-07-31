package knowledge

import (
	"context"
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
	"knowledge_index_active":   "knowledge:rw",
	"knowledge_document_links": "knowledge:r",

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
