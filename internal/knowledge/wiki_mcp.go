package knowledge

import (
	"context"
	"errors"

	"github.com/claw-works/agentmate/internal/mcpauth"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerWikiTools exposes the K3 wiki layer over MCP.
//
// The tool descriptions state plainly that pages are model-generated and that
// citations are the only way to verify a claim. An agent that does not know this
// will quote a synthesis page as if it were a primary source.
func registerWikiTools(s *server.MCPServer, svc *Service) {
	// knowledge_compile
	s.AddTool(mcp.NewTool("knowledge_compile",
		mcp.WithDescription("Queue a wiki compilation for a knowledge source's active raw revision. Returns immediately with a queued build — compilation takes minutes, so poll knowledge_build_get until status leaves queued/running. On success the build is activated automatically if checks pass; a build that fails checks writes no pages."),
		mcp.WithString("source_id", mcp.Required(), mcp.Description("Registered knowledge source ID; it must already have an active synced revision")),
		mcp.WithString("mode", mcp.Description("full (default) recompiles the whole wiki. incremental diffs the raw sources against the previous succeeded build, recompiles only the pages those changes touch, and carries the rest over. incremental requires an existing succeeded build and is refused rather than downgraded to full if there is none.")),
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
		response, err := svc.EnqueueCompile(ctx, owner, request)
		if err != nil {
			if errors.Is(err, ErrCompilerUnavailable) {
				return mcpauth.ErrResult("wiki compilation is not available: no compiler model is configured on this deployment"), nil
			}
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(response)
	})

	// knowledge_queue_stats
	s.AddTool(mcp.NewTool("knowledge_queue_stats",
		mcp.WithDescription("Report the wiki compile queue for this account: builds waiting, running, waiting on a retry backoff, and the age of the oldest waiting build. Use this to tell queue wait apart from a stuck build."),
	), func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		stats, err := svc.QueueStats(ctx, owner.Account())
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(stats)
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

// registerWikiRetrievalTools exposes the K3.6 entry layer.
//
// The descriptions insist on the second level of the query. A wiki page is model-written
// synthesis, so an agent that quotes it without following a citation is quoting a
// paraphrase of unknown fidelity — and it has no way to know that unless told.
func registerWikiRetrievalTools(s *server.MCPServer, svc *Service) {
	// knowledge_wiki_search
	s.AddTool(mcp.NewTool("knowledge_wiki_search",
		mcp.WithDescription("Search compiled wiki pages — the synthesised, cross-referenced layer above raw documents. Start here rather than with knowledge_search: a wiki page already combines what several documents say and records where each claim came from. Each hit carries the page's citations and typed links. The page text is model-generated, so treat only the cited source documents as authoritative: follow a citation with knowledge_document_get to verify a claim, and check contradicts links before relying on one. Only the currently active wiki build of each source is searchable."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Natural-language query; Chinese and identifier queries both work")),
		mcp.WithNumber("top_k", mcp.Description("Number of pages to return (default 10, max 50)")),
		mcp.WithString("domain", mcp.Description("Optional owning domain filter, for example \"platform\"")),
		mcp.WithBoolean("include_content", mcp.Description("Return full page bodies instead of snippets; off by default because pages are long")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		args := req.GetArguments()
		topK, err := strictMCPInteger(args, "top_k", 0, 1, maxSearchTopK)
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		response, searchErr := svc.SearchWiki(ctx, owner, SearchWikiRequest{
			Query:          mcpauth.StrArg(args, "query"),
			TopK:           topK,
			Domain:         mcpauth.StrArg(args, "domain"),
			IncludeContent: mcpauth.BoolArg(args, "include_content"),
		})
		if searchErr != nil {
			return mcpauth.ErrResult(searchErr.Error()), nil
		}
		return mcpauth.JSONResult(response)
	})

	// knowledge_wiki_index
	s.AddTool(mcp.NewTool("knowledge_wiki_index",
		mcp.WithDescription("Index the active wiki build of a source, or of every source with an active build, into the knowledge_wiki retrieval namespace. Needed after a compile or a rollback: search filters on the active build, so an unindexed wiki returns no hits rather than stale ones. Rows from earlier builds of the same source are removed."),
		mcp.WithString("source_id", mcp.Description("Optional; empty indexes every source with an active wiki build")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		response, err := svc.IndexActiveWikiBuilds(ctx, owner, mcpauth.StrArg(req.GetArguments(), "source_id"))
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(response)
	})

	// knowledge_wiki_status
	s.AddTool(mcp.NewTool("knowledge_wiki_status",
		mcp.WithDescription("Report, per source, which wiki build is active and which one the search index reflects. A stale entry means the active wiki is not searchable yet, which looks identical to a wiki with nothing to say — this is how to tell the two apart."),
		mcp.WithString("source_id", mcp.Description("Optional source ID filter")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		statuses, err := svc.WikiIndexStatuses(ctx, owner.Account(), mcpauth.StrArg(req.GetArguments(), "source_id"))
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(map[string]any{"items": statuses, "total": len(statuses)})
	})
}

// ─── K3.7: lint ───
//
// The tool descriptions say plainly that lint blocks nothing. An agent that mistakes
// findings for failures will refuse to use a wiki that is serving perfectly well, and an
// agent that never learns a page is stale will quote it as current. Both errors come from
// the same missing sentence.
func registerWikiLintTools(s *server.MCPServer, svc *Service) {
	// knowledge_wiki_lint
	s.AddTool(mcp.NewTool("knowledge_wiki_lint",
		mcp.WithDescription("Lint the active wiki of a source and return findings. This is advisory and read-only: it never changes a page and never stops a wiki from serving — unlike the compile check, which gates activation. Findings say what deserves attention: pages nothing links to, citations whose source document was removed or rewritten since the build, pages resting on those, recorded contradictions, superseded pages with no pointer to their replacement, mentions_entity links aimed at pages that are not entities, and documents no page cites. A stale_citation finding is the signal to recompile; until then, treat the affected page as possibly out of date."),
		mcp.WithString("source_id", mcp.Required(), mcp.Description("Source whose active wiki build should be linted")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		response, err := svc.LintActiveWiki(ctx, owner, mcpauth.StrArg(req.GetArguments(), "source_id"))
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(response)
	})

	// knowledge_wiki_lint_runs
	s.AddTool(mcp.NewTool("knowledge_wiki_lint_runs",
		mcp.WithDescription("List past lint runs with their finding counts. Lint keeps no per-finding status, so comparing runs is how to tell a problem that persists from one that cleared: a finding present before and after a sync is worth acting on, one that disappeared fixed itself."),
		mcp.WithString("source_id", mcp.Description("Optional source ID filter")),
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
		response, err := svc.ListLintRuns(ctx, owner.Account(), mcpauth.StrArg(args, "source_id"), limit, offset)
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(response)
	})

	// knowledge_wiki_lint_run_get
	s.AddTool(mcp.NewTool("knowledge_wiki_lint_run_get",
		mcp.WithDescription("Fetch one lint run with all of its findings. The run records both the build that was linted and the source revision it was compared against, because staleness is a relation between the two: the same build legitimately yields different findings before and after a sync."),
		mcp.WithString("run_id", mcp.Required(), mcp.Description("Lint run ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		response, err := svc.GetLintRun(ctx, owner.Account(), mcpauth.StrArg(req.GetArguments(), "run_id"))
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(response)
	})
}

// ─── K3.8: review ───
//
// The descriptions have to be explicit that review never gates, and equally explicit that
// its silence is bounded. An agent that reads "clean" as "verified" will trust pages nobody
// examined; an agent that reads "flagged" as "broken" will refuse a wiki that is serving
// correctly. Both come from omitting how much was looked at.
func registerWikiReviewTools(s *server.MCPServer, svc *Service) {
	// knowledge_build_review
	s.AddTool(mcp.NewTool("knowledge_build_review",
		mcp.WithDescription("Run faithfulness review on a committed wiki build: for each page, a model from a different provider than the compiler checks whether the page's claims are supported by the raw documents it cites, judging against the source text rather than the compiler's own excerpts. Findings are one of unsupported, overstated, fabricated_causality or conflated. This never blocks and never changes a page — check is the only gate. Review is capped at a number of pages per build, so read review_pages_examined against review_pages_total: \"clean\" means nothing was found among the pages examined, not that the whole wiki was verified. Re-running replaces the previous findings for that build."),
		mcp.WithString("build_id", mcp.Required(), mcp.Description("Build to review; must have succeeded")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		response, err := svc.ReviewBuild(ctx, owner, mcpauth.StrArg(req.GetArguments(), "build_id"))
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(response)
	})

	// knowledge_build_review_get
	s.AddTool(mcp.NewTool("knowledge_build_review_get",
		mcp.WithDescription("Fetch the recorded faithfulness verdict for a build and the findings behind it, without running a review. review_status is skipped, clean, partial, flagged or failed; review_note says why review did not run or did not finish, and reviewer_independence records how separated the reviewer was from the compiler. A same-model reviewer is refused outright rather than run, because a model cannot find the mistakes its own priors produced."),
		mcp.WithString("build_id", mcp.Required(), mcp.Description("Build ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, ok := mcpauth.OwnerFromContext(ctx)
		if !ok {
			return mcpauth.ErrResult("unauthorized"), nil
		}
		response, err := svc.GetBuildReview(ctx, owner.Account(), mcpauth.StrArg(req.GetArguments(), "build_id"))
		if err != nil {
			return mcpauth.ErrResult(err.Error()), nil
		}
		return mcpauth.JSONResult(response)
	})
}
