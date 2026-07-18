package skills

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wellxie/agentmate/internal/ownership"
)

func TestSnapshotIdentityIntegration(t *testing.T) {
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

	var accountID string
	if err := pool.QueryRow(ctx, `INSERT INTO accounts (name) VALUES ('skills integration') RETURNING id::text`).Scan(&accountID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	defer func() {
		if _, err := pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, accountID); err != nil {
			t.Errorf("clean up account: %v", err)
		}
	}()

	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, account_id) VALUES ($1, 'test', $2) RETURNING id::text`,
		"skills-"+accountID+"@example.test", accountID,
	).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}

	owner := ownership.Owner{AccountID: accountID, UserID: userID}
	repo := NewRepo(pool)
	service := NewService(repo)
	source, err := service.CreateSource(ctx, owner, CreateSkillSourceRequest{
		Name:          "identity-integration",
		Type:          "local",
		RepositoryURL: "file:///identity-integration",
		PackagePath:   "skill",
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}

	firstRequest := integrationSnapshotRequest("snapshot-a", "resource-a")
	const parallelReplays = 6
	responses := make([]*SubmitLocalSnapshotResponse, parallelReplays)
	errors := make([]error, parallelReplays)
	var waitGroup sync.WaitGroup
	for index := range responses {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			responses[index], errors[index] = service.SubmitLocalSnapshot(ctx, owner, source.ID, firstRequest)
		}(index)
	}
	waitGroup.Wait()

	for index, callErr := range errors {
		if callErr != nil {
			t.Fatalf("parallel replay %d: %v", index, callErr)
		}
		if responses[index].Revision.ID != responses[0].Revision.ID || responses[index].Version.ID != responses[0].Version.ID {
			t.Fatalf("parallel replay %d returned a different immutable identity", index)
		}
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO skill_source_revision_aliases
		 (account_id, user_id, source_id, revision_id, local_snapshot_id)
		 VALUES ($1, $2, $3, $4, 'snapshot-legacy')`,
		accountID, userID, source.ID, responses[0].Revision.ID,
	); err != nil {
		t.Fatalf("create historical snapshot alias: %v", err)
	}
	legacyAliasRequest := integrationSnapshotRequest("snapshot-legacy", "resource-a")
	legacyAliasResponse, err := service.SubmitLocalSnapshot(ctx, owner, source.ID, legacyAliasRequest)
	if err != nil {
		t.Fatalf("replay historical snapshot alias: %v", err)
	}
	if legacyAliasResponse.Revision.ID != responses[0].Revision.ID || legacyAliasResponse.Version.ID != responses[0].Version.ID {
		t.Fatalf("historical snapshot alias returned a different immutable identity")
	}

	aliasRequest := integrationSnapshotRequest("snapshot-b", "resource-a")
	if _, err := service.SubmitLocalSnapshot(ctx, owner, source.ID, aliasRequest); err == nil || !strings.Contains(err.Error(), "already bound to a different local snapshot ID") {
		t.Fatalf("unpersisted snapshot alias error = %v", err)
	}

	requests := []SubmitLocalSnapshotRequest{
		integrationSnapshotRequest("snapshot-b", "resource-b"),
		integrationSnapshotRequest("snapshot-c", "resource-c"),
	}
	concurrentErrors := make([]error, len(requests))
	waitGroup = sync.WaitGroup{}
	for index := range requests {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			_, concurrentErrors[index] = service.SubmitLocalSnapshot(ctx, owner, source.ID, requests[index])
		}(index)
	}
	waitGroup.Wait()
	for index, callErr := range concurrentErrors {
		if callErr != nil {
			t.Fatalf("concurrent package %d: %v", index, callErr)
		}
	}

	conflictingRequest := integrationSnapshotRequest("snapshot-b", "resource-a")
	if _, err := service.SubmitLocalSnapshot(ctx, owner, source.ID, conflictingRequest); err == nil || !strings.Contains(err.Error(), "refer to different source revisions") {
		t.Fatalf("crossed revision identities error = %v", err)
	}

	listedVersions, err := repo.ListVersions(ctx, accountID, VersionListParams{SkillName: "identity-integration"})
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(listedVersions) != 3 {
		t.Fatalf("listed versions = %d, want 3", len(listedVersions))
	}
	activationErrors := make([]error, len(listedVersions))
	waitGroup = sync.WaitGroup{}
	for index := range listedVersions {
		versionFiles, err := repo.ListVersionFiles(ctx, accountID, listedVersions[index].ID)
		if err != nil {
			t.Fatalf("list version %d files: %v", index, err)
		}
		if len(versionFiles) != 2 {
			t.Fatalf("version %d files = %d, want 2", index, len(versionFiles))
		}
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			_, activationErrors[index] = repo.ActivateVersion(ctx, accountID, listedVersions[index].ID)
		}(index)
	}
	waitGroup.Wait()
	for index, activateErr := range activationErrors {
		if activateErr != nil {
			t.Fatalf("concurrent activation %d: %v", index, activateErr)
		}
	}

	var versions, revisions, files, active, consistentLinks int
	if err := pool.QueryRow(ctx,
		`SELECT
		   (SELECT count(*) FROM skill_versions WHERE account_id = $1 AND skill_name = 'identity-integration'),
		   (SELECT count(*) FROM skill_source_revisions WHERE account_id = $1 AND source_id = $2),
		   (SELECT count(*) FROM skill_version_files WHERE account_id = $1),
		   (SELECT count(*) FROM skill_versions WHERE account_id = $1 AND skill_name = 'identity-integration' AND is_active),
		   (SELECT count(*)
		      FROM skill_source_revisions AS revision
		      JOIN skill_versions AS version
		        ON version.id = revision.skill_version_id
		       AND version.source_revision_id = revision.id
		       AND version.source_id = revision.source_id
		       AND version.package_hash = revision.package_hash
		      JOIN skill_version_files AS file
		        ON file.source_revision_id = revision.id
		       AND file.version_id = version.id
		     WHERE revision.account_id = $1 AND revision.source_id = $2)`,
		accountID, source.ID,
	).Scan(&versions, &revisions, &files, &active, &consistentLinks); err != nil {
		t.Fatalf("query package relationships: %v", err)
	}
	if versions != 3 || revisions != 3 || files != 6 || active != 1 || consistentLinks != 6 {
		t.Fatalf("versions=%d revisions=%d files=%d active=%d consistent_links=%d", versions, revisions, files, active, consistentLinks)
	}
}

func integrationSnapshotRequest(snapshotID, resource string) SubmitLocalSnapshotRequest {
	activate := true
	index := false
	return SubmitLocalSnapshotRequest{
		SnapshotID: snapshotID,
		Activate:   &activate,
		Index:      &index,
		Files: []SnapshotFile{
			{
				Path:    "SKILL.md",
				Content: "---\nname: identity-integration\ndescription: Integration test\n---\n\n# Identity\n",
			},
			{
				Path:    "resources/data.txt",
				Content: resource,
			},
		},
	}
}

func TestGitSyncIntegration(t *testing.T) {
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

	var accountID string
	if err := pool.QueryRow(ctx, `INSERT INTO accounts (name) VALUES ('git sync integration') RETURNING id::text`).Scan(&accountID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	defer func() {
		if _, err := pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, accountID); err != nil {
			t.Errorf("clean up account: %v", err)
		}
	}()

	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, account_id) VALUES ($1, 'test', $2) RETURNING id::text`,
		"git-sync-"+accountID+"@example.test", accountID,
	).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}

	providerServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		const commitPrefix = "/repos/acme/skills/commits/"
		const archivePrefix = "/repos/acme/skills/tarball/"
		switch {
		case strings.HasPrefix(request.URL.Path, commitPrefix):
			ref := strings.TrimPrefix(request.URL.Path, commitPrefix)
			if ref == "failure" {
				http.Error(writer, "provider unavailable", http.StatusBadGateway)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"sha":"commit-` + ref + `"}`))
		case strings.HasPrefix(request.URL.Path, archivePrefix):
			commit := strings.TrimPrefix(request.URL.Path, archivePrefix)
			resource := map[string]string{
				"commit-one":   "resource-a",
				"commit-two":   "resource-a",
				"commit-three": "resource-b",
				"commit-four":  "resource-c",
			}[commit]
			if resource == "" {
				http.NotFound(writer, request)
				return
			}
			archive := buildTestGitArchive(t, []testArchiveEntry{
				{name: "repository-root/skills/demo/SKILL.md", content: "---\nname: git-integration\ndescription: Git integration\n---\n"},
				{name: "repository-root/skills/demo/resources/data.txt", content: resource},
			})
			_, _ = writer.Write(archive)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer providerServer.Close()

	owner := ownership.Owner{AccountID: accountID, UserID: userID}
	repo := NewRepo(pool)
	service := NewService(repo)
	service.gitProvider = newGitProviderClient(providerServer.Client())
	service.gitProvider.githubAPIBaseURL = providerServer.URL

	source, err := service.CreateSource(ctx, owner, CreateSkillSourceRequest{
		Name:          "git-integration",
		Type:          "git",
		RepositoryURL: "https://github.com/acme/skills.git",
		PackagePath:   "skills/demo",
		DefaultRef:    "main",
	})
	if err != nil {
		t.Fatalf("create Git source: %v", err)
	}
	index := false
	first, err := service.SyncGitSource(ctx, owner, source.ID, SyncGitSourceRequest{Ref: "one", Index: &index})
	if err != nil {
		t.Fatalf("first Git sync: %v", err)
	}
	if first.CommitSHA != "commit-one" || first.Revision.CommitSHA != "commit-one" || len(first.Files) != 2 {
		t.Fatalf("first Git sync response = %#v", first)
	}
	assertGitSyncState(t, first.Source, "succeeded", "commit-one")

	second, err := service.SyncGitSource(ctx, owner, source.ID, SyncGitSourceRequest{Ref: "two", Index: &index})
	if err != nil {
		t.Fatalf("same package Git sync: %v", err)
	}
	if second.Revision.ID != first.Revision.ID || second.Version.ID != first.Version.ID {
		t.Fatal("same package at a new commit created a duplicate revision or version")
	}
	if second.CommitSHA != "commit-two" {
		t.Fatalf("resolved commit = %q, want commit-two", second.CommitSHA)
	}

	third, err := service.SyncGitSource(ctx, owner, source.ID, SyncGitSourceRequest{Ref: "three", Index: &index})
	if err != nil {
		t.Fatalf("changed package Git sync: %v", err)
	}
	if third.Revision.ID == first.Revision.ID || third.Version.ID == first.Version.ID {
		t.Fatal("changed package did not create a new immutable identity")
	}

	if _, err := service.SyncGitSource(ctx, owner, source.ID, SyncGitSourceRequest{Ref: "failure", Index: &index}); err == nil {
		t.Fatal("expected provider failure")
	}
	failedSource, err := repo.GetSource(ctx, accountID, source.ID)
	if err != nil {
		t.Fatalf("get failed source: %v", err)
	}
	if failedSource.Status != "error" {
		t.Fatalf("failed source status = %q, want error", failedSource.Status)
	}
	assertGitSyncState(t, failedSource, "failed", "")

	recovered, err := service.SyncGitSource(ctx, owner, source.ID, SyncGitSourceRequest{Ref: "four", Index: &index})
	if err != nil {
		t.Fatalf("recover Git sync: %v", err)
	}
	if recovered.Source.Status != "active" {
		t.Fatalf("recovered source status = %q, want active", recovered.Source.Status)
	}
	assertGitSyncState(t, recovered.Source, "succeeded", "commit-four")

	var versions, revisions, active int
	if err := pool.QueryRow(ctx,
		`SELECT
		   (SELECT count(*) FROM skill_versions WHERE account_id = $1 AND skill_name = 'git-integration'),
		   (SELECT count(*) FROM skill_source_revisions WHERE account_id = $1 AND source_id = $2),
		   (SELECT count(*) FROM skill_versions WHERE account_id = $1 AND skill_name = 'git-integration' AND is_active)`,
		accountID, source.ID,
	).Scan(&versions, &revisions, &active); err != nil {
		t.Fatalf("query Git sync identities: %v", err)
	}
	if versions != 3 || revisions != 3 || active != 1 {
		t.Fatalf("versions=%d revisions=%d active=%d", versions, revisions, active)
	}
}

func assertGitSyncState(t *testing.T, source *SkillSource, status, commitSHA string) {
	t.Helper()
	var metadata struct {
		GitSync GitSourceSyncState `json:"git_sync"`
	}
	if err := json.Unmarshal(source.Metadata, &metadata); err != nil {
		t.Fatalf("decode source metadata: %v", err)
	}
	if metadata.GitSync.Status != status {
		t.Fatalf("git_sync.status = %q, want %q", metadata.GitSync.Status, status)
	}
	if commitSHA != "" && metadata.GitSync.CommitSHA != commitSHA {
		t.Fatalf("git_sync.commit_sha = %q, want %q", metadata.GitSync.CommitSHA, commitSHA)
	}
}
