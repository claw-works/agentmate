package bookmarks

import (
	"context"
	"net/http"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/wellxie/agentmate/internal/auth"
	"github.com/wellxie/agentmate/internal/mcpauth"
)

var toolScopes = map[string]string{
	"bookmark_create": "bookmarks:rw",
	"bookmark_get":    "bookmarks:r",
	"bookmark_list":   "bookmarks:r",
	"bookmark_update": "bookmarks:rw",
	"bookmark_delete": "bookmarks:rw",
}

// NewMCPServer builds the standalone MCP HTTP handler for the bookmarks module.
func NewMCPServer(svc *Service, authSvc *auth.Service) http.Handler {
	s := server.NewMCPServer("agentmate-bookmarks", "0.1.0", server.WithToolHandlerMiddleware(mcpauth.ScopeMiddleware(toolScopes)))

	s.AddTool(mcp.NewTool("bookmark_create",
		mcp.WithDescription("Save a new bookmark."),
		mcp.WithString("url", mcp.Required(), mcp.Description("URL to bookmark")),
		mcp.WithString("title", mcp.Description("Title")),
		mcp.WithString("summary", mcp.Description("Short summary")),
		mcp.WithString("content", mcp.Description("Full extracted content")),
		mcp.WithArray("tags", mcp.Description("Tags")),
		mcp.WithString("source", mcp.Description("Source label")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		b, err := svc.Create(ctx, owner, CreateRequest{
			URL:     mcpauth.StrArg(args, "url"),
			Title:   mcpauth.StrArg(args, "title"),
			Summary: mcpauth.StrArg(args, "summary"),
			Content: mcpauth.StrArg(args, "content"),
			Tags:    mcpauth.StrSliceArg(args, "tags"),
			Source:  mcpauth.StrArg(args, "source"),
		})
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(b)
	})

	s.AddTool(mcp.NewTool("bookmark_get",
		mcp.WithDescription("Get a bookmark by ID."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Bookmark ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		b, err := svc.Get(ctx, owner.Account(), mcpauth.StrArg(req.GetArguments(), "id"))
		if err != nil {
			return mcpauth.ErrResult("not found"), nil
		}
		return mcpauth.JSONResult(b)
	})

	s.AddTool(mcp.NewTool("bookmark_list",
		mcp.WithDescription("List bookmarks, optionally filtered by read status/tags/source."),
		mcp.WithArray("tags", mcp.Description("Filter by tags")),
		mcp.WithBoolean("is_read", mcp.Description("Filter by read status")),
		mcp.WithString("source", mcp.Description("Filter by source")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 20)")),
		mcp.WithNumber("offset", mcp.Description("Offset for pagination")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		list, err := svc.List(ctx, owner.Account(), ListParams{
			Tags:   mcpauth.StrSliceArg(args, "tags"),
			IsRead: mcpauth.BoolPtrArg(args, "is_read"),
			Limit:  mcpauth.IntArg(args, "limit"),
			Offset: mcpauth.IntArg(args, "offset"),
		})
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(list)
	})

	s.AddTool(mcp.NewTool("bookmark_update",
		mcp.WithDescription("Update a bookmark's fields, e.g. mark as read."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Bookmark ID")),
		mcp.WithString("title", mcp.Description("New title")),
		mcp.WithString("summary", mcp.Description("New summary")),
		mcp.WithArray("tags", mcp.Description("Replace tags")),
		mcp.WithBoolean("is_read", mcp.Description("Mark read/unread")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		r := UpdateRequest{
			Tags:   mcpauth.StrSliceArg(args, "tags"),
			IsRead: mcpauth.BoolPtrArg(args, "is_read"),
		}
		if v, ok := args["title"].(string); ok {
			r.Title = &v
		}
		if v, ok := args["summary"].(string); ok {
			r.Summary = &v
		}
		b, err := svc.Update(ctx, owner.Account(), mcpauth.StrArg(args, "id"), r)
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(b)
	})

	s.AddTool(mcp.NewTool("bookmark_delete",
		mcp.WithDescription("Delete a bookmark by ID."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Bookmark ID")),
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
