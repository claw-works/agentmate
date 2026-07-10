package expenses

import (
	"context"
	"net/http"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/wellxie/agentmate/internal/auth"
	"github.com/wellxie/agentmate/internal/mcpauth"
)

var toolScopes = map[string]string{
	"expense_create":  "expenses:rw",
	"expense_get":     "expenses:r",
	"expense_list":    "expenses:r",
	"expense_summary": "expenses:r",
	"expense_update":  "expenses:rw",
	"expense_delete":  "expenses:rw",
}

// NewMCPServer builds the standalone MCP HTTP handler for the expenses module.
func NewMCPServer(svc *Service, authSvc *auth.Service) http.Handler {
	s := server.NewMCPServer("agentmate-expenses", "0.1.0", server.WithToolHandlerMiddleware(mcpauth.ScopeMiddleware(toolScopes)))

	s.AddTool(mcp.NewTool("expense_create",
		mcp.WithDescription("Record a new expense."),
		mcp.WithNumber("amount", mcp.Required(), mcp.Description("Amount, must be > 0")),
		mcp.WithString("currency", mcp.Description("Currency code, e.g. CNY/USD")),
		mcp.WithString("description", mcp.Description("Description")),
		mcp.WithArray("tags", mcp.Description("Tags")),
		mcp.WithString("source", mcp.Description("Source label")),
		mcp.WithString("happened_at", mcp.Description("RFC3339 timestamp, default now")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		amount := 0.0
		if v, ok := args["amount"].(float64); ok {
			amount = v
		}
		e, err := svc.Create(ctx, owner, CreateRequest{
			Amount:      amount,
			Currency:    mcpauth.StrArg(args, "currency"),
			Description: mcpauth.StrArg(args, "description"),
			Tags:        mcpauth.StrSliceArg(args, "tags"),
			Source:      mcpauth.StrArg(args, "source"),
			HappenedAt:  mcpauth.StrArg(args, "happened_at"),
		})
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(e)
	})

	s.AddTool(mcp.NewTool("expense_get",
		mcp.WithDescription("Get an expense by ID."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Expense ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		e, err := svc.Get(ctx, owner.Account(), mcpauth.StrArg(req.GetArguments(), "id"))
		if err != nil {
			return mcpauth.ErrResult("not found"), nil
		}
		return mcpauth.JSONResult(e)
	})

	s.AddTool(mcp.NewTool("expense_list",
		mcp.WithDescription("List expenses, optionally filtered by tags/date range/search."),
		mcp.WithArray("tags", mcp.Description("Filter by tags")),
		mcp.WithString("start", mcp.Description("RFC3339 range start")),
		mcp.WithString("end", mcp.Description("RFC3339 range end")),
		mcp.WithString("q", mcp.Description("Search in description")),
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
			Search: mcpauth.StrArg(args, "q"),
			Start:  mcpauth.StrArg(args, "start"),
			End:    mcpauth.StrArg(args, "end"),
			Limit:  mcpauth.IntArg(args, "limit"),
			Offset: mcpauth.IntArg(args, "offset"),
		})
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(list)
	})

	s.AddTool(mcp.NewTool("expense_summary",
		mcp.WithDescription("Get aggregated expense summary (total, by tag) for a date range."),
		mcp.WithString("start", mcp.Description("RFC3339 range start")),
		mcp.WithString("end", mcp.Description("RFC3339 range end")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		summary, err := svc.Summary(ctx, owner.Account(), ListParams{
			Start: mcpauth.StrArg(args, "start"),
			End:   mcpauth.StrArg(args, "end"),
		})
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(summary)
	})

	s.AddTool(mcp.NewTool("expense_update",
		mcp.WithDescription("Update an expense's fields."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Expense ID")),
		mcp.WithNumber("amount", mcp.Description("New amount")),
		mcp.WithString("description", mcp.Description("New description")),
		mcp.WithArray("tags", mcp.Description("Replace tags")),
		mcp.WithString("happened_at", mcp.Description("RFC3339 timestamp")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		r := UpdateRequest{
			Tags:   mcpauth.StrSliceArg(args, "tags"),
			Amount: mcpauth.FloatPtrArg(args, "amount"),
		}
		if v, ok := args["description"].(string); ok {
			r.Description = &v
		}
		if v, ok := args["happened_at"].(string); ok {
			r.HappenedAt = &v
		}
		e, err := svc.Update(ctx, owner.Account(), mcpauth.StrArg(args, "id"), r)
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(e)
	})

	s.AddTool(mcp.NewTool("expense_delete",
		mcp.WithDescription("Delete an expense by ID."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Expense ID")),
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
