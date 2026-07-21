package skills

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestQualityRunIntegration(t *testing.T) {
	databaseURL := os.Getenv("AGENTMATE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("AGENTMATE_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	defer pool.Close()

	owner := createSkillIntegrationOwner(t, ctx, pool, "quality-run")
	otherOwner := createSkillIntegrationOwner(t, ctx, pool, "quality-run-other")
	defer deleteSkillIntegrationAccount(t, ctx, pool, otherOwner.Account())
	createdVersionIDs := make([]string, 0, 3)
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM skill_quality_runs WHERE account_id = $1`, owner.Account())
		_, _ = pool.Exec(ctx, `UPDATE skill_logs SET skill_version_id = NULL WHERE account_id = $1`, owner.Account())
		deleteSkillIntegrationAccount(t, ctx, pool, owner.Account())
		for _, versionID := range createdVersionIDs {
			_, _ = pool.Exec(ctx, `DELETE FROM skill_versions WHERE id = $1`, versionID)
		}
	}()

	repo := NewRepo(pool)
	service := NewService(repo)
	baseline, err := service.CreateVersion(ctx, owner, qualityDirectVersionRequest("quality-integration", "v1", "baseline"))
	if err != nil {
		t.Fatalf("create baseline: %v", err)
	}
	createdVersionIDs = append(createdVersionIDs, baseline.ID)
	current, err := service.CreateVersion(ctx, owner, qualityDirectVersionRequest("quality-integration", "v2", "current"))
	if err != nil {
		t.Fatalf("create current: %v", err)
	}
	createdVersionIDs = append(createdVersionIDs, current.ID)
	wrongSkill, err := service.CreateVersion(ctx, owner, qualityDirectVersionRequest("other-skill", "v1", "other"))
	if err != nil {
		t.Fatalf("create wrong-skill version: %v", err)
	}
	createdVersionIDs = append(createdVersionIDs, wrongSkill.ID)

	for index := 0; index < 20; index++ {
		outcome := "success"
		if index < 3 {
			outcome = "failure"
		}
		logEntry, createErr := service.CreateLog(ctx, owner, CreateLogRequest{
			SkillName:      current.SkillName,
			SkillVersionID: current.ID,
			SkillVersion:   "untrusted-label",
			AgentID:        "quality-integration",
			Outcome:        outcome,
		})
		if createErr != nil {
			t.Fatalf("create version log %d: %v", index, createErr)
		}
		if logEntry.SkillVersion != current.Version || logEntry.SkillVersionID == nil || *logEntry.SkillVersionID != current.ID {
			t.Fatalf("canonical log version = %#v", logEntry)
		}
	}
	if _, err := service.CreateLog(ctx, owner, CreateLogRequest{SkillName: current.SkillName, SkillVersion: "legacy", AgentID: "quality-integration", Outcome: "failure"}); err != nil {
		t.Fatalf("create unassigned log: %v", err)
	}
	if _, err := service.CreateLog(ctx, otherOwner, CreateLogRequest{SkillName: current.SkillName, SkillVersionID: current.ID, AgentID: "quality-integration", Outcome: "failure"}); err == nil {
		t.Fatal("cross-account skill_version_id log succeeded")
	}

	before := qualitySideEffectCounts(t, ctx, pool, owner.Account())
	run, err := service.RunQuality(ctx, owner.Account(), current.ID, CreateQualityRunRequest{BaselineVersionID: baseline.ID})
	if err != nil {
		t.Fatalf("run quality: %v", err)
	}
	if run.Status != "completed" || run.Report.Telemetry.Considered != 20 || run.Report.Telemetry.Triggered != 20 || run.Report.Telemetry.Status != "sufficient" {
		t.Fatalf("quality run telemetry = %#v", run)
	}
	if len(run.Report.Telemetry.Suggestions) != 1 || run.Report.Telemetry.Suggestions[0].Category != "failure" {
		t.Fatalf("quality suggestions = %#v", run.Report.Telemetry.Suggestions)
	}
	if _, err := service.RunQuality(ctx, owner.Account(), current.ID, CreateQualityRunRequest{BaselineVersionID: wrongSkill.ID}); err == nil {
		t.Fatal("wrong-skill baseline succeeded")
	}
	if _, err := service.GetQualityRun(ctx, otherOwner.Account(), run.ID); err == nil {
		t.Fatal("cross-account quality run read succeeded")
	}

	second, err := service.RunQuality(ctx, owner.Account(), current.ID, CreateQualityRunRequest{})
	if err != nil {
		t.Fatalf("second quality run with auto baseline: %v", err)
	}
	if second.BaselineVersionID == nil || *second.BaselineVersionID != baseline.ID {
		t.Fatalf("auto baseline = %#v, want %s", second.BaselineVersionID, baseline.ID)
	}
	firstPage, err := service.ListQualityRuns(ctx, owner.Account(), current.ID, QualityRunListParams{Limit: 1})
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	secondPage, err := service.ListQualityRuns(ctx, owner.Account(), current.ID, QualityRunListParams{Limit: 1, Offset: 1})
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	if firstPage.Total != 2 || len(firstPage.Items) != 1 || len(secondPage.Items) != 1 || firstPage.Items[0].ID != second.ID || secondPage.Items[0].ID != run.ID {
		t.Fatalf("stable quality pagination first=%#v second=%#v", firstPage, secondPage)
	}
	encodedList, err := json.Marshal(firstPage)
	if err != nil {
		t.Fatalf("marshal quality list: %v", err)
	}
	if strings.Contains(string(encodedList), `"report"`) {
		t.Fatalf("quality list amplified full report: %s", encodedList)
	}

	after := qualitySideEffectCounts(t, ctx, pool, owner.Account())
	if before.Versions != after.Versions || before.Active != after.Active || before.Artifacts != after.Artifacts || before.RetrievalDocuments != after.RetrievalDocuments || before.Logs != after.Logs {
		t.Fatalf("quality run changed registry side effects: before=%#v after=%#v", before, after)
	}
	if after.QualityRuns != before.QualityRuns+2 {
		t.Fatalf("quality run count before=%d after=%d", before.QualityRuns, after.QualityRuns)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM skill_versions WHERE account_id = $1 AND id = $2`, owner.Account(), current.ID); err == nil {
		t.Fatal("delete referenced target version succeeded")
	}

	if _, err := pool.Exec(ctx, `DELETE FROM skill_versions WHERE account_id = $1 AND id = $2`, owner.Account(), baseline.ID); err != nil {
		t.Fatalf("delete referenced baseline: %v", err)
	}
	var storedBaselineID *string
	if err := pool.QueryRow(ctx,
		`SELECT baseline_version_id FROM skill_quality_runs WHERE account_id = $1 AND id = $2`,
		owner.Account(), run.ID,
	).Scan(&storedBaselineID); err != nil {
		t.Fatalf("read quality run after baseline delete: %v", err)
	}
	if storedBaselineID != nil {
		t.Fatalf("baseline_version_id after delete = %q, want null", *storedBaselineID)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, owner.Account()); err != nil {
		t.Fatalf("delete account with referenced target version: %v", err)
	}
	var remainingRuns int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM skill_quality_runs WHERE id = ANY($1::uuid[])`,
		[]string{run.ID, second.ID},
	).Scan(&remainingRuns); err != nil {
		t.Fatalf("count quality runs after account delete: %v", err)
	}
	if remainingRuns != 0 {
		t.Fatalf("quality runs remaining after account delete = %d", remainingRuns)
	}
}

type qualityDBCounts struct {
	Versions           int
	Active             int
	Artifacts          int
	RetrievalDocuments int
	Logs               int
	QualityRuns        int
}

func qualitySideEffectCounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, accountID string) qualityDBCounts {
	t.Helper()
	var counts qualityDBCounts
	if err := pool.QueryRow(ctx,
		`SELECT
		   (SELECT count(*) FROM skill_versions WHERE account_id = $1),
		   (SELECT count(*) FROM skill_versions WHERE account_id = $1 AND is_active),
		   (SELECT count(*) FROM skill_compiled_catalogs WHERE account_id = $1),
		   (SELECT count(*) FROM retrieval_documents WHERE account_id = $1),
		   (SELECT count(*) FROM skill_logs WHERE account_id = $1),
		   (SELECT count(*) FROM skill_quality_runs WHERE account_id = $1)`,
		accountID,
	).Scan(&counts.Versions, &counts.Active, &counts.Artifacts, &counts.RetrievalDocuments, &counts.Logs, &counts.QualityRuns); err != nil {
		t.Fatalf("query side-effect counts: %v", err)
	}
	return counts
}

func qualityDirectVersionRequest(skillName, version, marker string) CreateVersionRequest {
	return CreateVersionRequest{
		SkillName: skillName,
		Version:   version,
		Content:   "---\nname: " + skillName + "\ndescription: " + marker + "\ntriggers: [quality]\ncapabilities: [lint]\nconstraints: [offline]\ndependencies: []\n---\n\n# Instructions\n",
		AgentID:   "quality-integration",
	}
}
