package mcp

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/wellxie/agentmate/internal/bookmarks"
	"github.com/wellxie/agentmate/internal/expenses"
	"github.com/wellxie/agentmate/internal/notes"
	"github.com/wellxie/agentmate/internal/reports"
	"github.com/wellxie/agentmate/internal/todo"
)

type contextKey string

const UserIDKey contextKey = "user_id"

func userIDFromCtx(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(UserIDKey).(string)
	return id, ok && id != ""
}

func NewServer(todoSvc *todo.Service, notesSvc *notes.Service, reportsSvc *reports.Service, bookmarksSvc *bookmarks.Service, expensesSvc *expenses.Service) *server.MCPServer {
	s := server.NewMCPServer("agentmate", "0.1.0")

	// ─── Todo tools ───

	s.AddTool(mcp.NewTool("todo_create",
		mcp.WithDescription("Create a tracked task with status lifecycle (pending → in_progress → done). Use when the user wants to track something actionable with progress. NOT for quick notes or ideas without action needed."),
		mcp.WithString("title", mcp.Required(), mcp.Description("Title")),
		mcp.WithString("description", mcp.Description("Description")),
		mcp.WithString("priority", mcp.Description("low/medium/high")),
		mcp.WithString("due_date", mcp.Description("RFC3339 date")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		args := req.GetArguments()
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
		mcp.WithDescription("List actionable tasks. Filter by status (pending/in_progress/done), priority, or tags."),
		mcp.WithString("tag", mcp.Description("Filter by tag")),
		mcp.WithString("status", mcp.Description("Filter by status")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		args := req.GetArguments()
		list, err := todoSvc.List(ctx, userID, todo.ListTodosParams{
			Tags:   splitTags(strArg(args, "tag")),
			Status: strArg(args, "status"),
		})
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(list)
	})

	s.AddTool(mcp.NewTool("todo_get",
		mcp.WithDescription("Get a single task by ID."),
		mcp.WithString("id", mcp.Required()),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		t, err := todoSvc.Get(ctx, userID, strArg(req.GetArguments(), "id"))
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(t)
	})

	s.AddTool(mcp.NewTool("todo_update",
		mcp.WithDescription("Update a task's status, priority, or details. Use to mark tasks in_progress or done."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Todo ID")),
		mcp.WithString("title", mcp.Description("New title")),
		mcp.WithString("description", mcp.Description("New description")),
		mcp.WithString("status", mcp.Description("pending/in_progress/done")),
		mcp.WithString("priority", mcp.Description("low/medium/high")),
		mcp.WithString("due_date", mcp.Description("RFC3339 date")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		args := req.GetArguments()
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
		mcp.WithDescription("Permanently delete a task."),
		mcp.WithString("id", mcp.Required()),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		err := todoSvc.Delete(ctx, userID, strArg(req.GetArguments(), "id"))
		if err != nil {
			return errResult(err.Error()), nil
		}
		return mcp.NewToolResultText("deleted"), nil
	})

	s.AddTool(mcp.NewTool("todo_search",
		mcp.WithDescription("Search tasks by keyword across title and description."),
		mcp.WithString("q", mcp.Required(), mcp.Description("Search query")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		list, err := todoSvc.Search(ctx, userID, strArg(req.GetArguments(), "q"))
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(list)
	})

	// ─── Notes tools ───

	s.AddTool(mcp.NewTool("note_create",
		mcp.WithDescription("Save a quick note, idea, or unstructured text. No status tracking. Use for thoughts, meeting notes, observations. NOT for tasks that need to be completed, NOT for URLs to save, NOT for financial records."),
		mcp.WithString("title", mcp.Required()),
		mcp.WithString("content", mcp.Description("Note content")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		args := req.GetArguments()
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
		mcp.WithDescription("List notes. Filter by tag."),
		mcp.WithString("tag", mcp.Description("Filter by tag")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		args := req.GetArguments()
		list, err := notesSvc.List(ctx, userID, notes.ListNotesParams{Tags: splitTags(strArg(args, "tag"))})
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(list)
	})

	s.AddTool(mcp.NewTool("note_get",
		mcp.WithDescription("Get a single note by ID."),
		mcp.WithString("id", mcp.Required()),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		n, err := notesSvc.Get(ctx, userID, strArg(req.GetArguments(), "id"))
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(n)
	})

	s.AddTool(mcp.NewTool("note_update",
		mcp.WithDescription("Edit a note's title, content, or tags."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Note ID")),
		mcp.WithString("title", mcp.Description("New title")),
		mcp.WithString("content", mcp.Description("New content")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		args := req.GetArguments()
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
		mcp.WithDescription("Delete a note."),
		mcp.WithString("id", mcp.Required()),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		err := notesSvc.Delete(ctx, userID, strArg(req.GetArguments(), "id"))
		if err != nil {
			return errResult(err.Error()), nil
		}
		return mcp.NewToolResultText("deleted"), nil
	})

	s.AddTool(mcp.NewTool("note_search",
		mcp.WithDescription("Search notes by keyword."),
		mcp.WithString("q", mcp.Required(), mcp.Description("Search query")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		list, err := notesSvc.Search(ctx, userID, strArg(req.GetArguments(), "q"))
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(list)
	})

	// ─── Reports tools ───

	s.AddTool(mcp.NewTool("report_create",
		mcp.WithDescription("Save an Agent-generated report in Markdown or HTML format. Use for structured output from automated tasks (e.g., daily GitHub trending, weekly summaries, analysis results). NOT for casual notes. Always set source to identify which agent/task generated it."),
		mcp.WithString("title", mcp.Required(), mcp.Description("Report title")),
		mcp.WithString("content", mcp.Description("Report content")),
		mcp.WithString("format", mcp.Description("md or html")),
		mcp.WithString("source", mcp.Description("Source identifier")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		args := req.GetArguments()
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
		mcp.WithDescription("List reports. Filter by source (e.g. 'github-trending-daily') or tag."),
		mcp.WithString("source", mcp.Description("Filter by source")),
		mcp.WithString("tag", mcp.Description("Filter by tag")),
		mcp.WithNumber("limit", mcp.Description("Max results")),
		mcp.WithNumber("offset", mcp.Description("Offset")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		args := req.GetArguments()
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
		mcp.WithDescription("Get full report content by ID."),
		mcp.WithString("id", mcp.Required()),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		rpt, err := reportsSvc.Get(ctx, userID, strArg(req.GetArguments(), "id"))
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(rpt)
	})

	s.AddTool(mcp.NewTool("report_update",
		mcp.WithDescription("Update report metadata (title, tags, source). Content is immutable."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Report ID")),
		mcp.WithString("title", mcp.Description("New title")),
		mcp.WithString("source", mcp.Description("New source")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		args := req.GetArguments()
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
		mcp.WithDescription("Delete a report."),
		mcp.WithString("id", mcp.Required()),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		err := reportsSvc.Delete(ctx, userID, strArg(req.GetArguments(), "id"))
		if err != nil {
			return errResult(err.Error()), nil
		}
		return mcp.NewToolResultText("deleted"), nil
	})

	s.AddTool(mcp.NewTool("report_search",
		mcp.WithDescription("Full-text search across report titles and content."),
		mcp.WithString("q", mcp.Required(), mcp.Description("Search query")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		list, err := reportsSvc.List(ctx, userID, reports.ListReportsParams{Search: strArg(req.GetArguments(), "q")})
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(list)
	})

	// ─── Bookmarks tools ───

	s.AddTool(mcp.NewTool("bookmark_save",
		mcp.WithDescription("Save a URL with optional title, summary, and tags. Use when the user wants to save a link for later reading. Has read/unread tracking. NOT for notes without a URL, NOT for tasks."),
		mcp.WithString("url", mcp.Required(), mcp.Description("URL to bookmark")),
		mcp.WithString("title", mcp.Description("Title")),
		mcp.WithString("summary", mcp.Description("Summary")),
		mcp.WithString("content", mcp.Description("Full page content (Markdown or plain text), extracted by the agent from the webpage")),
		mcp.WithString("tags", mcp.Description("Comma-separated tags")),
		mcp.WithString("source", mcp.Description("Source identifier")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		args := req.GetArguments()
		r := bookmarks.CreateRequest{
			URL:     strArg(args, "url"),
			Title:   strArg(args, "title"),
			Summary: strArg(args, "summary"),
			Content: strArg(args, "content"),
			Source:  strArg(args, "source"),
		}
		if t := strArg(args, "tags"); t != "" {
			r.Tags = splitTags(t)
		}
		b, err := bookmarksSvc.Create(ctx, userID, r)
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(b)
	})

	s.AddTool(mcp.NewTool("bookmark_list",
		mcp.WithDescription("List saved bookmarks. Filter by read status, tag, or source."),
		mcp.WithString("is_read", mcp.Description("Filter by read status: true/false")),
		mcp.WithString("tag", mcp.Description("Filter by tag")),
		mcp.WithString("source", mcp.Description("Filter by source")),
		mcp.WithString("limit", mcp.Description("Max results")),
		mcp.WithString("offset", mcp.Description("Offset")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		args := req.GetArguments()
		params := bookmarks.ListParams{
			Tags: splitTags(strArg(args, "tag")),
		}
		if v := strArg(args, "is_read"); v != "" {
			b := v == "true"
			params.IsRead = &b
		}
		if v := strArg(args, "limit"); v != "" {
			params.Limit, _ = strconv.Atoi(v)
		}
		if v := strArg(args, "offset"); v != "" {
			params.Offset, _ = strconv.Atoi(v)
		}
		list, err := bookmarksSvc.List(ctx, userID, params)
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(list)
	})

	s.AddTool(mcp.NewTool("bookmark_get",
		mcp.WithDescription("Get a single bookmark by ID."),
		mcp.WithString("id", mcp.Required()),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		b, err := bookmarksSvc.Get(ctx, userID, strArg(req.GetArguments(), "id"))
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(b)
	})

	s.AddTool(mcp.NewTool("bookmark_update",
		mcp.WithDescription("Update a bookmark's title, summary, content, tags, or read status."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Bookmark ID")),
		mcp.WithString("title", mcp.Description("New title")),
		mcp.WithString("summary", mcp.Description("New summary")),
		mcp.WithString("tags", mcp.Description("Comma-separated tags")),
		mcp.WithString("is_read", mcp.Description("true/false")),
		mcp.WithString("content", mcp.Description("Full page content")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		args := req.GetArguments()
		r := bookmarks.UpdateRequest{}
		if v := strArg(args, "title"); v != "" {
			r.Title = &v
		}
		if v := strArg(args, "summary"); v != "" {
			r.Summary = &v
		}
		if v := strArg(args, "content"); v != "" {
			r.Content = &v
		}
		if v := strArg(args, "tags"); v != "" {
			r.Tags = splitTags(v)
		}
		if v := strArg(args, "is_read"); v != "" {
			b := v == "true"
			r.IsRead = &b
		}
		bm, err := bookmarksSvc.Update(ctx, userID, strArg(args, "id"), r)
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(bm)
	})

	s.AddTool(mcp.NewTool("bookmark_mark_read",
		mcp.WithDescription("Mark a bookmark as read. Shortcut for updating read status."),
		mcp.WithString("id", mcp.Required()),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		isRead := true
		bm, err := bookmarksSvc.Update(ctx, userID, strArg(req.GetArguments(), "id"), bookmarks.UpdateRequest{IsRead: &isRead})
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(bm)
	})

	s.AddTool(mcp.NewTool("bookmark_delete",
		mcp.WithDescription("Delete a bookmark."),
		mcp.WithString("id", mcp.Required()),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		err := bookmarksSvc.Delete(ctx, userID, strArg(req.GetArguments(), "id"))
		if err != nil {
			return errResult(err.Error()), nil
		}
		return mcp.NewToolResultText("deleted"), nil
	})

	s.AddTool(mcp.NewTool("bookmark_search",
		mcp.WithDescription("Search bookmarks by keyword across URL, title, and summary."),
		mcp.WithString("q", mcp.Required(), mcp.Description("Search query")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		list, err := bookmarksSvc.List(ctx, userID, bookmarks.ListParams{Search: strArg(req.GetArguments(), "q")})
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(list)
	})

	// ─── Expenses tools ───

	s.AddTool(mcp.NewTool("expense_add",
		mcp.WithDescription("Record a financial expense with amount, currency, and optional tags. Use for tracking spending and purchases. NOT for tasks, notes, or non-financial records."),
		mcp.WithString("amount", mcp.Required(), mcp.Description("Amount as float string")),
		mcp.WithString("currency", mcp.Description("Currency code, default CNY")),
		mcp.WithString("description", mcp.Description("Description")),
		mcp.WithString("tags", mcp.Description("Comma-separated tags")),
		mcp.WithString("source", mcp.Description("Source identifier")),
		mcp.WithString("happened_at", mcp.Description("RFC3339 datetime")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		args := req.GetArguments()
		amount, err := strconv.ParseFloat(strArg(args, "amount"), 64)
		if err != nil {
			return errResult("invalid amount"), nil
		}
		r := expenses.CreateRequest{
			Amount:      amount,
			Currency:    strArg(args, "currency"),
			Description: strArg(args, "description"),
			Source:      strArg(args, "source"),
			HappenedAt:  strArg(args, "happened_at"),
		}
		if t := strArg(args, "tags"); t != "" {
			r.Tags = splitTags(t)
		}
		e, err := expensesSvc.Create(ctx, userID, r)
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(e)
	})

	s.AddTool(mcp.NewTool("expense_list",
		mcp.WithDescription("List expenses. Filter by tags, date range, or pagination."),
		mcp.WithString("tags", mcp.Description("Comma-separated tags")),
		mcp.WithString("start", mcp.Description("Start date RFC3339")),
		mcp.WithString("end", mcp.Description("End date RFC3339")),
		mcp.WithString("limit", mcp.Description("Max results")),
		mcp.WithString("offset", mcp.Description("Offset")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		args := req.GetArguments()
		params := expenses.ListParams{
			Tags:  splitTags(strArg(args, "tags")),
			Start: strArg(args, "start"),
			End:   strArg(args, "end"),
		}
		if v := strArg(args, "limit"); v != "" {
			params.Limit, _ = strconv.Atoi(v)
		}
		if v := strArg(args, "offset"); v != "" {
			params.Offset, _ = strconv.Atoi(v)
		}
		list, err := expensesSvc.List(ctx, userID, params)
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(list)
	})

	s.AddTool(mcp.NewTool("expense_summary",
		mcp.WithDescription("Get aggregated expense statistics with breakdowns by tag and by month. Use for spending analysis and budget reviews."),
		mcp.WithString("start", mcp.Description("Start date RFC3339")),
		mcp.WithString("end", mcp.Description("End date RFC3339")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		args := req.GetArguments()
		params := expenses.ListParams{
			Start: strArg(args, "start"),
			End:   strArg(args, "end"),
		}
		summary, err := expensesSvc.Summary(ctx, userID, params)
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(summary)
	})

	s.AddTool(mcp.NewTool("expense_search",
		mcp.WithDescription("Search expenses by keyword in description."),
		mcp.WithString("q", mcp.Required(), mcp.Description("Search query")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		list, err := expensesSvc.List(ctx, userID, expenses.ListParams{Search: strArg(req.GetArguments(), "q")})
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(list)
	})

	s.AddTool(mcp.NewTool("expense_update",
		mcp.WithDescription("Update an expense's amount, description, tags, or date."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Expense ID")),
		mcp.WithString("amount", mcp.Description("New amount as float string")),
		mcp.WithString("description", mcp.Description("New description")),
		mcp.WithString("tags", mcp.Description("Comma-separated tags")),
		mcp.WithString("happened_at", mcp.Description("RFC3339 datetime")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		args := req.GetArguments()
		r := expenses.UpdateRequest{}
		if v := strArg(args, "amount"); v != "" {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return errResult("invalid amount"), nil
			}
			r.Amount = &f
		}
		if v := strArg(args, "description"); v != "" {
			r.Description = &v
		}
		if v := strArg(args, "tags"); v != "" {
			r.Tags = splitTags(v)
		}
		if v := strArg(args, "happened_at"); v != "" {
			r.HappenedAt = &v
		}
		e, err := expensesSvc.Update(ctx, userID, strArg(args, "id"), r)
		if err != nil {
			return errResult(err.Error()), nil
		}
		return jsonResult(e)
	})

	s.AddTool(mcp.NewTool("expense_delete",
		mcp.WithDescription("Delete an expense record."),
		mcp.WithString("id", mcp.Required()),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return errResult("unauthorized"), nil
		}
		err := expensesSvc.Delete(ctx, userID, strArg(req.GetArguments(), "id"))
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

func splitTags(s string) []string {
	parts := strings.Split(s, ",")
	tags := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}
