package contextpack

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wellxie/agentmate/internal/knowledge"
	"github.com/wellxie/agentmate/internal/memory"
	"github.com/wellxie/agentmate/internal/notes"
	"github.com/wellxie/agentmate/internal/ownership"
	"github.com/wellxie/agentmate/internal/skills"
	"github.com/wellxie/agentmate/internal/todo"
)

// ─── doubles ───

type fakeSkills struct {
	searchResult *skills.SearchSkillsResponse
	active       *skills.SkillVersion
	err          error
	lastQuery    string
}

func (f *fakeSkills) Search(_ context.Context, _ ownership.Owner, req skills.SearchSkillsRequest) (*skills.SearchSkillsResponse, error) {
	f.lastQuery = req.Query
	if f.err != nil {
		return nil, f.err
	}
	return f.searchResult, nil
}

func (f *fakeSkills) GetActiveVersion(_ context.Context, _, skillName string) (*skills.SkillVersion, error) {
	if f.active == nil || f.active.SkillName != skillName {
		return nil, errors.New("not found")
	}
	return f.active, nil
}

type fakeKnowledge struct {
	result  *knowledge.SearchKnowledgeResponse
	err     error
	lastReq knowledge.SearchKnowledgeRequest
}

func (f *fakeKnowledge) Search(_ context.Context, _ ownership.Owner, req knowledge.SearchKnowledgeRequest) (*knowledge.SearchKnowledgeResponse, error) {
	f.lastReq = req
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

type fakeMemory struct {
	search   *memory.SearchResponse
	timeline *memory.SessionTimelineResponse
	resume   *memory.ResumeResponse
	err      error
	lastReq  memory.SearchEntriesRequest
}

func (f *fakeMemory) Resume(_ context.Context, _ ownership.Owner, sessionID string) (*memory.ResumeResponse, error) {
	if f.resume != nil {
		return f.resume, nil
	}
	// Default double: no checkpoint, journal only, mirroring a session that was
	// never checkpointed.
	items := []memory.TimelineItem{}
	if f.timeline != nil {
		items = f.timeline.Items
	}
	resolution := "empty"
	if len(items) > 0 {
		resolution = "journal_only"
	}
	return &memory.ResumeResponse{SessionID: sessionID, SinceCheckpoint: items, Resolution: resolution}, nil
}

func (f *fakeMemory) SearchEntries(_ context.Context, _ ownership.Owner, req memory.SearchEntriesRequest) (*memory.SearchResponse, error) {
	f.lastReq = req
	if f.err != nil {
		return nil, f.err
	}
	return f.search, nil
}

func (f *fakeMemory) SessionTimeline(_ context.Context, _ ownership.Owner, _ memory.SessionTimelineParams) (*memory.SessionTimelineResponse, error) {
	if f.timeline == nil {
		return &memory.SessionTimelineResponse{}, nil
	}
	return f.timeline, nil
}

type fakeTodos struct {
	items []todo.Todo
	err   error
}

func (f *fakeTodos) Search(_ context.Context, _, _ string) ([]todo.Todo, error) {
	return f.items, f.err
}

type fakeNotes struct {
	items []notes.Note
	err   error
}

func (f *fakeNotes) Search(_ context.Context, _, _ string) ([]notes.Note, error) {
	return f.items, f.err
}

func testOwner() ownership.Owner {
	return ownership.Owner{AccountID: "acct-1", UserID: "user-1"}
}

func fullProviders() Providers {
	return Providers{
		Skills: &fakeSkills{searchResult: &skills.SearchSkillsResponse{Items: []skills.SkillSearchItem{{
			SkillName: "grounded-answer", Version: "0.1.0", VersionID: "sv-1",
			Content: "# 指令\n\n先检索证据，再回答。", Score: 0.99,
		}}}},
		Knowledge: &fakeKnowledge{result: &knowledge.SearchKnowledgeResponse{Items: []knowledge.KnowledgeSearchHit{{
			DocumentID: "doc-1", Path: "raw/cjk-lexical.md", HeadingPath: "CJK lexical 投影",
			Knowledge: "platform-retrieval", Content: "整段中文会成为单个 token。", Score: 0.98,
		}}}},
		Memory: &fakeMemory{search: &memory.SearchResponse{Items: []memory.SearchItem{{
			Entry: &memory.EntryDetail{Entry: memory.Entry{
				ID: "mem-1", MemoryType: "procedural", Title: "归因锚点",
				Content: "session_id 单独不足以归因。",
			}},
			Score: 0.91,
		}}}},
		Todos: &fakeTodos{items: []todo.Todo{{ID: "todo-1", Title: "修复检索", Status: "pending", Priority: "high"}}},
		Notes: &fakeNotes{items: []notes.Note{{ID: "note-1", Title: "笔记", Content: "bigram 投影备忘。"}}},
	}
}

// ─── tests ───

func TestPackAssemblesAllFiveLayersWithinBudget(t *testing.T) {
	service := NewService(fullProviders())
	response, err := service.Pack(context.Background(), testOwner(), nil, PackRequest{
		Task:   "修复中文检索命中为零的问题",
		Render: true,
	})
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if len(response.Layers) != len(LayerOrder) {
		t.Fatalf("layers = %d, want %d", len(response.Layers), len(LayerOrder))
	}
	for index, layer := range response.Layers {
		if layer.Layer != LayerOrder[index] {
			t.Fatalf("layer %d = %s, want %s", index, layer.Layer, LayerOrder[index])
		}
		if layer.CharsUsed > layer.CharBudget {
			t.Fatalf("%s used %d over budget %d", layer.Layer, layer.CharsUsed, layer.CharBudget)
		}
	}
	if response.CharsUsed > response.MaxChars {
		t.Fatalf("total %d exceeds max %d", response.CharsUsed, response.MaxChars)
	}

	// Every layer must carry its label into the rendered form, otherwise the
	// model cannot tell instructions from remembered experience.
	for _, label := range []string{"[SKILL]", "[KNOWLEDGE]", "[MEMORY]", "[FACTS]", "[TASK]"} {
		if !strings.Contains(response.Rendered, label) {
			t.Fatalf("rendered pack missing %s:\n%s", label, response.Rendered)
		}
	}
	// Citations must survive rendering: an evidence line without a location
	// cannot be checked.
	if !strings.Contains(response.Rendered, "raw/cjk-lexical.md # CJK lexical 投影") {
		t.Fatalf("rendered pack lost the citation:\n%s", response.Rendered)
	}
}

// A nil provider degrades one layer and leaves the others usable; the response
// must say so rather than returning a quietly thinner pack.
func TestPackDegradesMissingProvidersWithWarnings(t *testing.T) {
	providers := fullProviders()
	providers.Knowledge = nil
	providers.Todos = nil
	providers.Notes = nil
	service := NewService(providers)

	response, err := service.Pack(context.Background(), testOwner(), nil, PackRequest{Task: "任务"})
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	byLayer := map[string]LayerResult{}
	for _, layer := range response.Layers {
		byLayer[layer.Layer] = layer
	}
	if len(byLayer[LayerKnowledge].Items) != 0 || byLayer[LayerKnowledge].Note == "" {
		t.Fatalf("knowledge layer = %#v", byLayer[LayerKnowledge])
	}
	if len(byLayer[LayerFacts].Items) != 0 || byLayer[LayerFacts].Note == "" {
		t.Fatalf("facts layer = %#v", byLayer[LayerFacts])
	}
	if len(byLayer[LayerSkill].Items) == 0 {
		t.Fatal("skill layer should still be populated")
	}
	if len(response.Warnings) < 2 {
		t.Fatalf("warnings = %v", response.Warnings)
	}
}

// A failing layer must not fail the pack.
func TestPackSurvivesLayerFailure(t *testing.T) {
	providers := fullProviders()
	providers.Knowledge = &fakeKnowledge{err: errors.New("qdrant unreachable")}
	service := NewService(providers)

	response, err := service.Pack(context.Background(), testOwner(), nil, PackRequest{Task: "任务"})
	if err != nil {
		t.Fatalf("pack should not fail: %v", err)
	}
	var note string
	for _, layer := range response.Layers {
		if layer.Layer == LayerKnowledge {
			note = layer.Note
		}
	}
	if !strings.Contains(note, "qdrant unreachable") {
		t.Fatalf("knowledge note = %q", note)
	}
	if len(response.Warnings) == 0 {
		t.Fatal("a failed layer must produce a warning")
	}
}

func TestPackHonoursLayerSelection(t *testing.T) {
	service := NewService(fullProviders())
	response, err := service.Pack(context.Background(), testOwner(), nil, PackRequest{
		Task:   "任务",
		Layers: []string{"KNOWLEDGE", "TASK"},
	})
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if len(response.Layers) != 2 {
		t.Fatalf("layers = %#v", response.Layers)
	}
	total := 0
	for _, layer := range response.Layers {
		total += layer.CharBudget
	}
	if total != response.MaxChars {
		t.Fatalf("selected layers should share the whole budget: %d vs %d", total, response.MaxChars)
	}
}

// A pinned skill bypasses retrieval; a pinned name with no active version must
// be reported rather than silently falling back to a different skill.
func TestPackPinnedSkill(t *testing.T) {
	providers := fullProviders()
	providers.Skills = &fakeSkills{
		active: &skills.SkillVersion{ID: "sv-9", SkillName: "kb-lint", Version: "0.2.0", Content: "# 体检指令"},
	}
	service := NewService(providers)

	response, err := service.Pack(context.Background(), testOwner(), nil, PackRequest{Task: "体检", SkillName: "kb-lint"})
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	skillLayer := response.Layers[0]
	if len(skillLayer.Items) != 1 || skillLayer.Items[0].Ref != "sv-9" {
		t.Fatalf("skill layer = %#v", skillLayer)
	}

	response, err = service.Pack(context.Background(), testOwner(), nil, PackRequest{Task: "体检", SkillName: "missing"})
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if len(response.Layers[0].Items) != 0 || !strings.Contains(response.Layers[0].Note, "missing") {
		t.Fatalf("expected a note about the pinned skill: %#v", response.Layers[0])
	}
}

// Retrieval can return a card whose L1 body is unavailable. The layer must fall
// back to the card and say so instead of emitting nothing.
func TestPackFallsBackToSkillCardWhenInstructionsMissing(t *testing.T) {
	providers := fullProviders()
	providers.Skills = &fakeSkills{searchResult: &skills.SearchSkillsResponse{Items: []skills.SkillSearchItem{{
		SkillName: "grounded-answer", Version: "0.1.0", VersionID: "sv-2",
		Description: "基于证据回答", Triggers: []string{"事实性问题"},
	}}}}
	service := NewService(providers)

	response, err := service.Pack(context.Background(), testOwner(), nil, PackRequest{Task: "任务"})
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	skillLayer := response.Layers[0]
	if len(skillLayer.Items) != 1 {
		t.Fatalf("skill layer = %#v", skillLayer)
	}
	if !strings.Contains(skillLayer.Items[0].Content, "基于证据回答") {
		t.Fatalf("expected the card as fallback: %q", skillLayer.Items[0].Content)
	}
	if skillLayer.Note == "" {
		t.Fatal("the fallback must be reported")
	}
}

// Query overrides the task as the retrieval string; filters must reach the
// providers unchanged.
func TestPackPassesQueryAndFiltersToProviders(t *testing.T) {
	providers := fullProviders()
	fakeK := providers.Knowledge.(*fakeKnowledge)
	fakeM := providers.Memory.(*fakeMemory)
	fakeS := providers.Skills.(*fakeSkills)
	service := NewService(providers)

	if _, err := service.Pack(context.Background(), testOwner(), nil, PackRequest{
		Task:               "一段很长的命令式任务描述，不适合直接当检索词",
		Query:              "bigram 投影",
		KnowledgeDomain:    "platform",
		KnowledgeSourceIDs: []string{"src-1"},
		MemoryScopeType:    "project",
		MemoryScopeKey:     "agentmate",
	}); err != nil {
		t.Fatalf("pack: %v", err)
	}
	if fakeS.lastQuery != "bigram 投影" || fakeK.lastReq.Query != "bigram 投影" || fakeM.lastReq.Query != "bigram 投影" {
		t.Fatalf("query override did not reach providers: %q / %q / %q",
			fakeS.lastQuery, fakeK.lastReq.Query, fakeM.lastReq.Query)
	}
	if fakeK.lastReq.Domain != "platform" || len(fakeK.lastReq.SourceIDs) != 1 {
		t.Fatalf("knowledge filters = %#v", fakeK.lastReq)
	}
	if !fakeK.lastReq.IncludeContent {
		t.Fatal("evidence must be the full passage, not a snippet")
	}
	if fakeM.lastReq.ScopeType != "project" || fakeM.lastReq.ScopeKey != "agentmate" {
		t.Fatalf("memory scope = %#v", fakeM.lastReq)
	}
	// Superseded experience is exactly what must not be replayed.
	if fakeM.lastReq.Status != memory.StatusActive {
		t.Fatalf("memory status filter = %q, want %q", fakeM.lastReq.Status, memory.StatusActive)
	}
}

func TestPackTaskLayerIncludesRecentSessionActivity(t *testing.T) {
	providers := fullProviders()
	skillName := "grounded-answer"
	providers.Memory = &fakeMemory{
		search: &memory.SearchResponse{},
		timeline: &memory.SessionTimelineResponse{Items: []memory.TimelineItem{
			{Kind: memory.TimelineKindMemoryEvent, EventType: "goal", SkillName: skillName},
			{Kind: memory.TimelineKindMemoryEvent, EventType: "observation", SkillName: skillName},
			{Kind: memory.TimelineKindSkillLog, SkillName: skillName, Outcome: "failure"},
			{Kind: memory.TimelineKindMemoryEvent, EventType: "correction"},
		}},
	}
	service := NewService(providers)

	response, err := service.Pack(context.Background(), testOwner(), nil, PackRequest{
		Task:      "继续修复",
		SessionID: "sess-1",
	})
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	taskLayer := response.Layers[len(response.Layers)-1]
	if taskLayer.Layer != LayerTask || len(taskLayer.Items) != 2 {
		t.Fatalf("task layer = %#v", taskLayer)
	}
	recent := taskLayer.Items[1].Content
	for _, want := range []string{"goal", "failure", "correction"} {
		if !strings.Contains(recent, want) {
			t.Fatalf("recent activity missing %q: %q", want, recent)
		}
	}
	// Low-signal events would spend the task budget on noise.
	if strings.Contains(recent, "observation") {
		t.Fatalf("observation should be filtered out: %q", recent)
	}
	// A journal replay is not a checkpoint restore; the note must not blur them.
	if !strings.Contains(taskLayer.Note, "no checkpoint saved") {
		t.Fatalf("task note = %q", taskLayer.Note)
	}
}

// A saved checkpoint is preferred over reconstruction, and the activity recorded
// after it must come along: that tail is exactly what the snapshot is missing.
func TestPackTaskLayerPrefersCheckpointAndIncludesTail(t *testing.T) {
	providers := fullProviders()
	saved := time.Now().UTC().Add(-10 * time.Minute)
	providers.Memory = &fakeMemory{
		search: &memory.SearchResponse{},
		resume: &memory.ResumeResponse{
			SessionID:  "sess-2",
			Resolution: "checkpoint",
			Checkpoint: &memory.Checkpoint{
				EventID: "ev-cp", SessionID: "sess-2", Label: "M3 阶段",
				Goal:       "补齐 memory 三项能力",
				Done:       []string{"supersede 已实现"},
				Next:       []string{"接线 feedback"},
				Open:       []string{"checkpoint 是否要压缩历史"},
				OccurredAt: saved,
			},
			SinceCheckpoint: []memory.TimelineItem{
				{Kind: memory.TimelineKindMemoryEvent, EventType: "outcome", SkillName: "kb-lint"},
			},
		},
	}
	service := NewService(providers)

	response, err := service.Pack(context.Background(), testOwner(), nil, PackRequest{
		Task:      "继续 M3",
		SessionID: "sess-2",
	})
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	taskLayer := response.Layers[len(response.Layers)-1]
	if len(taskLayer.Items) != 3 {
		t.Fatalf("task layer should hold goal, checkpoint and tail: %#v", taskLayer.Items)
	}
	if taskLayer.Items[1].Source != "checkpoint" || taskLayer.Items[2].Source != "since_checkpoint" {
		t.Fatalf("task item sources = %q / %q", taskLayer.Items[1].Source, taskLayer.Items[2].Source)
	}
	rendered := taskLayer.Items[1].Content
	for _, want := range []string{"补齐 memory 三项能力", "supersede 已实现", "接线 feedback", "checkpoint 是否要压缩历史"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("checkpoint render missing %q:\n%s", want, rendered)
		}
	}
	if !strings.Contains(taskLayer.Note, "resumed from checkpoint") {
		t.Fatalf("task note = %q", taskLayer.Note)
	}
}

func TestPackValidatesInput(t *testing.T) {
	service := NewService(fullProviders())
	for _, testCase := range []struct {
		name string
		req  PackRequest
	}{
		{"empty task", PackRequest{Task: "  "}},
		{"budget too small", PackRequest{Task: "t", MaxChars: 10}},
		{"budget too large", PackRequest{Task: "t", MaxChars: MaxMaxChars + 1}},
		{"negative top_k", PackRequest{Task: "t", TopK: -1}},
		{"top_k too large", PackRequest{Task: "t", TopK: 100}},
		{"unknown layer", PackRequest{Task: "t", Layers: []string{"DREAMS"}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := service.Pack(context.Background(), testOwner(), nil, testCase.req); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
	if _, err := service.Pack(context.Background(), ownership.Owner{}, nil, PackRequest{Task: "t"}); err == nil {
		t.Fatal("expected an error without an account")
	}
}

// A tight budget must still produce a usable pack rather than overflowing.
func TestPackRespectsTightBudget(t *testing.T) {
	providers := fullProviders()
	providers.Skills = &fakeSkills{searchResult: &skills.SearchSkillsResponse{Items: []skills.SkillSearchItem{{
		SkillName: "big", Version: "1", VersionID: "sv-big",
		Content: strings.Repeat("指令内容。", 2000),
	}}}}
	service := NewService(providers)

	response, err := service.Pack(context.Background(), testOwner(), nil, PackRequest{
		Task:     "任务",
		MaxChars: MinMaxChars,
	})
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if response.CharsUsed > MinMaxChars {
		t.Fatalf("used %d over %d", response.CharsUsed, MinMaxChars)
	}
	skillLayer := response.Layers[0]
	if skillLayer.Truncated != 1 {
		t.Fatalf("oversized instructions should be truncated: %#v", skillLayer)
	}
	if !skillLayer.Items[0].Truncated {
		t.Fatal("the truncated item must be flagged so the agent knows")
	}
}

// One endpoint spanning five domains must not let a narrowly scoped key read
// everything. Authorisation is per layer: permitted layers are populated, the
// rest carry an explicit scope note.
func TestPackEnforcesScopesPerLayer(t *testing.T) {
	service := NewService(fullProviders())
	response, err := service.Pack(context.Background(), testOwner(), []string{"skills:r"}, PackRequest{
		Task:      "任务",
		SessionID: "sess-1",
	})
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	byLayer := map[string]LayerResult{}
	for _, layer := range response.Layers {
		byLayer[layer.Layer] = layer
	}

	if len(byLayer[LayerSkill].Items) == 0 {
		t.Fatalf("skills:r should permit the skill layer: %#v", byLayer[LayerSkill])
	}
	for _, layer := range []string{LayerKnowledge, LayerMemory, LayerFacts} {
		if len(byLayer[layer].Items) != 0 {
			t.Fatalf("%s must be empty without its scope: %#v", layer, byLayer[layer])
		}
		if !strings.Contains(byLayer[layer].Note, "insufficient scope") {
			t.Fatalf("%s note = %q", layer, byLayer[layer].Note)
		}
	}
	// The goal statement is the caller's own input, so TASK stays available; the
	// session slice reads the journal and must be withheld.
	taskLayer := byLayer[LayerTask]
	if len(taskLayer.Items) != 1 || taskLayer.Items[0].Source != "goal" {
		t.Fatalf("task layer = %#v", taskLayer)
	}
}

// A read-write scope implies read, and an empty scope list means full access.
func TestPackScopeImplicationAndFullAccess(t *testing.T) {
	service := NewService(fullProviders())

	response, err := service.Pack(context.Background(), testOwner(), []string{"knowledge:rw"}, PackRequest{Task: "任务"})
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	for _, layer := range response.Layers {
		if layer.Layer == LayerKnowledge && len(layer.Items) == 0 {
			t.Fatalf("knowledge:rw should imply knowledge:r: %#v", layer)
		}
	}

	response, err = service.Pack(context.Background(), testOwner(), []string{}, PackRequest{Task: "任务"})
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	for _, layer := range response.Layers {
		if len(layer.Items) == 0 {
			t.Fatalf("an empty scope list means full access, but %s is empty: %#v", layer.Layer, layer)
		}
	}
}

// Partial fact scopes must include only the authorised half.
func TestPackFactsLayerAuthorisesTodosAndNotesSeparately(t *testing.T) {
	service := NewService(fullProviders())
	response, err := service.Pack(context.Background(), testOwner(), []string{"notes:r"}, PackRequest{Task: "任务"})
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	for _, layer := range response.Layers {
		if layer.Layer != LayerFacts {
			continue
		}
		if len(layer.Items) != 1 || layer.Items[0].Source != "note" {
			t.Fatalf("facts layer should hold only notes: %#v", layer.Items)
		}
	}
}

// A nil slice marshals to null, which would force every client to handle two
// shapes for "no items".
func TestPackAlwaysEmitsItemArrays(t *testing.T) {
	providers := fullProviders()
	providers.Knowledge = nil
	service := NewService(providers)

	response, err := service.Pack(context.Background(), testOwner(), []string{"skills:r"}, PackRequest{Task: "任务"})
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), `"items":null`) {
		t.Fatalf("layer items marshalled to null: %s", encoded)
	}
	for _, layer := range response.Layers {
		if layer.Items == nil {
			t.Fatalf("%s has nil items", layer.Layer)
		}
	}
}
