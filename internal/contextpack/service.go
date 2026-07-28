package contextpack

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/wellxie/agentmate/internal/auth"
	"github.com/wellxie/agentmate/internal/knowledge"
	"github.com/wellxie/agentmate/internal/memory"
	"github.com/wellxie/agentmate/internal/ownership"
	"github.com/wellxie/agentmate/internal/skills"
)

var ErrInvalidInput = errors.New("invalid context pack input")

func invalidInputf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, fmt.Sprintf(format, args...))
}

type Service struct {
	providers Providers
}

func NewService(providers Providers) *Service {
	return &Service{providers: providers}
}

// layerScopes lists the read scopes a layer needs. A caller holding only
// skills:r must not receive knowledge or memory content just because the pack is
// one endpoint, so authorisation is enforced per layer rather than once for the
// whole call. Layers with several scopes need all of them; FACTS is special-cased
// because its two halves are independently authorised.
var layerScopes = map[string][]string{
	LayerSkill:     {"skills:r"},
	LayerKnowledge: {"knowledge:r"},
	LayerMemory:    {"memory:r"},
	// TASK carries the caller's own goal statement, which needs no scope. The
	// optional session slice reads the memory journal, so it is gated inside the
	// layer on memory:r.
	LayerTask: nil,
}

func permits(scopes []string, scope string) bool {
	// An empty scope list means full access, matching the platform-wide API key
	// convention; reimplementing the check here would let the two drift apart.
	return auth.HasScope(&auth.APIKey{Scopes: scopes}, scope)
}

// Pack assembles the context. Layers are gathered independently: a failure in
// one is recorded as a warning on that layer and the rest still return, because
// an agent with four layers can act while an agent with an error cannot.
//
// scopes are the caller's granted scopes. Pass nil for full access (JWT or an
// unrestricted key).
func (s *Service) Pack(ctx context.Context, owner ownership.Owner, scopes []string, req PackRequest) (*PackResponse, error) {
	if owner.Account() == "" {
		return nil, invalidInputf("account is required")
	}
	req.Task = strings.TrimSpace(req.Task)
	if req.Task == "" {
		return nil, invalidInputf("task required")
	}
	req.Query = strings.TrimSpace(req.Query)
	req.SkillName = strings.TrimSpace(req.SkillName)
	req.SessionID = strings.TrimSpace(req.SessionID)

	if req.MaxChars == 0 {
		req.MaxChars = DefaultMaxChars
	}
	if req.MaxChars < MinMaxChars || req.MaxChars > MaxMaxChars {
		return nil, invalidInputf("max_chars must be between %d and %d", MinMaxChars, MaxMaxChars)
	}
	if req.TopK < 0 {
		return nil, invalidInputf("top_k must not be negative")
	}
	if req.TopK == 0 {
		req.TopK = 5
	}
	if req.TopK > 20 {
		return nil, invalidInputf("top_k must be at most 20")
	}

	layers, err := resolveLayers(req.Layers)
	if err != nil {
		return nil, err
	}
	budgets := allocate(req.MaxChars, layers)

	// The retrieval query defaults to the task statement. A task written as a
	// long imperative makes a poor query, which is why Query can override it.
	query := req.Query
	if query == "" {
		query = req.Task
	}

	response := &PackResponse{
		Task:      req.Task,
		SessionID: req.SessionID,
		MaxChars:  req.MaxChars,
		Layers:    make([]LayerResult, 0, len(layers)),
	}
	for _, layer := range layers {
		var result LayerResult
		if missing := missingScopes(scopes, layer); missing != "" {
			response.Layers = append(response.Layers, LayerResult{
				Layer:      layer,
				Items:      []Item{},
				CharBudget: budgets[layer],
				Note:       "insufficient scope: " + missing,
			})
			continue
		}
		switch layer {
		case LayerSkill:
			result = s.gatherSkill(ctx, owner, req, query, budgets[layer])
		case LayerKnowledge:
			result = s.gatherKnowledge(ctx, owner, req, query, budgets[layer])
		case LayerMemory:
			result = s.gatherMemory(ctx, owner, req, query, budgets[layer])
		case LayerFacts:
			result = s.gatherFacts(ctx, owner, scopes, req, query, budgets[layer])
		case LayerTask:
			result = s.gatherTask(ctx, owner, scopes, req, budgets[layer])
		}
		result.Layer = layer
		result.CharBudget = budgets[layer]
		if result.Items == nil {
			// Always emit an array. A nil slice marshals to null, forcing every
			// client to handle two shapes for "no items".
			result.Items = []Item{}
		}
		response.Layers = append(response.Layers, result)
		response.CharsUsed += result.CharsUsed
	}

	for _, result := range response.Layers {
		if result.Note != "" && len(result.Items) == 0 {
			response.Warnings = append(response.Warnings, result.Layer+": "+result.Note)
		}
	}
	if req.Render {
		response.Rendered = Render(response)
	}
	return response, nil
}

// ─── SKILL ───

// gatherSkill resolves which skill to use and emits its instructions.
//
// Selection is either a pinned name or the top retrieval hit. Dynamic discovery
// driven by a Skill's declared knowledge contract is K4; pretending to do it
// here would produce a worse selection with the same API shape, making the gap
// invisible.
func (s *Service) gatherSkill(ctx context.Context, owner ownership.Owner, req PackRequest, query string, budget int) LayerResult {
	if s.providers.Skills == nil {
		return LayerResult{Note: "skill provider is not configured"}
	}

	var (
		items []Item
		note  string
	)
	if req.SkillName != "" {
		version, err := s.providers.Skills.GetActiveVersion(ctx, owner.Account(), req.SkillName)
		if err != nil {
			return LayerResult{Note: fmt.Sprintf("pinned skill %q has no active version: %v", req.SkillName, err)}
		}
		items = append(items, Item{
			Layer:   LayerSkill,
			Source:  "skill_instructions",
			Ref:     version.ID,
			Title:   fmt.Sprintf("%s@%s", version.SkillName, version.Version),
			Content: version.Content,
		})
	} else {
		result, err := s.providers.Skills.Search(ctx, owner, skills.SearchSkillsRequest{
			Query: query,
			TopK:  1,
			// Load the L1 body: a pack whose skill layer holds only the card
			// tells the agent which skill to use but not how to run it.
			IncludeContent: true,
		})
		if err != nil {
			return LayerResult{Note: "skill search failed: " + err.Error()}
		}
		if len(result.Items) == 0 {
			return LayerResult{Note: "no skill matched the task"}
		}
		hit := result.Items[0]
		content := hit.Content
		if content == "" {
			// Retrieval can return a card whose L1 body could not be loaded, for
			// example a stale row awaiting recompilation. Fall back to the card
			// and say so rather than emitting an empty skill layer.
			content = renderSkillCard(hit)
			note = "skill instructions unavailable; using the L0 card"
		}
		score := hit.Score
		items = append(items, Item{
			Layer:   LayerSkill,
			Source:  "skill_instructions",
			Ref:     hit.VersionID,
			Title:   fmt.Sprintf("%s@%s", hit.SkillName, hit.Version),
			Content: content,
			Score:   &score,
		})
	}

	kept, used, dropped, truncated := fit(items, budget)
	return LayerResult{Items: kept, CharsUsed: used, Dropped: dropped, Truncated: truncated, Note: note}
}

func renderSkillCard(hit skills.SkillSearchItem) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Skill: %s@%s\n", hit.SkillName, hit.Version)
	if hit.Description != "" {
		fmt.Fprintf(&builder, "Description: %s\n", hit.Description)
	}
	for label, values := range map[string][]string{
		"Triggers":     hit.Triggers,
		"Capabilities": hit.Capabilities,
		"Constraints":  hit.Constraints,
	} {
		if len(values) > 0 {
			fmt.Fprintf(&builder, "%s: %s\n", label, strings.Join(values, "; "))
		}
	}
	return builder.String()
}

// ─── KNOWLEDGE ───

func (s *Service) gatherKnowledge(ctx context.Context, owner ownership.Owner, req PackRequest, query string, budget int) LayerResult {
	if s.providers.Knowledge == nil {
		return LayerResult{Note: "knowledge provider is not configured"}
	}
	result, err := s.providers.Knowledge.Search(ctx, owner, knowledge.SearchKnowledgeRequest{
		Query:     query,
		TopK:      req.TopK,
		Domain:    req.KnowledgeDomain,
		SourceIDs: req.KnowledgeSourceIDs,
		// Evidence must be the actual passage, not a snippet: the agent is asked
		// to ground claims in it.
		IncludeContent: true,
	})
	if err != nil {
		return LayerResult{Note: "knowledge search failed: " + err.Error()}
	}
	items := make([]Item, 0, len(result.Items))
	for _, hit := range result.Items {
		score := hit.Score
		citation := hit.Path
		if hit.HeadingPath != "" {
			citation += " # " + hit.HeadingPath
		}
		items = append(items, Item{
			Layer:    LayerKnowledge,
			Source:   "knowledge_chunk",
			Ref:      hit.DocumentID,
			Title:    hit.Knowledge,
			Content:  hit.Content,
			Citation: citation,
			Score:    &score,
		})
	}
	kept, used, dropped, truncated := fit(items, budget)
	note := ""
	if len(kept) == 0 && err == nil {
		note = "no indexed knowledge matched the query"
	}
	return LayerResult{Items: kept, CharsUsed: used, Dropped: dropped, Truncated: truncated, Note: note}
}

// ─── MEMORY ───

func (s *Service) gatherMemory(ctx context.Context, owner ownership.Owner, req PackRequest, query string, budget int) LayerResult {
	if s.providers.Memory == nil {
		return LayerResult{Note: "memory provider is not configured"}
	}
	result, err := s.providers.Memory.SearchEntries(ctx, owner, memory.SearchEntriesRequest{
		Query:     query,
		TopK:      req.TopK,
		ScopeType: req.MemoryScopeType,
		ScopeKey:  req.MemoryScopeKey,
		// Only active memories: superseded or invalidated experience is exactly
		// what must not be replayed.
		Status: memory.StatusActive,
	})
	if err != nil {
		return LayerResult{Note: "memory search failed: " + err.Error()}
	}
	items := make([]Item, 0, len(result.Items))
	for _, hit := range result.Items {
		if hit.Entry == nil {
			continue
		}
		score := hit.Score
		content := hit.Entry.Content
		if content == "" {
			content = hit.Entry.Summary
		}
		items = append(items, Item{
			Layer:   LayerMemory,
			Source:  "memory_entry",
			Ref:     hit.Entry.ID,
			Title:   strings.TrimSpace(hit.Entry.MemoryType + " " + hit.Entry.Title),
			Content: content,
			Score:   &score,
		})
	}
	kept, used, dropped, truncated := fit(items, budget)
	note := ""
	if len(kept) == 0 {
		note = "no active memory matched the query"
	}
	return LayerResult{Items: kept, CharsUsed: used, Dropped: dropped, Truncated: truncated, Note: note}
}

// ─── FACTS ───

// gatherFacts reads live application state. It queries in real time and never
// consults an index: task state changes constantly, so indexed facts would be
// served with the confidence of retrieved evidence while being stale.
func (s *Service) gatherFacts(ctx context.Context, owner ownership.Owner, scopes []string, req PackRequest, query string, budget int) LayerResult {
	todosAllowed := permits(scopes, "todos:r")
	notesAllowed := permits(scopes, "notes:r")
	if !todosAllowed && !notesAllowed {
		return LayerResult{Note: "insufficient scope: todos:r or notes:r"}
	}
	if s.providers.Todos == nil && s.providers.Notes == nil {
		return LayerResult{Note: "no fact provider is configured"}
	}
	items := make([]Item, 0, req.TopK*2)
	var problems []string

	if s.providers.Todos != nil && todosAllowed {
		todos, err := s.providers.Todos.Search(ctx, owner.Account(), query)
		if err != nil {
			problems = append(problems, "todo search failed: "+err.Error())
		} else {
			for index, item := range todos {
				if index >= req.TopK {
					break
				}
				// Status and priority are the point of a todo in context; the
				// description is secondary.
				content := fmt.Sprintf("[%s/%s] %s", item.Status, item.Priority, item.Title)
				if item.Description != "" {
					content += "\n" + item.Description
				}
				items = append(items, Item{
					Layer:   LayerFacts,
					Source:  "todo",
					Ref:     item.ID,
					Title:   item.Title,
					Content: content,
				})
			}
		}
	}
	if s.providers.Notes != nil && notesAllowed {
		found, err := s.providers.Notes.Search(ctx, owner.Account(), query)
		if err != nil {
			problems = append(problems, "note search failed: "+err.Error())
		} else {
			for index, item := range found {
				if index >= req.TopK {
					break
				}
				items = append(items, Item{
					Layer:   LayerFacts,
					Source:  "note",
					Ref:     item.ID,
					Title:   item.Title,
					Content: item.Content,
				})
			}
		}
	}

	kept, used, dropped, truncated := fit(items, budget)
	note := strings.Join(problems, "; ")
	if note == "" && len(kept) == 0 {
		note = "no matching todos or notes"
	}
	return LayerResult{Items: kept, CharsUsed: used, Dropped: dropped, Truncated: truncated, Note: note}
}

// ─── TASK ───

// gatherTask emits the goal statement plus, when a session is known, the recent
// decisions and outcomes of that session.
//
// A resumable checkpoint is not implemented yet (it is part of the remaining
// memory work), so this layer reconstructs recent intent from the journal rather
// than restoring a saved state. The distinction is recorded in the layer note so
// a caller does not mistake this for resume support.
func (s *Service) gatherTask(ctx context.Context, owner ownership.Owner, scopes []string, req PackRequest, budget int) LayerResult {
	items := []Item{{
		Layer:   LayerTask,
		Source:  "goal",
		Content: req.Task,
	}}
	note := ""

	if req.SessionID != "" && s.providers.Memory != nil && permits(scopes, "memory:r") {
		timeline, err := s.providers.Memory.SessionTimeline(ctx, owner, memory.SessionTimelineParams{
			SessionID: req.SessionID,
			Limit:     50,
		})
		if err != nil {
			note = "session timeline unavailable: " + err.Error()
		} else {
			recent := recentSessionLines(timeline)
			if recent != "" {
				items = append(items, Item{
					Layer:   LayerTask,
					Source:  "session_recent",
					Ref:     req.SessionID,
					Title:   "recent session activity",
					Content: recent,
				})
			}
			note = "checkpoint restore is not implemented; recent activity is reconstructed from the journal"
		}
	}

	kept, used, dropped, truncated := fit(items, budget)
	return LayerResult{Items: kept, CharsUsed: used, Dropped: dropped, Truncated: truncated, Note: note}
}

// recentSessionLines renders the tail of a session as compact lines. Only the
// event kinds that carry intent or result are kept: replaying every observation
// would spend the task budget on noise.
func recentSessionLines(timeline *memory.SessionTimelineResponse) string {
	if timeline == nil || len(timeline.Items) == 0 {
		return ""
	}
	interesting := map[string]bool{
		"goal": true, "decision": true, "outcome": true,
		"correction": true, "issue": true, "checkpoint": true,
	}
	lines := make([]string, 0, 12)
	for _, item := range timeline.Items {
		switch item.Kind {
		case memory.TimelineKindSkillLog:
			label := item.Outcome
			if label == "" {
				label = "ran"
			}
			lines = append(lines, fmt.Sprintf("- skill %s: %s", item.SkillName, label))
		case memory.TimelineKindMemoryEvent:
			if !interesting[item.EventType] {
				continue
			}
			skill := item.SkillName
			if skill == "" {
				skill = "unattributed"
			}
			lines = append(lines, fmt.Sprintf("- %s (%s)", item.EventType, skill))
		}
	}
	// Keep the most recent lines: when a session is long, what happened last is
	// what constrains the next step.
	const maxLines = 12
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n")
}

// ─── render ───

// Render produces the labelled plain-text form. Items carry their source and
// citation inline so the label survives being pasted into a prompt: a model that
// sees only prose cannot tell compiled instructions from remembered experience.
func Render(response *PackResponse) string {
	var builder strings.Builder
	for _, layer := range response.Layers {
		if len(layer.Items) == 0 {
			continue
		}
		fmt.Fprintf(&builder, "[%s]\n", layer.Layer)
		for _, item := range layer.Items {
			header := item.Source
			if item.Title != "" {
				header += " · " + item.Title
			}
			if item.Citation != "" {
				header += " · " + item.Citation
			}
			if item.Truncated {
				header += " · truncated"
			}
			fmt.Fprintf(&builder, "- %s\n%s\n", header, strings.TrimRight(item.Content, "\n"))
		}
		builder.WriteString("\n")
	}
	return strings.TrimRight(builder.String(), "\n")
}

// missingScopes returns the first scope a layer requires but the caller lacks,
// or "" when the layer is permitted.
func missingScopes(scopes []string, layer string) string {
	for _, required := range layerScopes[layer] {
		if !permits(scopes, required) {
			return required
		}
	}
	return ""
}
