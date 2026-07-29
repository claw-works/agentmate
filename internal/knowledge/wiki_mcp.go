package knowledge

import (
	"context"
	"errors"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/wellxie/agentmate/internal/mcpauth"
)

// registerWikiTools exposes the K3 wiki layer over MCP.
//
// The tool descriptions state plainly that pages are model-generated and that
// citations are the only way to verify a claim. An agent that does not know this
// will quote a synthesis page as if it were a primary source.
func registerWikiTools(s *server.MCPServer, svc *Service) {
	// knowledge_compile
	s.AddTool(mcp.NewTool("knowledge_compile",
		mcp.WithDescription("Compile a knowledge source's active raw revision into an interlinked wiki. Runs deterministic checks and, if they pass, activates the resulting build automatically. Returns the build with its provenance; a build that fails checks writes no pages."),
		mcp.WithString("source_id", mcp.Required(), mcp.Description("Registered knowledge source ID; it must already have an active synced revision")),
		mcp.WithString("mode", mcp.Description("full (default). incremental is not implemented yet and is rejected rather than downgraded")),
		mcp.WithBoolean("force", mcp.Description("Recompile even when a succeeded build already exists for the same inputs")),
		mcp.WithBoolean("activate", mcp.Description("Move the active wiki pointer to this build when checks pass (default true)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		request := CompileRequest{
			SourceID: mcpauth.StrArg(args, "source_id"),
			Mode:     mcpauth.StrArg(args, "mode"),
			Force:    mcpauth.BoolArg(args, "force"),
		}
		// Only honour an explicit false: an absent argument must keep the
		// automatic-activation default rather than silently disabling it.
		if raw, present := args["activate"]; present {
			if value, isBool := raw.(bool); isBool {
				request.Activate = &value
			}
		}
		response, err := svc.Compile(ctx, owner, request)
		if err != nil {
			if errors.Is(err, ErrCompilerUnavailable) {
				return mcpauth.ErrResult("wiki compilation is not available: no compiler model is configured on this deployment"), nil
			}
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(response)
	})

	// knowledge_builds_list
	s.AddTool(mcp.NewTool("knowledge_builds_list",
		mcp.WithDescription("List wiki builds, newest first. Builds are immutable and retained, so this is the history of how the wiki changed. is_active marks the build currently serving as the wiki."),
		mcp.WithString("source_id", mcp.Description("Optional knowledge source ID filter")),
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
		response, err := svc.ListBuilds(ctx, owner.Account(), mcpauth.StrArg(args, "source_id"), limit, offset)
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(response)
	})

	// knowledge_build_get
	s.AddTool(mcp.NewTool("knowledge_build_get",
		mcp.WithDescription("Get one wiki build with full provenance: raw revision and package hash, profile version, compiler and prompt versions, model, reviewer independence, check verdict and token spend."),
		mcp.WithString("build_id", mcp.Required(), mcp.Description("Wiki build ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		build, err := svc.GetBuild(ctx, owner.Account(), mcpauth.StrArg(req.GetArguments(), "build_id"))
		if err != nil {
			return mcpauth.ErrResult("not found"), nil
		}
		return mcpauth.JSONResult(build)
	})

	// knowledge_build_pages
	s.AddTool(mcp.NewTool("knowledge_build_pages",
		mcp.WithDescription("List the pages of a wiki build: path, kind, title and content hash, without bodies. Start at wiki/index.md to navigate, then fetch individual pages."),
		mcp.WithString("build_id", mcp.Required(), mcp.Description("Wiki build ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		response, err := svc.ListPages(ctx, owner.Account(), mcpauth.StrArg(req.GetArguments(), "build_id"))
		if err != nil {
			return mcpauth.ErrResult("not found"), nil
		}
		return mcpauth.JSONResult(response)
	})

	// knowledge_page_get
	s.AddTool(mcp.NewTool("knowledge_page_get",
		mcp.WithDescription("Get one wiki page with its body, citations and both inbound and outbound typed links. The body is model-generated: treat only the cited source documents as authoritative, and follow contradicts links before relying on a claim."),
		mcp.WithString("build_id", mcp.Required(), mcp.Description("Wiki build ID")),
		mcp.WithString("path", mcp.Required(), mcp.Description("Page path, for example wiki/index.md")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		page, err := svc.GetPage(ctx, owner.Account(), mcpauth.StrArg(args, "build_id"), mcpauth.StrArg(args, "path"))
		if err != nil {
			return mcpauth.ErrResult("not found"), nil
		}
		return mcpauth.JSONResult(page)
	})

	// knowledge_build_diff
	s.AddTool(mcp.NewTool("knowledge_build_diff",
		mcp.WithDescription("Compare two wiki builds by page path and content hash. Compilation is not reproducible, so this is how a change is explained without re-running it. Omit from to compare against the previous succeeded build."),
		mcp.WithString("to", mcp.Required(), mcp.Description("Newer wiki build ID")),
		mcp.WithString("from", mcp.Description("Older wiki build ID; defaults to the previous succeeded build of the same source")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		diff, err := svc.DiffBuilds(ctx, owner.Account(), mcpauth.StrArg(args, "from"), mcpauth.StrArg(args, "to"))
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(diff)
	})

	// knowledge_build_events
	s.AddTool(mcp.NewTool("knowledge_build_events",
		mcp.WithDescription("Get the ordered build log for a wiki build: what was read, which pages were written, and which checks failed. Rendered as wiki/log.md on succeeded builds."),
		mcp.WithString("build_id", mcp.Required(), mcp.Description("Wiki build ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		buildID := mcpauth.StrArg(req.GetArguments(), "build_id")
		events, err := svc.ListBuildEvents(ctx, owner.Account(), buildID)
		if err != nil {
			return mcpauth.ErrResult("not found"), nil
		}
		return mcpauth.JSONResult(map[string]any{"build_id": buildID, "items": events, "total": len(events)})
	})

	// knowledge_build_activate
	s.AddTool(mcp.NewTool("knowledge_build_activate",
		mcp.WithDescription("Point a knowledge source's wiki at a specific build. This is also how a rollback is performed: activate an earlier build. Only builds that succeeded and passed checks can be activated."),
		mcp.WithString("build_id", mcp.Required(), mcp.Description("Wiki build ID to make active")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		response, err := svc.ActivateBuild(ctx, owner, mcpauth.StrArg(req.GetArguments(), "build_id"))
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(response)
	})
}
