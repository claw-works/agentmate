package knowledge

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/claw-works/agentmate/internal/llm"
)

// ─── K3.4: incremental compilation ───
//
// Full compilation emits the entire wiki in one model reply, which puts a hard
// ceiling on corpus size: the output budget has already been raised twice (4096 →
// 16384 → 32768) and there is no further headroom to buy. Incremental compilation is
// the way past that ceiling, not a cost optimisation bolted on afterwards.
//
// The shape is: diff the raw sources, resolve which pages that touches, ask the model
// only about those, and copy everything else forward. The impact set is computed from
// the database rather than judged by the model — an under-estimate would leave pages
// asserting things their sources no longer say, and nothing downstream would notice.

const incrementalSystemPrompt = `You are a disciplined wiki maintainer updating an existing knowledge base.

The wiki already exists. You are given only the source documents that changed, were
added or were removed, plus the current text of the pages affected by those changes.
Every other page is being kept exactly as it is and you will not see it.

Your job is to rewrite the affected pages so they match the new sources, and to add
pages for genuinely new material. Do not restate the whole knowledge base.

Rules you must follow:
1. Every factual claim must be supported by a citation naming the source document
   path it came from. Never cite a path you were not given as a current document.
2. Do not invent facts. If the sources do not say something, leave it out.
3. You may link to any path in the "existing page paths" list even though you cannot
   see those pages. Never link to a path that is neither in that list nor among the
   pages you are returning: the build rejects a link that does not resolve.
4. Do not rename a page you were asked to rewrite. Its path is how the rest of the
   wiki refers to it, and renaming it breaks links you cannot see. Emit it under the
   same path.
5. If a page's sources were removed entirely and nothing supports it any more, return
   it with an empty "content" and set "delete": true. Do not silently drop it — a page
   you simply omit is treated as unchanged, not as deleted.
6. When two sources disagree, record a "contradicts" link between the pages with a
   note stating the specific disagreement.
7. Write in the language of the source documents.
8. Page paths must start with "wiki/" and end with ".md". Never write "wiki/index.md"
   or "wiki/log.md"; the platform generates those.

Reply with a single JSON object and nothing else:
{
  "pages": [
    {
      "path": "wiki/<name>.md",
      "kind": "summary" | "entity" | "concept" | "overview" | "synthesis",
      "title": "<short title>",
      "content": "<markdown body>",
      "delete": false,
      "citations": [
        {"document_path": "<exact source path>", "heading_path": "<optional section>",
         "claim": "<the claim this supports>", "excerpt": "<short quote from the source>"}
      ],
      "links": [
        {"target_path": "wiki/<other>.md",
         "link_type": "references" | "contradicts" | "supersedes" | "elaborates" | "mentions_entity",
         "note": "<why, required for contradicts and supersedes>"}
      ]
    }
  ]
}`

// buildIncrementalPrompt renders only the delta.
//
// Two things in here are load-bearing. The changed documents are the material to work
// from, and the list of every existing page path is what lets the model link into
// pages it cannot see — without it, check's link-closure rule would fail any
// incremental build that referenced the untouched part of the wiki.
func buildIncrementalPrompt(
	profile ProfileVersion, manifest Manifest,
	diff RevisionDiff, changedDocuments, contextDocuments []KnowledgeDocument,
	affectedPages []WikiPage, allPagePaths []string, perDocumentChars int,
) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Knowledge base: %s\n", manifest.Name)
	if manifest.Description != "" {
		fmt.Fprintf(&builder, "Description: %s\n", manifest.Description)
	}
	fmt.Fprintf(&builder, "Citation policy: %s\n", profile.CitationPolicy)
	fmt.Fprintf(&builder, "Allowed page kinds: %s\n", strings.Join(profile.AllowedPageKinds, ", "))
	fmt.Fprintf(&builder, "Allowed link types: %s\n", strings.Join(profile.AllowedLinkTypes, ", "))
	if profile.Instructions != "" {
		fmt.Fprintf(&builder, "\nProfile instructions:\n%s\n", profile.Instructions)
	}

	builder.WriteString("\n## What changed in the sources\n")
	writeList := func(label string, paths []string) {
		if len(paths) == 0 {
			return
		}
		fmt.Fprintf(&builder, "\n%s:\n", label)
		for _, path := range paths {
			fmt.Fprintf(&builder, "- %s\n", path)
		}
	}
	writeList("Added", diff.Added)
	writeList("Changed", diff.Changed)
	// Removed documents are named without content, because there is none. A page
	// citing only removed documents is the case rule 5 exists for.
	writeList("Removed (no longer exist; any claim resting only on these is unsupported)", diff.Removed)

	builder.WriteString("\n## Current text of the added and changed documents\n")
	for _, document := range changedDocuments {
		content := document.ContentSnapshot
		truncated := false
		if perDocumentChars > 0 && utf8.RuneCountInString(content) > perDocumentChars {
			content = string([]rune(content)[:perDocumentChars])
			truncated = true
		}
		fmt.Fprintf(&builder, "\n--- path: %s ---\n%s\n", document.Path, content)
		if truncated {
			fmt.Fprintf(&builder, "[document truncated at %d characters]\n", perDocumentChars)
		}
	}

	if len(contextDocuments) > 0 {
		// The affected pages also rest on documents that did not change. Without them the
		// compiler cannot tell which claims came from where and drops the ones it cannot
		// see: a page citing changed A and unchanged B silently loses B.
		builder.WriteString("\n## Unchanged documents the affected pages still rely on\n")
		for _, document := range contextDocuments {
			content := document.ContentSnapshot
			if perDocumentChars > 0 && utf8.RuneCountInString(content) > perDocumentChars {
				content = string([]rune(content)[:perDocumentChars])
				fmt.Fprintf(&builder, "\n--- path: %s ---\n%s\n[document truncated at %d characters]\n",
					document.Path, content, perDocumentChars)
				continue
			}
			fmt.Fprintf(&builder, "\n--- path: %s ---\n%s\n", document.Path, content)
		}
	}

	builder.WriteString("\n## Pages you must rewrite\n")
	if len(affectedPages) == 0 {
		builder.WriteString("\nNone. Only add pages for the new material above.\n")
	}
	for _, page := range affectedPages {
		fmt.Fprintf(&builder, "\n--- page: %s (kind: %s, title: %s) ---\n%s\n",
			page.Path, page.Kind, page.Title, page.Content)
		// The page's current citations and links, so a rewrite can preserve the ones that
		// are still valid. Without them the compiler has to guess which claims were
		// sourced where, and its only safe move is to drop what it cannot account for.
		if len(page.Citations) > 0 {
			builder.WriteString("\nIts current citations:\n")
			for _, citation := range page.Citations {
				fmt.Fprintf(&builder, "- %s", citation.DocumentPath)
				if citation.HeadingPath != "" {
					fmt.Fprintf(&builder, " (%s)", citation.HeadingPath)
				}
				if citation.Claim != "" {
					fmt.Fprintf(&builder, " — %s", citation.Claim)
				}
				builder.WriteString("\n")
			}
		}
		if len(page.Links) > 0 {
			builder.WriteString("\nIts current links:\n")
			for _, link := range page.Links {
				fmt.Fprintf(&builder, "- %s -> %s", link.LinkType, link.TargetPath)
				if link.Note != "" {
					fmt.Fprintf(&builder, " (%s)", link.Note)
				}
				builder.WriteString("\n")
			}
		}
	}

	// Every path, including the ones being rewritten, so the model can link freely
	// within the wiki without guessing what exists.
	builder.WriteString("\n## Existing page paths (link to these freely; they are kept unless listed above)\n")
	for _, path := range allPagePaths {
		fmt.Fprintf(&builder, "- %s\n", path)
	}
	return builder.String()
}

// compileIncrementalWithModel runs one incremental call.
func (s *Service) compileIncrementalWithModel(
	ctx context.Context, profile ProfileVersion, manifest Manifest,
	diff RevisionDiff, changedDocuments, contextDocuments []KnowledgeDocument,
	affectedPages []WikiPage, allPagePaths []string, currentDocumentIDs map[string]string,
) (written []WikiPage, deleted []string, rejected []string, usage llm.Usage, err error) {
	prompt := buildIncrementalPrompt(profile, manifest, diff, changedDocuments, contextDocuments,
		affectedPages, allPagePaths, s.compilePerDocumentChars())

	completion, err := s.compiler.Complete(ctx, []llm.Message{
		{Role: "system", Content: incrementalSystemPrompt},
		{Role: "user", Content: prompt},
	}, llm.WithJSONObject())
	if err != nil {
		return nil, nil, nil, llm.Usage{}, err
	}
	usage = completion.Usage

	output, err := parseCompilerOutput(completion.Content)
	if err != nil {
		return nil, nil, nil, usage, err
	}

	written = make([]WikiPage, 0, len(output.Pages))
	deleted = make([]string, 0)
	rejected = make([]string, 0)
	for _, raw := range output.Pages {
		path := strings.TrimSpace(raw.Path)
		if path == "" {
			continue
		}
		if _, isReserved := reservedPagePaths[path]; isReserved {
			rejected = append(rejected, path)
			continue
		}
		if raw.Delete {
			deleted = append(deleted, path)
			continue
		}
		page := WikiPage{
			Path:    path,
			Kind:    strings.TrimSpace(strings.ToLower(raw.Kind)),
			Title:   strings.TrimSpace(raw.Title),
			Content: strings.TrimSpace(raw.Content),
		}
		for _, citation := range raw.Citations {
			documentPath := strings.TrimSpace(citation.DocumentPath)
			if documentPath == "" {
				continue
			}
			item := PageCitation{
				DocumentPath: documentPath,
				HeadingPath:  strings.TrimSpace(citation.HeadingPath),
				Claim:        strings.TrimSpace(citation.Claim),
				Excerpt:      strings.TrimSpace(citation.Excerpt),
			}
			// Resolved against the whole current revision, not just the changed
			// documents: a rewritten page legitimately still cites material that did
			// not change.
			if id, ok := currentDocumentIDs[documentPath]; ok {
				item.DocumentID = &id
			}
			page.Citations = append(page.Citations, item)
		}
		for _, link := range raw.Links {
			target := strings.TrimSpace(link.TargetPath)
			if target == "" {
				continue
			}
			page.Links = append(page.Links, PageLink{
				TargetPath: target,
				LinkType:   strings.TrimSpace(strings.ToLower(link.LinkType)),
				Note:       strings.TrimSpace(link.Note),
			})
		}
		page.ContentHash = hashContent(page.Content)
		written = append(written, page)
	}
	sort.Slice(written, func(i, j int) bool { return written[i].Path < written[j].Path })
	return written, deleted, rejected, usage, nil
}

// mergeIncremental combines pages carried over from the parent with what the model
// just produced.
//
// Freshly compiled pages win over reused ones at the same path, which is the whole
// point of asking. Deletions are applied last so a model that both rewrote and
// deleted a path ends up with it deleted — contradictory instructions resolve toward
// removal, since keeping a page the model declared unsupported would leave a claim
// standing that nothing backs.
//
// Note what this does not do: it does not repair links. If removing a page leaves a
// reused page pointing at nothing, check fails the build. That is deliberate — the
// alternative is silently deleting the link, which turns a detectable inconsistency
// into an invisible hole in the graph.
func mergeIncremental(reused, compiled []WikiPage, deleted []string) (merged []WikiPage, reusedCount int) {
	byPath := make(map[string]WikiPage, len(reused)+len(compiled))
	order := make([]string, 0, len(reused)+len(compiled))
	reusedPaths := make(map[string]struct{}, len(reused))

	for _, page := range reused {
		if _, seen := byPath[page.Path]; !seen {
			order = append(order, page.Path)
		}
		byPath[page.Path] = page
		reusedPaths[page.Path] = struct{}{}
	}
	for _, page := range compiled {
		if _, seen := byPath[page.Path]; !seen {
			order = append(order, page.Path)
		}
		byPath[page.Path] = page
		// A recompiled page is no longer derived from the parent, even if the parent
		// had one at the same path.
		delete(reusedPaths, page.Path)
	}
	for _, path := range deleted {
		delete(byPath, path)
		delete(reusedPaths, path)
	}

	sort.Strings(order)
	merged = make([]WikiPage, 0, len(byPath))
	for _, path := range order {
		if page, ok := byPath[path]; ok {
			merged = append(merged, page)
		}
	}
	return merged, len(reusedPaths)
}

// pagePathSet is a small helper used to decide reuse membership.
func pagePathSet(paths []string) map[string]struct{} {
	set := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		set[path] = struct{}{}
	}
	return set
}

// runIncremental produces the page set for an incremental build.
//
// Returns the merged pages, the reserved paths it refused, token usage, how many
// pages were carried over untouched, and the plan it followed.
func (s *Service) runIncremental(
	ctx context.Context, accountID string, build *BuildRevision,
	profile ProfileVersion, manifest Manifest, documents []KnowledgeDocument,
	addEvent func(eventType, pagePath, detail string),
) ([]WikiPage, []string, llm.Usage, int, *IncrementalPlan, error) {
	if build.ParentBuildID == nil {
		return nil, nil, llm.Usage{}, 0, nil, ErrNoParentBuild
	}
	parentID := *build.ParentBuildID
	parent, err := s.repo.GetBuild(ctx, accountID, parentID)
	if err != nil {
		return nil, nil, llm.Usage{}, 0, nil, err
	}

	diff, err := s.repo.DiffRevisionDocuments(ctx, accountID, parent.SourceRevisionID, build.SourceRevisionID)
	if err != nil {
		return nil, nil, llm.Usage{}, 0, nil, err
	}
	parentPages, err := s.repo.LoadBuildPages(ctx, accountID, parentID)
	if err != nil {
		return nil, nil, llm.Usage{}, 0, nil, err
	}
	// index and log are regenerated from the merged result, so carrying the parent's
	// copies forward would produce an index describing the wrong wiki.
	carried := make([]WikiPage, 0, len(parentPages))
	carriedPaths := make([]string, 0, len(parentPages))
	for _, page := range parentPages {
		if _, reserved := reservedPagePaths[page.Path]; reserved {
			continue
		}
		carried = append(carried, page)
		carriedPaths = append(carriedPaths, page.Path)
	}
	sort.Strings(carriedPaths)

	rawImpacted, err := s.repo.ImpactedPagePaths(ctx, accountID, parentID, diff.Touched())
	if err != nil {
		return nil, nil, llm.Usage{}, 0, nil, err
	}
	// The index links to every content page, so the one-hop rule drags it in whenever
	// anything is impacted. Platform-generated pages are regenerated on every build
	// regardless, so scheduling them is meaningless — and recording them as scheduled
	// would claim the compiler was asked for a page it is forbidden to write.
	impacted := make([]string, 0, len(rawImpacted))
	for _, path := range rawImpacted {
		if _, reserved := reservedPagePaths[path]; reserved {
			continue
		}
		impacted = append(impacted, path)
	}
	plan := &IncrementalPlan{
		ParentBuildID: parentID, RevisionDiff: *diff,
		ScheduledPaths:  impacted,
		RecompiledPaths: make([]string, 0, len(impacted)),
		ReusedPaths:     make([]string, 0, len(carriedPaths)),
		DeletedPaths:    make([]string, 0),
	}

	addEvent(BuildEventPlanned, "", fmt.Sprintf(
		"raw diff against build %s: %d added, %d changed, %d removed, %d unchanged; %d of %d pages impacted",
		shortID(parentID), len(diff.Added), len(diff.Changed), len(diff.Removed), diff.Unchanged,
		len(impacted), len(carried)))

	// A parent built by a different compiler, prompt, model or profile is not something
	// this build can be an increment of. The raw diff would be empty while every page
	// still needs rewriting, and reusing them would stamp this build's provenance onto
	// text that model never produced — which is exactly what happened before this check
	// existed: a build recorded model=qwen while every page came from a deepseek run.
	// Refused rather than silently widened to a full rebuild.
	if parent.Model != build.Model || parent.CompilerVersion != build.CompilerVersion ||
		parent.PromptVersion != build.PromptVersion || parent.ProfileVersionID != build.ProfileVersionID {
		return nil, nil, llm.Usage{}, 0, plan, fmt.Errorf(
			"%w: parent build %s was produced by a different compiler identity "+
				"(model %q/%q, prompt %q/%q, compiler %q/%q, profile %q/%q); compile with mode=full",
			ErrIncompatibleParent, shortID(parentID),
			parent.Model, build.Model, parent.PromptVersion, build.PromptVersion,
			parent.CompilerVersion, build.CompilerVersion, parent.ProfileVersionID, build.ProfileVersionID)
	}

	// Nothing changed in the sources. Every page is carried over and the model is
	// never called — the cheapest correct outcome, and worth stating rather than
	// hiding, because a build that cost nothing looks suspicious otherwise.
	currentDocumentIDs := make(map[string]string, len(documents))
	for _, document := range documents {
		currentDocumentIDs[document.Path] = document.ID
	}

	if diff.IsEmpty() {
		plan.ReusedPaths = carriedPaths
		for _, page := range carried {
			addEvent(BuildEventPageReused, page.Path, "sources unchanged")
		}
		reused := prepareReusedPages(ReuseInput{
			Pages: carried, ParentBuildID: parentID, CurrentDocumentIDs: currentDocumentIDs})
		return reused, nil, llm.Usage{}, len(reused), plan, nil
	}

	impactedSet := pagePathSet(impacted)
	affected := make([]WikiPage, 0, len(impacted))
	reusable := make([]WikiPage, 0, len(carried))
	for _, page := range carried {
		if _, hit := impactedSet[page.Path]; hit {
			affected = append(affected, page)
			continue
		}
		reusable = append(reusable, page)
		plan.ReusedPaths = append(plan.ReusedPaths, page.Path)
	}

	// Added documents with no impacted page still need compiling: that is new material
	// the wiki does not cover yet.
	changedPaths := pagePathSet(append(append([]string{}, diff.Added...), diff.Changed...))
	changedDocuments := make([]KnowledgeDocument, 0, len(changedPaths))
	for _, document := range documents {
		if _, hit := changedPaths[document.Path]; hit {
			changedDocuments = append(changedDocuments, document)
		}
	}
	// The affected pages also rest on documents that did not change. Rewriting a page
	// without them means the compiler cannot tell which of its claims came from where,
	// so it silently drops the ones it cannot see — a page citing changed A and
	// unchanged B loses B.
	contextDocuments := collectCitedContext(affected, changedPaths, documents)

	compiled, deleted, rejected, usage, err := s.compileIncrementalWithModel(
		ctx, profile, manifest, *diff, changedDocuments, contextDocuments,
		affected, carriedPaths, currentDocumentIDs)
	if err != nil {
		return nil, nil, usage, 0, plan, err
	}

	// The compiler is handed every page path so it can link freely, which also lets it
	// return a page nobody asked about. Out-of-plan writes are dropped here rather than
	// left for check, so the wiki keeps the page the plan promised to keep; check still
	// fails the build, because a compiler ignoring its scope is a defect worth surfacing
	// rather than papering over.
	inScope := pagePathSet(impacted)
	existing := pagePathSet(carriedPaths)
	kept := make([]WikiPage, 0, len(compiled))
	for _, page := range compiled {
		_, scheduled := inScope[page.Path]
		_, existed := existing[page.Path]
		if !scheduled && existed {
			plan.RejectedPaths = append(plan.RejectedPaths, page.Path)
			addEvent(BuildEventPageRejected, page.Path,
				"rewritten without being in the plan; the source diff did not touch it")
			continue
		}
		kept = append(kept, page)
	}
	compiled = kept
	keptDeletions := make([]string, 0, len(deleted))
	for _, path := range deleted {
		if _, scheduled := inScope[path]; !scheduled {
			plan.RejectedPaths = append(plan.RejectedPaths, path)
			addEvent(BuildEventPageRejected, path,
				"deletion requested without being in the plan; the source diff did not touch it")
			continue
		}
		keptDeletions = append(keptDeletions, path)
	}
	deleted = keptDeletions

	for _, page := range compiled {
		plan.RecompiledPaths = append(plan.RecompiledPaths, page.Path)
		addEvent(BuildEventPageWritten, page.Path, fmt.Sprintf("%s, %d citations, %d links",
			page.Kind, len(page.Citations), len(page.Links)))
	}
	plan.DeletedPaths = append(plan.DeletedPaths, deleted...)
	for _, path := range deleted {
		addEvent(BuildEventPageDeleted, path, "sources no longer support this page")
	}
	for _, page := range reusable {
		addEvent(BuildEventPageReused, page.Path, "not impacted by the source diff")
	}

	reusedPrepared := prepareReusedPages(ReuseInput{
		Pages: reusable, ParentBuildID: parentID, CurrentDocumentIDs: currentDocumentIDs})
	merged, reusedCount := mergeIncremental(reusedPrepared, compiled, deleted)
	// A scheduled page the compiler neither rewrote nor deleted is deliberately NOT
	// reinstated from the parent. Keeping its old text would leave a claim standing that
	// its source no longer supports, and no structural rule can see that: the citation
	// path still exists, so every check passes while the page is quietly wrong. check's
	// incremental_coverage rule fails the build instead. Failing loudly is the same
	// choice made for a half-written wiki, applied to a half-updated one.

	sort.Strings(plan.ReusedPaths)
	sort.Strings(plan.RecompiledPaths)
	sort.Strings(plan.DeletedPaths)
	sort.Strings(plan.RejectedPaths)
	return merged, rejected, usage, reusedCount, plan, nil
}

// collectCitedContext gathers the unchanged documents that the affected pages cite.
//
// Bounded by the impact set rather than the corpus: only documents actually cited by a
// page being rewritten are included, so this does not reintroduce the full-corpus prompt
// that incremental compilation exists to avoid.
func collectCitedContext(
	affected []WikiPage, changedPaths map[string]struct{}, documents []KnowledgeDocument,
) []KnowledgeDocument {
	needed := make(map[string]struct{})
	for _, page := range affected {
		for _, citation := range page.Citations {
			if _, alsoChanged := changedPaths[citation.DocumentPath]; alsoChanged {
				// Already supplied in full under "what changed".
				continue
			}
			needed[citation.DocumentPath] = struct{}{}
		}
	}
	if len(needed) == 0 {
		return nil
	}
	context := make([]KnowledgeDocument, 0, len(needed))
	for _, document := range documents {
		if _, hit := needed[document.Path]; hit {
			context = append(context, document)
		}
	}
	sort.Slice(context, func(i, j int) bool { return context[i].Path < context[j].Path })
	return context
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
