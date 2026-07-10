package notes

import (
	"context"
	"net/http"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/wellxie/agentmate/internal/auth"
	"github.com/wellxie/agentmate/internal/mcpauth"
)

var toolScopes = map[string]string{
	"note_create": "notes:rw",
	"note_get":    "notes:r",
	"note_list":   "notes:r",
	"note_update": "notes:rw",
	"note_append": "notes:rw",
	"note_delete": "notes:rw",
	"note_search": "notes:r",
}

// NewMCPServer builds the standalone MCP HTTP handler for the notes module.
func NewMCPServer(svc *Service, authSvc *auth.Service) http.Handler {
	s := server.NewMCPServer("agentmate-notes", "0.1.0", server.WithToolHandlerMiddleware(mcpauth.ScopeMiddleware(toolScopes)))

	s.AddTool(mcp.NewTool("note_create",
		mcp.WithDescription("Create a new note."),
		mcp.WithString("title", mcp.Required(), mcp.Description("Note title")),
		mcp.WithString("content", mcp.Description("Note content")),
		mcp.WithArray("tags", mcp.Description("Tags")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		n, err := svc.Create(ctx, owner, CreateRequest{
			Title:   mcpauth.StrArg(args, "title"),
			Content: mcpauth.StrArg(args, "content"),
			Tags:    mcpauth.StrSliceArg(args, "tags"),
		})
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(n)
	})

	s.AddTool(mcp.NewTool("note_get",
		mcp.WithDescription("Get a note by ID."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Note ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		n, err := svc.Get(ctx, owner.Account(), mcpauth.StrArg(req.GetArguments(), "id"))
		if err != nil {
			return mcpauth.ErrResult("not found"), nil
		}
		return mcpauth.JSONResult(n)
	})

	s.AddTool(mcp.NewTool("note_list",
		mcp.WithDescription("List notes, optionally filtered by tags."),
		mcp.WithArray("tags", mcp.Description("Filter by tags")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 20)")),
		mcp.WithNumber("offset", mcp.Description("Offset for pagination")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		list, err := svc.List(ctx, owner.Account(), ListNotesParams{
			Tags:   mcpauth.StrSliceArg(args, "tags"),
			Limit:  mcpauth.IntArg(args, "limit"),
			Offset: mcpauth.IntArg(args, "offset"),
		})
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(list)
	})

	s.AddTool(mcp.NewTool("note_update",
		mcp.WithDescription("Update a note's fields (replaces content/tags if provided)."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Note ID")),
		mcp.WithString("title", mcp.Description("New title")),
		mcp.WithString("content", mcp.Description("New content (replaces existing)")),
		mcp.WithArray("tags", mcp.Description("Replace tags")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		r := UpdateRequest{Tags: mcpauth.StrSliceArg(args, "tags")}
		if v, ok := args["title"].(string); ok {
			r.Title = &v
		}
		if v, ok := args["content"].(string); ok {
			r.Content = &v
		}
		n, err := svc.Update(ctx, owner.Account(), mcpauth.StrArg(args, "id"), r)
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(n)
	})

	s.AddTool(mcp.NewTool("note_append",
		mcp.WithDescription("Append content to the end of an existing note."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Note ID")),
		mcp.WithString("content", mcp.Required(), mcp.Description("Content to append")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		n, err := svc.Append(ctx, mcpauth.StrArg(args, "id"), owner.Account(), mcpauth.StrArg(args, "content"))
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(n)
	})

	s.AddTool(mcp.NewTool("note_delete",
		mcp.WithDescription("Delete a note by ID."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Note ID")),
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

	s.AddTool(mcp.NewTool("note_search",
		mcp.WithDescription("Full-text search notes by title/content."),
		mcp.WithString("q", mcp.Required(), mcp.Description("Search query")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		list, err := svc.Search(ctx, owner.Account(), mcpauth.StrArg(req.GetArguments(), "q"))
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(list)
	})

	return server.NewStreamableHTTPServer(s, server.WithHTTPContextFunc(mcpauth.HTTPContextFunc(authSvc)))
}
