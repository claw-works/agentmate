package reports

import (
	"context"
	"net/http"

	"github.com/claw-works/agentmate/internal/auth"
	"github.com/claw-works/agentmate/internal/mcpauth"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var toolScopes = map[string]string{
	"report_create":       "reports:rw",
	"report_get":          "reports:r",
	"report_list":         "reports:r",
	"report_list_sources": "reports:r",
	"report_update":       "reports:rw",
	"report_delete":       "reports:rw",
}

// NewMCPServer builds the standalone MCP HTTP handler for the reports module.
func NewMCPServer(svc *Service, authSvc *auth.Service) http.Handler {
	s := server.NewMCPServer("agentmate-reports", "0.1.0", server.WithToolHandlerMiddleware(mcpauth.ScopeMiddleware(toolScopes)))

	s.AddTool(mcp.NewTool("report_create",
		mcp.WithDescription("Create a new report (md or html content)."),
		mcp.WithString("title", mcp.Required(), mcp.Description("Report title")),
		mcp.WithString("content", mcp.Description("Report content")),
		mcp.WithString("format", mcp.Description("md or html, default md")),
		mcp.WithArray("tags", mcp.Description("Tags, e.g. weekly/monthly/customer/project/other")),
		mcp.WithString("source", mcp.Description("Source label, e.g. agent/skill name")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		format := mcpauth.StrArg(args, "format")
		if format == "" {
			format = "md"
		}
		r, err := svc.Create(ctx, owner, CreateReportRequest{
			Title:   mcpauth.StrArg(args, "title"),
			Content: mcpauth.StrArg(args, "content"),
			Format:  format,
			Tags:    mcpauth.StrSliceArg(args, "tags"),
			Source:  mcpauth.StrArg(args, "source"),
		})
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(r)
	})

	s.AddTool(mcp.NewTool("report_get",
		mcp.WithDescription("Get a report by ID, including full content."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Report ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		r, err := svc.Get(ctx, owner.Account(), mcpauth.StrArg(req.GetArguments(), "id"))
		if err != nil {
			return mcpauth.ErrResult("not found"), nil
		}
		return mcpauth.JSONResult(r)
	})

	s.AddTool(mcp.NewTool("report_list",
		mcp.WithDescription("List reports, optionally filtered by tag/source/search query."),
		mcp.WithString("tag", mcp.Description("Filter by tag")),
		mcp.WithString("source", mcp.Description("Filter by source")),
		mcp.WithString("q", mcp.Description("Search in title")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 20)")),
		mcp.WithNumber("offset", mcp.Description("Offset for pagination")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		list, err := svc.List(ctx, owner.Account(), ListReportsParams{
			Tag:    mcpauth.StrArg(args, "tag"),
			Source: mcpauth.StrArg(args, "source"),
			Search: mcpauth.StrArg(args, "q"),
			Limit:  mcpauth.IntArg(args, "limit"),
			Offset: mcpauth.IntArg(args, "offset"),
		})
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(list)
	})

	s.AddTool(mcp.NewTool("report_list_sources",
		mcp.WithDescription("List distinct report sources with counts."),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		list, err := svc.ListSources(ctx, owner.Account())
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(list)
	})

	s.AddTool(mcp.NewTool("report_update",
		mcp.WithDescription("Update a report's title, content, tags, or source."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Report ID")),
		mcp.WithString("title", mcp.Description("New title")),
		mcp.WithString("content", mcp.Description("New report content")),
		mcp.WithArray("tags", mcp.Description("Replace tags")),
		mcp.WithString("source", mcp.Description("New source")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		r := UpdateReportRequest{Tags: mcpauth.StrSliceArg(args, "tags")}
		if v, ok := args["title"].(string); ok {
			r.Title = &v
		}
		if v, ok := args["content"].(string); ok {
			r.Content = &v
		}
		if v, ok := args["source"].(string); ok {
			r.Source = &v
		}
		rep, err := svc.Update(ctx, owner.Account(), mcpauth.StrArg(args, "id"), r)
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(rep)
	})

	s.AddTool(mcp.NewTool("report_delete",
		mcp.WithDescription("Delete a report by ID."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Report ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		if err := svc.Delete(ctx, owner.Account(), mcpauth.StrArg(req.GetArguments(), "id")); err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(map[string]bool{"deleted": true})
	})

	return server.NewStreamableHTTPServer(s, server.WithHTTPContextFunc(mcpauth.HTTPContextFunc(authSvc)))
}
