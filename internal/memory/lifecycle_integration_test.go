package memory

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wellxie/agentmate/internal/ownership"
)

func newEntry(t *testing.T, ctx context.Context, service *Service, owner ownership.Owner, content string) *EntryDetail {
	t.Helper()
	entry, err := service.CreateEntry(ctx, owner, CreateEntryRequest{
		MemoryType: "procedural",
		Content:    content,
		Evidence: []EvidenceInput{{
			SourceType: "manual", SourceID: "fixture", Excerpt: "fixture evidence",
		}},
	})
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}
	return entry
}

// ─── supersede ───

func TestSupersedeMarksReplacementAndClosesValidity(t *testing.T) {
	ctx := context.Background()
	pool := attributionPool(t, ctx)
	owner, cleanup := attributionOwner(t, ctx, pool, "supersede")
	defer cleanup()
	service := NewService(NewRepo(pool))

	old := newEntry(t, ctx, service, owner, "旧做法：只用 session_id 归因")
	replacement := newEntry(t, ctx, service, owner, "新做法：memory event 带 skill_version_id")

	response, err := service.SupersedeEntry(ctx, owner, SupersedeRequest{
		SupersedingID: replacement.Entry.ID,
		SupersededID:  old.Entry.ID,
	})
	if err != nil {
		t.Fatalf("supersede: %v", err)
	}
	if response.Superseded.Status != StatusSuperseded {
		t.Fatalf("status = %q, want %q", response.Superseded.Status, StatusSuperseded)
	}
	if response.Superseded.SupersededBy == nil || *response.Superseded.SupersededBy != replacement.Entry.ID {
		t.Fatalf("superseded_by = %v", response.Superseded.SupersededBy)
	}
	// The validity window must close, otherwise the entry stays temporally valid
	// while being marked replaced.
	if response.Superseded.ValidTo == nil {
		t.Fatal("valid_to must be closed at the supersede time")
	}
	if response.Superseding.Status != StatusActive {
		t.Fatalf("replacement status = %q", response.Superseding.Status)
	}
}

func TestSupersedeIsIdempotentAndRejectsConflicts(t *testing.T) {
	ctx := context.Background()
	pool := attributionPool(t, ctx)
	owner, cleanup := attributionOwner(t, ctx, pool, "supersede-idem")
	defer cleanup()
	service := NewService(NewRepo(pool))

	old := newEntry(t, ctx, service, owner, "旧条目")
	first := newEntry(t, ctx, service, owner, "第一个替代者")
	second := newEntry(t, ctx, service, owner, "第二个替代者")

	request := SupersedeRequest{SupersedingID: first.Entry.ID, SupersededID: old.Entry.ID}
	if _, err := service.SupersedeEntry(ctx, owner, request); err != nil {
		t.Fatalf("first supersede: %v", err)
	}
	// Replaying the same supersede must not error: a retrying agent should not
	// need to know whether its previous call landed.
	if _, err := service.SupersedeEntry(ctx, owner, request); err != nil {
		t.Fatalf("replay should be idempotent: %v", err)
	}
	// Repointing at a different replacement is a conflict, not an update: two
	// answers to "which entry is current" cannot both be right.
	if _, err := service.SupersedeEntry(ctx, owner, SupersedeRequest{
		SupersedingID: second.Entry.ID, SupersededID: old.Entry.ID,
	}); err == nil {
		t.Fatal("expected a conflict when switching the replacement")
	}
}

// Chains are legitimate; cycles make "which one is current" unanswerable.
func TestSupersedeAllowsChainsButRejectsCycles(t *testing.T) {
	ctx := context.Background()
	pool := attributionPool(t, ctx)
	owner, cleanup := attributionOwner(t, ctx, pool, "supersede-chain")
	defer cleanup()
	service := NewService(NewRepo(pool))

	first := newEntry(t, ctx, service, owner, "A")
	second := newEntry(t, ctx, service, owner, "B")
	third := newEntry(t, ctx, service, owner, "C")

	if _, err := service.SupersedeEntry(ctx, owner, SupersedeRequest{
		SupersedingID: second.Entry.ID, SupersededID: first.Entry.ID,
	}); err != nil {
		t.Fatalf("B replaces A: %v", err)
	}
	if _, err := service.SupersedeEntry(ctx, owner, SupersedeRequest{
		SupersedingID: third.Entry.ID, SupersededID: second.Entry.ID,
	}); err != nil {
		t.Fatalf("C replaces B should be allowed: %v", err)
	}
	// A is already replaced (transitively) by C, so C being replaced by A closes
	// a loop.
	if _, err := service.SupersedeEntry(ctx, owner, SupersedeRequest{
		SupersedingID: first.Entry.ID, SupersededID: third.Entry.ID,
	}); err == nil {
		t.Fatal("expected the cycle to be rejected")
	}

	if _, err := service.SupersedeEntry(ctx, owner, SupersedeRequest{
		SupersedingID: first.Entry.ID, SupersededID: first.Entry.ID,
	}); err == nil {
		t.Fatal("expected self-supersede to be rejected")
	}
}

// ─── feedback ───

func TestFeedbackMovesCountersAndIsPerSession(t *testing.T) {
	ctx := context.Background()
	pool := attributionPool(t, ctx)
	owner, cleanup := attributionOwner(t, ctx, pool, "feedback")
	defer cleanup()
	service := NewService(NewRepo(pool))

	entry := newEntry(t, ctx, service, owner, "被评价的记忆")
	version := createSkillVersion(t, ctx, pool, owner, "feedback-skill")

	response, err := service.RecordFeedback(ctx, owner, FeedbackRequest{
		MemoryID:       entry.Entry.ID,
		Signal:         SignalUseful,
		Reason:         "直接解决了问题",
		SessionID:      "sess-a",
		SkillVersionID: version,
	})
	if err != nil {
		t.Fatalf("feedback: %v", err)
	}
	if !response.Created || response.Feedback.Entry == nil || response.Feedback.Entry.UsefulCount != 1 {
		t.Fatalf("first signal = %#v", response)
	}
	if response.Feedback.SkillVersionID == nil || *response.Feedback.SkillVersionID != version {
		t.Fatal("attribution anchor lost")
	}

	// A retry within the same session must not inflate the counter.
	repeat, err := service.RecordFeedback(ctx, owner, FeedbackRequest{
		MemoryID: entry.Entry.ID, Signal: SignalUseful, SessionID: "sess-a",
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if repeat.Created {
		t.Fatal("a repeated signal for the same session must not create a second row")
	}

	// A different session is a genuinely new judgement.
	other, err := service.RecordFeedback(ctx, owner, FeedbackRequest{
		MemoryID: entry.Entry.ID, Signal: SignalUseful, SessionID: "sess-b",
	})
	if err != nil {
		t.Fatalf("second session: %v", err)
	}
	if !other.Created || other.Feedback.Entry.UsefulCount != 2 {
		t.Fatalf("second session signal = %#v", other.Feedback.Entry)
	}

	harmful, err := service.RecordFeedback(ctx, owner, FeedbackRequest{
		MemoryID: entry.Entry.ID, Signal: SignalHarmful, SessionID: "sess-a", Reason: "误导",
	})
	if err != nil {
		t.Fatalf("harmful: %v", err)
	}
	if harmful.Feedback.Entry.HarmfulCount != 1 || harmful.Feedback.Entry.UsefulCount != 2 {
		t.Fatalf("counters = %#v", harmful.Feedback.Entry)
	}

	// The log is the durable record; counters are its projection.
	log, err := service.ListFeedback(ctx, owner, entry.Entry.ID, 0)
	if err != nil {
		t.Fatalf("list feedback: %v", err)
	}
	if len(log) != 3 {
		t.Fatalf("feedback log = %d entries, want 3", len(log))
	}

	if _, err := service.RecordFeedback(ctx, owner, FeedbackRequest{
		MemoryID: entry.Entry.ID, Signal: "excellent", SessionID: "sess-c",
	}); err == nil {
		t.Fatal("expected an unknown signal to be rejected")
	}
}

func TestFeedbackAdjustmentIsBoundedAndEvidenceWeighted(t *testing.T) {
	// No signals: no influence at all, so an unrated memory ranks purely on
	// relevance.
	if got := feedbackAdjustment(Entry{}); got != 0 {
		t.Fatalf("no feedback should not adjust: %v", got)
	}

	// A single signal must move the score far less than a consistent history,
	// otherwise one stray click would bury an otherwise good memory.
	one := feedbackAdjustment(Entry{UsefulCount: 1})
	many := feedbackAdjustment(Entry{UsefulCount: 20})
	if !(one > 0 && many > one) {
		t.Fatalf("confidence should grow with evidence: one=%v many=%v", one, many)
	}
	if many > maxFeedbackAdjustment {
		t.Fatalf("adjustment %v exceeds the cap %v", many, maxFeedbackAdjustment)
	}
	if worst := feedbackAdjustment(Entry{HarmfulCount: 50}); worst < -maxFeedbackAdjustment {
		t.Fatalf("negative adjustment %v exceeds the cap", worst)
	}
	// Balanced signals cancel out rather than accumulating in either direction.
	if balanced := feedbackAdjustment(Entry{UsefulCount: 5, HarmfulCount: 5}); balanced != 0 {
		t.Fatalf("balanced feedback = %v, want 0", balanced)
	}
}

// ─── checkpoint ───

func TestCheckpointSaveAndResume(t *testing.T) {
	ctx := context.Background()
	pool := attributionPool(t, ctx)
	owner, cleanup := attributionOwner(t, ctx, pool, "checkpoint")
	defer cleanup()
	service := NewService(NewRepo(pool))

	const session = "sess-checkpoint"

	// A session with activity but no checkpoint must be distinguishable from one
	// that never started.
	empty, err := service.Resume(ctx, owner, session)
	if err != nil {
		t.Fatalf("resume empty: %v", err)
	}
	if empty.Resolution != "empty" {
		t.Fatalf("resolution = %q, want empty", empty.Resolution)
	}

	occurred := time.Now().UTC().Add(-time.Hour)
	if _, _, err := service.RecordEvent(ctx, owner, RecordEventRequest{
		EventType: "goal", IdempotencyKey: "cp-goal", SessionID: session,
		OccurredAt: &occurred, Payload: map[string]any{},
	}); err != nil {
		t.Fatalf("record goal: %v", err)
	}
	journalOnly, err := service.Resume(ctx, owner, session)
	if err != nil {
		t.Fatalf("resume journal: %v", err)
	}
	if journalOnly.Resolution != "journal_only" || journalOnly.Checkpoint != nil {
		t.Fatalf("resume = %#v", journalOnly)
	}

	saved, err := service.SaveCheckpoint(ctx, owner, SaveCheckpointRequest{
		SessionID: session,
		Label:     "M3 阶段",
		Goal:      "补齐 memory 三项能力",
		Done:      []string{"supersede", "  "},
		Next:      []string{"feedback 接线"},
		Open:      []string{"checkpoint 是否压缩历史"},
		Notes:     "数据可清空重来",
	})
	if err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
	if !saved.Created {
		t.Fatal("first save should create")
	}
	// Blank list entries carry no meaning and would render as empty bullets.
	if len(saved.Checkpoint.Done) != 1 {
		t.Fatalf("blank list items should be dropped: %#v", saved.Checkpoint.Done)
	}

	// Saving unchanged state is a no-op rather than a near-duplicate snapshot.
	again, err := service.SaveCheckpoint(ctx, owner, SaveCheckpointRequest{
		SessionID: session, Label: "M3 阶段", Goal: "补齐 memory 三项能力",
		Done: []string{"supersede"}, Next: []string{"feedback 接线"},
		Open: []string{"checkpoint 是否压缩历史"}, Notes: "数据可清空重来",
	})
	if err != nil {
		t.Fatalf("resave: %v", err)
	}
	if again.Created || again.Checkpoint.EventID != saved.Checkpoint.EventID {
		t.Fatalf("unchanged state should not append a checkpoint: %#v", again)
	}

	// Activity after the checkpoint is exactly what a naive resume would drop.
	after := time.Now().UTC()
	if _, _, err := service.RecordEvent(ctx, owner, RecordEventRequest{
		EventType: "outcome", IdempotencyKey: "cp-after", SessionID: session,
		OccurredAt: &after, Payload: map[string]any{},
	}); err != nil {
		t.Fatalf("record post-checkpoint event: %v", err)
	}

	resumed, err := service.Resume(ctx, owner, session)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.Resolution != "checkpoint" || resumed.Checkpoint == nil {
		t.Fatalf("resume = %#v", resumed)
	}
	if resumed.Checkpoint.Goal != "补齐 memory 三项能力" {
		t.Fatalf("goal = %q", resumed.Checkpoint.Goal)
	}
	if len(resumed.SinceCheckpoint) != 1 || resumed.SinceCheckpoint[0].EventType != "outcome" {
		t.Fatalf("post-checkpoint tail = %#v", resumed.SinceCheckpoint)
	}
	// The pre-checkpoint goal event must not reappear in the tail.
	for _, item := range resumed.SinceCheckpoint {
		if item.EventType == "goal" {
			t.Fatalf("tail must only contain activity after the checkpoint: %#v", resumed.SinceCheckpoint)
		}
	}

	if _, err := service.SaveCheckpoint(ctx, owner, SaveCheckpointRequest{SessionID: session}); err == nil {
		t.Fatal("a checkpoint without a goal cannot be resumed from and must be rejected")
	}
	if _, err := service.Resume(ctx, owner, "  "); err == nil {
		t.Fatal("expected an error without a session id")
	}
}

// The supersede path must also stop the entry from being returned by search.
func TestSupersededEntryLeavesSearch(t *testing.T) {
	ctx := context.Background()
	pool := attributionPool(t, ctx)
	owner, cleanup := attributionOwner(t, ctx, pool, "supersede-search")
	defer cleanup()
	service := NewService(NewRepo(pool))

	old := newEntry(t, ctx, service, owner, "旧的检索约定")
	replacement := newEntry(t, ctx, service, owner, "新的检索约定")

	if _, err := service.SupersedeEntry(ctx, owner, SupersedeRequest{
		SupersedingID: replacement.Entry.ID, SupersededID: old.Entry.ID,
	}); err != nil {
		t.Fatalf("supersede: %v", err)
	}

	// Status filtering happens on hydration, so the replaced entry must fail the
	// active-status match regardless of the retrieval projection.
	reloaded, err := service.GetEntry(ctx, owner.Account(), old.Entry.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if entryMatchesSearch(reloaded.Entry, SearchEntriesRequest{Status: StatusActive}, time.Now().UTC()) {
		t.Fatal("a superseded entry must not match an active-status search")
	}
	if !strings.EqualFold(reloaded.Entry.Status, StatusSuperseded) {
		t.Fatalf("status = %q", reloaded.Entry.Status)
	}
}
