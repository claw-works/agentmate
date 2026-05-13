package mcp

import (
	"context"
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/wellxie/agentmate/internal/notes"
	"github.com/wellxie/agentmate/internal/todo"
)

func NewServer(todoSvc *todo.Service, notesSvc *notes.Service, userID string) *server.MCPServer {
	s := server.NewMCPServer("agentmate", "0.1.0")

	// Todo tools
	s.AddTool(mcp.NewTool("todo_create",
		mcp.WithDescription("Create a new todo"),
		mcp.WithString("title", mcp.Required(), mcp.Description("Title")),
		mcp.WithString("description", mcp.Description("Description")),
		mcp.WithString("priority", mcp.Description("low/medium/high")),
		mcp.WithString("due_date", mcp.Description("RFC3339 date")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments
		r := todo.CreateRequest{
			Title:       strArg(args, "title"),
			Description: strArg(args, "description"),
			Priority:    strArg(args, "priority"),
			DueDate:     strArg(args, "due_date"),
		}
		t, err := todoSvc.Create(ctx, userID, r)
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(t)
	})

	s.AddTool(mcp.NewTool("todo_list",
		mcp.WithDescription("List all todos"),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		list, err := todoSvc.List(ctx, userID)
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(list)
	})

	s.AddTool(mcp.NewTool("todo_get",
		mcp.WithDescription("Get a todo by ID"),
		mcp.WithString("id", mcp.Required()),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, err := todoSvc.Get(ctx, userID, strArg(req.Params.Arguments, "id"))
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(t)
	})

	s.AddTool(mcp.NewTool("todo_delete",
		mcp.WithDescription("Delete a todo"),
		mcp.WithString("id", mcp.Required()),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		err := todoSvc.Delete(ctx, userID, strArg(req.Params.Arguments, "id"))
		if err != nil {
			return errResult(err.Error()), nil
		}
		return mcp.NewToolResultText("deleted"), nil
	})

	// Notes tools
	s.AddTool(mcp.NewTool("note_create",
		mcp.WithDescription("Create a new note"),
		mcp.WithString("title", mcp.Required()),
		mcp.WithString("content", mcp.Description("Note content")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments
		r := notes.CreateRequest{
			Title:   strArg(args, "title"),
			Content: strArg(args, "content"),
		}
		n, err := notesSvc.Create(ctx, userID, r)
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(n)
	})

	s.AddTool(mcp.NewTool("note_list",
		mcp.WithDescription("List all notes"),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		list, err := notesSvc.List(ctx, userID)
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(list)
	})

	s.AddTool(mcp.NewTool("note_get",
		mcp.WithDescription("Get a note by ID"),
		mcp.WithString("id", mcp.Required()),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		n, err := notesSvc.Get(ctx, userID, strArg(req.Params.Arguments, "id"))
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(n)
	})

	s.AddTool(mcp.NewTool("note_delete",
		mcp.WithDescription("Delete a note"),
		mcp.WithString("id", mcp.Required()),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		err := notesSvc.Delete(ctx, userID, strArg(req.Params.Arguments, "id"))
		if err != nil {
			return errResult(err.Error()), nil
		}
		return mcp.NewToolResultText("deleted"), nil
	})

	return s
}

func strArg(args map[string]interface{}, key string) string {
	v, _ := args[key].(string)
	return v
}

func errResult(msg string) *mcp.CallToolResult {
	r := mcp.NewToolResultText(msg)
	r.IsError = true
	return r
}

func jsonResult(v interface{}) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return errResult(err.Error()), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}
