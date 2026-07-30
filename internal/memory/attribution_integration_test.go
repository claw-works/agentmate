package memory

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claw-works/agentmate/internal/ownership"
)

func attributionPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("AGENTMATE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("AGENTMATE_TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func attributionOwner(t *testing.T, ctx context.Context, pool *pgxpool.Pool, label string) (ownership.Owner, func()) {
	t.Helper()
	var accountID string
	if err := pool.QueryRow(ctx, `INSERT INTO accounts (name) VALUES ($1) RETURNING id::text`,
		"memory attribution "+label).Scan(&accountID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, account_id) VALUES ($1, 'test', $2) RETURNING id::text`,
		"memory-"+label+"-"+accountID+"@example.test", accountID,
	).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	cleanup := func() {
		if _, err := pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, accountID); err != nil {
			t.Errorf("clean up account %s: %v", accountID, err)
		}
	}
	return ownership.Owner{AccountID: accountID, UserID: userID}, cleanup
}

// createSkillVersion inserts an active skill version directly. The memory tests
// must not depend on the skills package, so the row is built here.
func createSkillVersion(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner ownership.Owner, skillName string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO skill_versions (account_id, user_id, skill_name, version, content, content_hash, package_hash, is_active)
		 VALUES ($1, $2, $3, '0.1.0', $4, md5(random()::text), md5(random()::text), true)
		 RETURNING id::text`,
		owner.AccountID, owner.UserID, skillName, "# "+skillName,
	).Scan(&id); err != nil {
		t.Fatalf("create skill version %s: %v", skillName, err)
	}
	return id
}

func recordSkillLog(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner ownership.Owner,
	skillVersionID, skillName, sessionID, outcome string, at time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`INSERT INTO skill_logs (account_id, user_id, skill_version_id, skill_name, skill_version,
		   session_id, was_triggered, outcome, created_at)
		 VALUES ($1, $2, $3::uuid, $4, '0.1.0', $5, true, $6, $7)`,
		owner.AccountID, owner.UserID, skillVersionID, skillName, sessionID, outcome, at,
	); err != nil {
		t.Fatalf("insert skill log: %v", err)
	}
}

// The reason this whole feature exists: a session that runs two skills cannot be
// attributed by session_id alone, so events must carry the skill version.
func TestSessionTimelineAttributesEventsAcrossMultipleSkills(t *testing.T) {
	ctx := context.Background()
	pool := attributionPool(t, ctx)
	owner, cleanup := attributionOwner(t, ctx, pool, "multi-skill")
	defer cleanup()
	service := NewService(NewRepo(pool))

	const session = "session-multi"
	grounded := createSkillVersion(t, ctx, pool, owner, "grounded-answer")
	lint := createSkillVersion(t, ctx, pool, owner, "kb-lint")

	base := time.Now().UTC().Truncate(time.Second)
	recordSkillLog(t, ctx, pool, owner, grounded, "grounded-answer", session, "success", base)
	recordSkillLog(t, ctx, pool, owner, lint, "kb-lint", session, "failure", base.Add(3*time.Second))

	record := func(eventType, key, skillVersionID string, at time.Time) *Event {
		t.Helper()
		occurred := at
		event, _, err := service.RecordEvent(ctx, owner, RecordEventRequest{
			EventType:      eventType,
			IdempotencyKey: key,
			SessionID:      session,
			SkillVersionID: skillVersionID,
			OccurredAt:     &occurred,
			Payload:        map[string]any{"note": key},
		})
		if err != nil {
			t.Fatalf("record %s: %v", key, err)
		}
		return event
	}

	groundedEvent := record("outcome", "ev-grounded", grounded, base.Add(1*time.Second))
	lintEvent := record("issue", "ev-lint", lint, base.Add(4*time.Second))
	// An event with no skill origin, for example a note the user wrote.
	record("note", "ev-manual", "", base.Add(5*time.Second))

	if groundedEvent.SkillVersionID == nil || *groundedEvent.SkillVersionID != grounded {
		t.Fatalf("grounded event attribution = %v", groundedEvent.SkillVersionID)
	}

	// ── Whole session: both skills, both events, plus the unattributed note ──
	timeline, err := service.SessionTimeline(ctx, owner, SessionTimelineParams{SessionID: session})
	if err != nil {
		t.Fatalf("session timeline: %v", err)
	}
	if timeline.Total != 5 || timeline.SkillLogCount != 2 || timeline.MemoryEventCount != 3 {
		t.Fatalf("timeline counts = %#v", timeline)
	}
	if timeline.UnattributedCount != 1 {
		t.Fatalf("unattributed = %d, want 1", timeline.UnattributedCount)
	}
	if timeline.Truncated {
		t.Fatal("timeline should not report truncation below the limit")
	}
	for i := 1; i < len(timeline.Items); i++ {
		if timeline.Items[i].OccurredAt.Before(timeline.Items[i-1].OccurredAt) {
			t.Fatalf("timeline not time-ordered at %d: %#v", i, timeline.Items)
		}
	}

	// ── Narrowing to one skill version is what session_id alone cannot do ──
	scoped, err := service.SessionTimeline(ctx, owner, SessionTimelineParams{SkillVersionID: lint})
	if err != nil {
		t.Fatalf("scoped timeline: %v", err)
	}
	if scoped.Total != 2 || scoped.SkillLogCount != 1 || scoped.MemoryEventCount != 1 {
		t.Fatalf("scoped counts = %#v", scoped)
	}
	for _, item := range scoped.Items {
		if item.SkillVersionID == nil || *item.SkillVersionID != lint {
			t.Fatalf("scoped timeline leaked another skill: %#v", item)
		}
	}
	if scoped.Items[0].SkillName != "kb-lint" {
		t.Fatalf("skill name not resolved: %#v", scoped.Items[0])
	}
	_ = lintEvent

	// ── An anchor is required; an account-wide dump is not attribution ──
	if _, err := service.SessionTimeline(ctx, owner, SessionTimelineParams{}); err == nil {
		t.Fatal("expected an error without session_id or skill_version_id")
	}
}

// Attribution must not silently succeed across accounts. The composite foreign
// key makes it impossible; the service turns it into a readable error.
func TestRecordEventRejectsForeignSkillVersion(t *testing.T) {
	ctx := context.Background()
	pool := attributionPool(t, ctx)
	ownerA, cleanupA := attributionOwner(t, ctx, pool, "acct-a")
	defer cleanupA()
	ownerB, cleanupB := attributionOwner(t, ctx, pool, "acct-b")
	defer cleanupB()
	service := NewService(NewRepo(pool))

	foreign := createSkillVersion(t, ctx, pool, ownerB, "other-account-skill")

	_, _, err := service.RecordEvent(ctx, ownerA, RecordEventRequest{
		EventType:      "note",
		IdempotencyKey: "ev-foreign",
		SkillVersionID: foreign,
		Payload:        map[string]any{},
	})
	if err == nil {
		t.Fatal("expected cross-account attribution to be rejected")
	}

	_, _, err = service.RecordEvent(ctx, ownerA, RecordEventRequest{
		EventType:      "note",
		IdempotencyKey: "ev-missing",
		SkillVersionID: "00000000-0000-0000-0000-000000000000",
		Payload:        map[string]any{},
	})
	if err == nil {
		t.Fatal("expected unknown skill version to be rejected")
	}
}

// Attribution is part of the event's identity, so a replay that adds it must not
// quietly return the original unattributed row.
func TestReplayCannotSilentlyAddAttribution(t *testing.T) {
	ctx := context.Background()
	pool := attributionPool(t, ctx)
	owner, cleanup := attributionOwner(t, ctx, pool, "replay")
	defer cleanup()
	service := NewService(NewRepo(pool))

	version := createSkillVersion(t, ctx, pool, owner, "replay-skill")
	occurred := time.Now().UTC().Truncate(time.Second)

	first, created, err := service.RecordEvent(ctx, owner, RecordEventRequest{
		EventType:      "outcome",
		IdempotencyKey: "ev-replay",
		Payload:        map[string]any{"n": 1},
		OccurredAt:     &occurred,
	})
	if err != nil || !created {
		t.Fatalf("first record: err=%v created=%v", err, created)
	}
	if first.SkillVersionID != nil {
		t.Fatalf("expected no attribution, got %v", first.SkillVersionID)
	}

	// Same key, now with attribution: the content differs, so this is a conflict
	// rather than a successful replay.
	if _, _, err := service.RecordEvent(ctx, owner, RecordEventRequest{
		EventType:      "outcome",
		IdempotencyKey: "ev-replay",
		Payload:        map[string]any{"n": 1},
		OccurredAt:     &occurred,
		SkillVersionID: version,
	}); err == nil {
		t.Fatal("expected an idempotency conflict when attribution changes")
	}

	// An identical replay still returns the same row.
	again, created, err := service.RecordEvent(ctx, owner, RecordEventRequest{
		EventType:      "outcome",
		IdempotencyKey: "ev-replay",
		Payload:        map[string]any{"n": 1},
		OccurredAt:     &occurred,
	})
	if err != nil {
		t.Fatalf("identical replay: %v", err)
	}
	if created || again.ID != first.ID {
		t.Fatalf("replay created=%v id=%s want existing %s", created, again.ID, first.ID)
	}
}

// The reverse direction: from a durable memory back to the execution that
// produced it, reporting how far the chain resolved.
func TestEntryAttributionResolvesChain(t *testing.T) {
	ctx := context.Background()
	pool := attributionPool(t, ctx)
	owner, cleanup := attributionOwner(t, ctx, pool, "reverse")
	defer cleanup()
	service := NewService(NewRepo(pool))

	const session = "session-reverse"
	version := createSkillVersion(t, ctx, pool, owner, "reverse-skill")
	base := time.Now().UTC().Truncate(time.Second)
	recordSkillLog(t, ctx, pool, owner, version, "reverse-skill", session, "success", base)

	newEntry := func(idempotencyKey string, req RecordEventRequest) *EntryDetail {
		t.Helper()
		var sourceEventID *string
		if idempotencyKey != "" {
			event, _, err := service.RecordEvent(ctx, owner, req)
			if err != nil {
				t.Fatalf("record event %s: %v", idempotencyKey, err)
			}
			sourceEventID = &event.ID
		}
		create := CreateEntryRequest{
			MemoryType:    "procedural",
			Content:       "attribution fixture " + idempotencyKey,
			SourceEventID: sourceEventID,
		}
		if sourceEventID == nil {
			// A durable memory always needs evidence; without a source event the
			// evidence must be supplied directly. This is the "written directly"
			// case, which is exactly what resolution "none" describes.
			create.Evidence = []EvidenceInput{{
				SourceType: "manual",
				SourceID:   "operator-note",
				Excerpt:    "recorded by hand",
			}}
		}
		entry, err := service.CreateEntry(ctx, owner, create)
		if err != nil {
			t.Fatalf("create entry %s: %v", idempotencyKey, err)
		}
		return entry
	}

	occurred := base.Add(time.Second)
	attributed := newEntry("ev-attributed", RecordEventRequest{
		EventType:      "outcome",
		IdempotencyKey: "ev-attributed",
		SessionID:      session,
		SkillVersionID: version,
		OccurredAt:     &occurred,
		Payload:        map[string]any{},
	})
	sessionOnly := newEntry("ev-session-only", RecordEventRequest{
		EventType:      "outcome",
		IdempotencyKey: "ev-session-only",
		SessionID:      session,
		OccurredAt:     &occurred,
		Payload:        map[string]any{},
	})
	eventOnly := newEntry("ev-no-session", RecordEventRequest{
		EventType:      "note",
		IdempotencyKey: "ev-no-session",
		OccurredAt:     &occurred,
		Payload:        map[string]any{},
	})
	direct := newEntry("", RecordEventRequest{})

	for _, testCase := range []struct {
		name           string
		entryID        string
		wantResolution string
		wantSkill      string
		wantTimeline   bool
	}{
		{"full chain", attributed.Entry.ID, "skill_version", "reverse-skill", true},
		{"session but no skill", sessionOnly.Entry.ID, "session_only", "", true},
		{"event but no session", eventOnly.Entry.ID, "event_only", "", false},
		{"written directly", direct.Entry.ID, "none", "", false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			attribution, err := service.EntryAttribution(ctx, owner, testCase.entryID)
			if err != nil {
				t.Fatalf("attribution: %v", err)
			}
			if attribution.Resolution != testCase.wantResolution {
				t.Fatalf("resolution = %q, want %q (%#v)", attribution.Resolution, testCase.wantResolution, attribution)
			}
			if attribution.SkillName != testCase.wantSkill {
				t.Fatalf("skill name = %q, want %q", attribution.SkillName, testCase.wantSkill)
			}
			if testCase.wantTimeline && len(attribution.SessionTimeline) == 0 {
				t.Fatal("expected the surrounding session timeline")
			}
			if !testCase.wantTimeline && len(attribution.SessionTimeline) != 0 {
				t.Fatalf("unexpected timeline: %#v", attribution.SessionTimeline)
			}
		})
	}
}

// Deleting an account must remove its skill content rather than orphaning it.
// Before 000025 the account_id foreign keys were ON DELETE SET NULL, so the rows
// survived with no owner and a second deletion of a same-named active skill
// collided with idx_skill_versions_global_active, failing the delete outright.
func TestAccountDeletionRemovesSkillRowsInsteadOfOrphaningThem(t *testing.T) {
	ctx := context.Background()
	pool := attributionPool(t, ctx)

	orphanCount := func() int {
		t.Helper()
		var count int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM skill_versions WHERE account_id IS NULL`).Scan(&count); err != nil {
			t.Fatalf("count orphans: %v", err)
		}
		return count
	}
	before := orphanCount()

	// Two accounts owning an active skill with the same name: this is the pair
	// that used to make the second delete fail.
	const sharedName = "cascade-shared-skill"
	for _, label := range []string{"cascade-a", "cascade-b"} {
		owner, cleanup := attributionOwner(t, ctx, pool, label)
		version := createSkillVersion(t, ctx, pool, owner, sharedName)
		recordSkillLog(t, ctx, pool, owner, version, sharedName, "session-"+label, "success", time.Now().UTC())
		service := NewService(NewRepo(pool))
		if _, _, err := service.RecordEvent(ctx, owner, RecordEventRequest{
			EventType:      "outcome",
			IdempotencyKey: "ev-" + label,
			SessionID:      "session-" + label,
			SkillVersionID: version,
			Payload:        map[string]any{},
		}); err != nil {
			t.Fatalf("record event for %s: %v", label, err)
		}
		// cleanup deletes the account; it must not error.
		cleanup()

		var remaining int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM skill_versions WHERE id = $1::uuid`, version).Scan(&remaining); err != nil {
			t.Fatalf("count remaining: %v", err)
		}
		if remaining != 0 {
			t.Fatalf("%s: skill version survived account deletion", label)
		}
	}

	if after := orphanCount(); after != before {
		t.Fatalf("orphaned skill versions changed from %d to %d", before, after)
	}
}
