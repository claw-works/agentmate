package knowledge

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
	"knowledge_sources_list":   "knowledge:r",
	"knowledge_source_sync":    "knowledge:rw",
	"knowledge_documents_list": "knowledge:r",
	"knowledge_document_get":   "knowledge:r",
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
