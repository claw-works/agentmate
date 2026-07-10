// Package mcpauth provides shared API-key authentication and scope
// enforcement for per-module MCP servers (todo, notes, reports, bookmarks,
// expenses, skills). Each module mounts its own MCP endpoint but reuses this
// package instead of duplicating context plumbing and scope checks.
package mcpauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/wellxie/agentmate/internal/auth"
	"github.com/wellxie/agentmate/internal/ownership"
)

type accountIDKeyType struct{}
type userIDKeyType struct{}
type apiKeyIDKeyType struct{}
type scopesKeyType struct{}

var accountIDKey = accountIDKeyType{}
var userIDKey = userIDKeyType{}
var apiKeyIDKey = apiKeyIDKeyType{}
var scopesKey = scopesKeyType{}

// HTTPContextFunc extracts the API key from the request (X-Api-Key header,
// api_key query param, or Authorization: Bearer) and validates it, storing
// the resulting owner/scopes in the request context for tool handlers.
func HTTPContextFunc(authSvc *auth.Service) server.HTTPContextFunc {
	return func(ctx context.Context, r *http.Request) context.Context {
		apiKey := r.Header.Get("X-Api-Key")
		if apiKey == "" {
			apiKey = r.URL.Query().Get("api_key")
		}
		if apiKey == "" {
			if bearer := r.Header.Get("Authorization"); strings.HasPrefix(bearer, "Bearer ") {
				apiKey = strings.TrimPrefix(bearer, "Bearer ")
			}
		}
		if apiKey == "" {
			return ctx
		}
		ak, err := authSvc.ValidateAPIKey(ctx, apiKey)
		if err != nil {
			return ctx
		}
		ctx = context.WithValue(ctx, accountIDKey, ak.AccountID)
		ctx = context.WithValue(ctx, userIDKey, ak.UserID)
		ctx = context.WithValue(ctx, apiKeyIDKey, ak.ID)
		return context.WithValue(ctx, scopesKey, ak.Scopes)
	}
}

// OwnerFromContext extracts the authenticated ownership.Owner from context.
func OwnerFromContext(ctx context.Context) (ownership.Owner, bool) {
	userID, ok := ctx.Value(userIDKey).(string)
	if !ok || userID == "" {
		return ownership.Owner{}, false
	}
	accountID, _ := ctx.Value(accountIDKey).(string)
	if accountID == "" {
		accountID = userID
	}
	var keyID *string
	if id, ok := ctx.Value(apiKeyIDKey).(string); ok && id != "" {
		keyID = &id
	}
	return ownership.Owner{AccountID: accountID, UserID: userID, KeyID: keyID}, true
}

func scopesFromContext(ctx context.Context) ([]string, bool) {
	scopes, ok := ctx.Value(scopesKey).([]string)
	return scopes, ok
}

// ScopeMiddleware enforces a required scope per tool name, looked up from
// requiredScopes. Tools without an entry are rejected.
func ScopeMiddleware(requiredScopes map[string]string) server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if _, ok := OwnerFromContext(ctx); !ok {
				return ErrResult("unauthorized"), nil
			}
			scopes, ok := scopesFromContext(ctx)
			if !ok {
				return ErrResult("unauthorized"), nil
			}
			required, ok := requiredScopes[req.Params.Name]
			if !ok {
				return ErrResult("missing scope policy for tool: " + req.Params.Name), nil
			}
			if !auth.HasScope(&auth.APIKey{Scopes: scopes}, required) {
				return ErrResult("insufficient scope: " + required), nil
			}
			return next(ctx, req)
		}
	}
}

// Arg helpers for reading MCP tool call arguments.

func StrArg(args map[string]interface{}, key string) string {
	v, _ := args[key].(string)
	return v
}

func IntArg(args map[string]interface{}, key string) int {
	v, _ := args[key].(float64)
	return int(v)
}

func BoolArg(args map[string]interface{}, key string) bool {
	v, _ := args[key].(bool)
	return v
}

func BoolPtrArg(args map[string]interface{}, key string) *bool {
	v, ok := args[key].(bool)
	if !ok {
		return nil
	}
	return &v
}

func FloatPtrArg(args map[string]interface{}, key string) *float64 {
	v, ok := args[key].(float64)
	if !ok {
		return nil
	}
	return &v
}

func StrSliceArg(args map[string]interface{}, key string) []string {
	raw, ok := args[key].([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// Result helpers for returning MCP tool call results.

func ErrResult(msg string) *mcp.CallToolResult {
	r := mcp.NewToolResultText(msg)
	r.IsError = true
	return r
}

func JSONResult(v interface{}) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return ErrResult(fmt.Sprintf("json marshal: %v", err)), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}
