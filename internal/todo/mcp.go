package todo

import (
	"context"
	"net/http"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/wellxie/agentmate/internal/auth"
	"github.com/wellxie/agentmate/internal/mcpauth"
)

var toolScopes = map[string]string{
	"todo_create": "todos:rw",
	"todo_get":    "todos:r",
	"todo_list":   "todos:r",
	"todo_update": "todos:rw",
	"todo_delete": "todos:rw",
	"todo_search": "todos:r",
}

// NewMCPServer builds the standalone MCP HTTP handler for the todos module.
func NewMCPServer(svc *Service, authSvc *auth.Service) http.Handler {
	s := server.NewMCPServer("agentmate-todos", "0.1.0", server.WithToolHandlerMiddleware(mcpauth.ScopeMiddleware(toolScopes)))

	s.AddTool(mcp.NewTool("todo_create",
		mcp.WithDescription("Create a new todo item."),
		mcp.WithString("title", mcp.Required(), mcp.Description("Todo title")),
		mcp.WithString("description", mcp.Description("Details")),
		mcp.WithString("priority", mcp.Description("low/medium/high, default medium")),
		mcp.WithString("due_date", mcp.Description("RFC3339 due date")),
		mcp.WithArray("tags", mcp.Description("Tags")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		t, err := svc.Create(ctx, owner, CreateRequest{
			Title:       mcpauth.StrArg(args, "title"),
			Description: mcpauth.StrArg(args, "description"),
			Priority:    mcpauth.StrArg(args, "priority"),
			DueDate:     mcpauth.StrArg(args, "due_date"),
			Tags:        mcpauth.StrSliceArg(args, "tags"),
		})
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(t)
	})

	s.AddTool(mcp.NewTool("todo_get",
		mcp.WithDescription("Get a todo by ID."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Todo ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		t, err := svc.Get(ctx, owner.Account(), mcpauth.StrArg(req.GetArguments(), "id"))
		if err != nil {
			return mcpauth.ErrResult("not found"), nil
		}
		return mcpauth.JSONResult(t)
	})

	s.AddTool(mcp.NewTool("todo_list",
		mcp.WithDescription("List todos, optionally filtered by tags/status."),
		mcp.WithArray("tags", mcp.Description("Filter by tags")),
		mcp.WithString("status", mcp.Description("Filter by status: pending/in_progress/done")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 20)")),
		mcp.WithNumber("offset", mcp.Description("Offset for pagination")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		list, err := svc.List(ctx, owner.Account(), ListTodosParams{
			Tags:   mcpauth.StrSliceArg(args, "tags"),
			Status: mcpauth.StrArg(args, "status"),
			Limit:  mcpauth.IntArg(args, "limit"),
			Offset: mcpauth.IntArg(args, "offset"),
		})
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(list)
	})

	s.AddTool(mcp.NewTool("todo_update",
		mcp.WithDescription("Update a todo's fields."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Todo ID")),
		mcp.WithString("title", mcp.Description("New title")),
		mcp.WithString("description", mcp.Description("New description")),
		mcp.WithString("status", mcp.Description("pending/in_progress/done")),
		mcp.WithString("priority", mcp.Description("low/medium/high")),
		mcp.WithString("due_date", mcp.Description("RFC3339 due date")),
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
		if v, ok := args["description"].(string); ok {
			r.Description = &v
		}
		if v, ok := args["status"].(string); ok {
			r.Status = &v
		}
		if v, ok := args["priority"].(string); ok {
			r.Priority = &v
		}
		if v, ok := args["due_date"].(string); ok {
			r.DueDate = &v
		}
		t, err := svc.Update(ctx, owner.Account(), mcpauth.StrArg(args, "id"), r)
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(t)
	})

	s.AddTool(mcp.NewTool("todo_delete",
		mcp.WithDescription("Delete a todo by ID."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Todo ID")),
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

	s.AddTool(mcp.NewTool("todo_search",
		mcp.WithDescription("Full-text search todos by title/description."),
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
