package mcp

import (
	"context"
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/wellxie/agentmate/internal/notes"
	"github.com/wellxie/agentmate/internal/reports"
	"github.com/wellxie/agentmate/internal/todo"
)

func NewServer(todoSvc *todo.Service, notesSvc *notes.Service, reportsSvc *reports.Service, userID string) *server.MCPServer {
	s := server.NewMCPServer("agentmate", "0.1.0")

	// ─── Todo tools ───

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
		mcp.WithDescription("List todos with optional filters"),
		mcp.WithString("tag", mcp.Description("Filter by tag")),
		mcp.WithString("status", mcp.Description("Filter by status")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments
		list, err := todoSvc.List(ctx, userID, todo.ListTodosParams{
			Tag:    strArg(args, "tag"),
			Status: strArg(args, "status"),
		})
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

	s.AddTool(mcp.NewTool("todo_update",
		mcp.WithDescription("Update a todo"),
		mcp.WithString("id", mcp.Required(), mcp.Description("Todo ID")),
		mcp.WithString("title", mcp.Description("New title")),
		mcp.WithString("description", mcp.Description("New description")),
		mcp.WithString("status", mcp.Description("pending/in_progress/done")),
		mcp.WithString("priority", mcp.Description("low/medium/high")),
		mcp.WithString("due_date", mcp.Description("RFC3339 date")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments
		r := todo.UpdateRequest{}
		if v := strArg(args, "title"); v != "" {
			r.Title = &v
		}
		if v := strArg(args, "description"); v != "" {
			r.Description = &v
		}
		if v := strArg(args, "status"); v != "" {
			r.Status = &v
		}
		if v := strArg(args, "priority"); v != "" {
			r.Priority = &v
		}
		if v := strArg(args, "due_date"); v != "" {
			r.DueDate = &v
		}
		t, err := todoSvc.Update(ctx, userID, strArg(args, "id"), r)
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

	s.AddTool(mcp.NewTool("todo_search",
		mcp.WithDescription("Search todos by keyword"),
		mcp.WithString("q", mcp.Required(), mcp.Description("Search query")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		list, err := todoSvc.Search(ctx, userID, strArg(req.Params.Arguments, "q"))
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(list)
	})

	// ─── Notes tools ───

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
		mcp.WithString("tag", mcp.Description("Filter by tag")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments
		list, err := notesSvc.List(ctx, userID, notes.ListNotesParams{Tag: strArg(args, "tag")})
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

	s.AddTool(mcp.NewTool("note_update",
		mcp.WithDescription("Update a note"),
		mcp.WithString("id", mcp.Required(), mcp.Description("Note ID")),
		mcp.WithString("title", mcp.Description("New title")),
		mcp.WithString("content", mcp.Description("New content")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments
		r := notes.UpdateRequest{}
		if v := strArg(args, "title"); v != "" {
			r.Title = &v
		}
		if v := strArg(args, "content"); v != "" {
			r.Content = &v
		}
		n, err := notesSvc.Update(ctx, userID, strArg(args, "id"), r)
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

	s.AddTool(mcp.NewTool("note_search",
		mcp.WithDescription("Search notes by keyword"),
		mcp.WithString("q", mcp.Required(), mcp.Description("Search query")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		list, err := notesSvc.Search(ctx, userID, strArg(req.Params.Arguments, "q"))
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(list)
	})

	// ─── Reports tools ───

	s.AddTool(mcp.NewTool("report_create",
		mcp.WithDescription("Create a new report"),
		mcp.WithString("title", mcp.Required(), mcp.Description("Report title")),
		mcp.WithString("content", mcp.Description("Report content")),
		mcp.WithString("format", mcp.Description("md or html")),
		mcp.WithString("source", mcp.Description("Source identifier")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments
		r := reports.CreateReportRequest{
			Title:   strArg(args, "title"),
			Content: strArg(args, "content"),
			Format:  strArg(args, "format"),
			Source:  strArg(args, "source"),
			Tags:    strSliceArg(args, "tags"),
		}
		rpt, err := reportsSvc.Create(ctx, userID, r, nil)
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(rpt)
	})

	s.AddTool(mcp.NewTool("report_list",
		mcp.WithDescription("List reports"),
		mcp.WithString("source", mcp.Description("Filter by source")),
		mcp.WithString("tag", mcp.Description("Filter by tag")),
		mcp.WithNumber("limit", mcp.Description("Max results")),
		mcp.WithNumber("offset", mcp.Description("Offset")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments
		params := reports.ListReportsParams{
			Source: strArg(args, "source"),
			Tag:    strArg(args, "tag"),
			Limit:  intArg(args, "limit"),
			Offset: intArg(args, "offset"),
		}
		list, err := reportsSvc.List(ctx, userID, params)
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(list)
	})

	s.AddTool(mcp.NewTool("report_get",
		mcp.WithDescription("Get a report by ID"),
		mcp.WithString("id", mcp.Required()),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		rpt, err := reportsSvc.Get(ctx, userID, strArg(req.Params.Arguments, "id"))
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(rpt)
	})

	s.AddTool(mcp.NewTool("report_update",
		mcp.WithDescription("Update a report"),
		mcp.WithString("id", mcp.Required(), mcp.Description("Report ID")),
		mcp.WithString("title", mcp.Description("New title")),
		mcp.WithString("source", mcp.Description("New source")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments
		r := reports.UpdateReportRequest{}
		if v := strArg(args, "title"); v != "" {
			r.Title = &v
		}
		if v := strArg(args, "source"); v != "" {
			r.Source = &v
		}
		r.Tags = strSliceArg(args, "tags")
		rpt, err := reportsSvc.Update(ctx, userID, strArg(args, "id"), r)
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(rpt)
	})

	s.AddTool(mcp.NewTool("report_delete",
		mcp.WithDescription("Delete a report"),
		mcp.WithString("id", mcp.Required()),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		err := reportsSvc.Delete(ctx, userID, strArg(req.Params.Arguments, "id"))
		if err != nil {
			return errResult(err.Error()), nil
		}
		return mcp.NewToolResultText("deleted"), nil
	})

	s.AddTool(mcp.NewTool("report_search",
		mcp.WithDescription("Search reports by keyword"),
		mcp.WithString("q", mcp.Required(), mcp.Description("Search query")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		list, err := reportsSvc.List(ctx, userID, reports.ListReportsParams{Search: strArg(req.Params.Arguments, "q")})
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(list)
	})

	return s
}

func strArg(args map[string]interface{}, key string) string {
	v, _ := args[key].(string)
	return v
}

func intArg(args map[string]interface{}, key string) int {
	v, _ := args[key].(float64)
	return int(v)
}

func strSliceArg(args map[string]interface{}, key string) []string {
	v, ok := args[key]
	if !ok || v == nil {
		return nil
	}
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	result := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
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
