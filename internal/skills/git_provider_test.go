package skills

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseGitRepositoryURL(t *testing.T) {
	tests := []struct {
		name        string
		repository  string
		provider    string
		projectPath string
	}{
		{name: "github", repository: "https://github.com/acme/skills.git", provider: "github", projectPath: "acme/skills"},
		{name: "github trailing slash", repository: "https://github.com/acme/skills/", provider: "github", projectPath: "acme/skills"},
		{name: "github dot git trailing slash", repository: "https://github.com/acme/skills.git/", provider: "github", projectPath: "acme/skills"},
		{name: "gitlab nested group", repository: "https://gitlab.com/acme/platform/skills.git", provider: "gitlab", projectPath: "acme/platform/skills"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository, err := parseGitRepositoryURL(test.repository)
			if err != nil {
				t.Fatalf("parseGitRepositoryURL() error: %v", err)
			}
			if repository.Provider != test.provider || repository.ProjectPath != test.projectPath {
				t.Fatalf("repository = %#v, want provider=%q projectPath=%q", repository, test.provider, test.projectPath)
			}
		})
	}
}

func TestParseGitRepositoryURLRejectsUnsupportedOrUnsafeURLs(t *testing.T) {
	for _, repositoryURL := range []string{
		"http://github.com/acme/skills",
		"https://user@github.com/acme/skills",
		"https://github.com/acme/skills?ref=main",
		"https://github.com/acme/skills/tree/main",
		"https://gitlab.example.com/acme/skills",
		"https://gitlab.com/acme",
		"file:///tmp/skills",
	} {
		t.Run(repositoryURL, func(t *testing.T) {
			if _, err := parseGitRepositoryURL(repositoryURL); err == nil {
				t.Fatal("expected repository URL rejection")
			}
		})
	}
}

func TestGitProviderClientResolveGitHubDefaultBranch(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.URL.EscapedPath())
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/repos/acme/skills":
			_, _ = writer.Write([]byte(`{"default_branch":"trunk"}`))
		case "/repos/acme/skills/commits/trunk":
			_, _ = writer.Write([]byte(`{"sha":"github-commit-sha"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client := newGitProviderClient(server.Client())
	client.githubAPIBaseURL = server.URL
	resolved, err := client.Resolve(context.Background(), "https://github.com/acme/skills.git", "", "")
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if resolved.Provider != "github" || resolved.Ref != "trunk" || resolved.CommitSHA != "github-commit-sha" {
		t.Fatalf("resolved = %#v", resolved)
	}
	if resolved.ArchiveURL != server.URL+"/repos/acme/skills/tarball/github-commit-sha" {
		t.Fatalf("ArchiveURL = %q", resolved.ArchiveURL)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %v, want 2 calls", requests)
	}
}

func TestGitProviderClientResolveGitLabRequestedRef(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if !strings.Contains(request.URL.Path, "/repository/commits/release/v1") {
			http.NotFound(writer, request)
			return
		}
		_, _ = writer.Write([]byte(`{"id":"gitlab-commit-sha"}`))
	}))
	defer server.Close()

	client := newGitProviderClient(server.Client())
	client.gitlabAPIBaseURL = server.URL
	resolved, err := client.Resolve(context.Background(), "https://gitlab.com/acme/platform/skills.git", "release/v1", "ignored")
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if resolved.Provider != "gitlab" || resolved.ProjectPath != "acme/platform/skills" || resolved.Ref != "release/v1" || resolved.CommitSHA != "gitlab-commit-sha" {
		t.Fatalf("resolved = %#v", resolved)
	}
	wantArchiveURL := server.URL + "/projects/acme%2Fplatform%2Fskills/repository/archive.tar.gz?sha=gitlab-commit-sha"
	if resolved.ArchiveURL != wantArchiveURL {
		t.Fatalf("ArchiveURL = %q, want %q", resolved.ArchiveURL, wantArchiveURL)
	}
}

func TestGitProviderClientReportsProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "missing repository", http.StatusNotFound)
	}))
	defer server.Close()

	client := newGitProviderClient(server.Client())
	client.githubAPIBaseURL = server.URL
	_, err := client.Resolve(context.Background(), "https://github.com/acme/missing", "main", "")
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("Resolve() error = %v", err)
	}
}
