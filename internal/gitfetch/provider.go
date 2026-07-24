// Package gitfetch provides bounded, provider-neutral fetching of public
// GitHub/GitLab package directories at an immutable commit.
//
// The parsing, commit resolution, and archive extraction logic mirrors
// internal/skills (git_provider.go / git_archive.go) and must stay
// behaviorally consistent with it. It is intentionally a separate package so
// the Knowledge Registry can reuse the capability without changing any
// public behavior of the skills package; a later increment may migrate
// skills onto this package.
package gitfetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxProviderResponseBytes = 1024 * 1024

// Repository identifies a public GitHub or GitLab project.
type Repository struct {
	Provider    string
	ProjectPath string
}

// ResolvedRevision is a ref resolved to an immutable commit plus the archive URL.
type ResolvedRevision struct {
	Provider    string
	ProjectPath string
	Ref         string
	CommitSHA   string
	ArchiveURL  string
}

// File is one extracted package file with raw content bytes.
type File struct {
	Path    string
	Content []byte
}

// Client resolves refs and downloads bounded package archives from public
// GitHub/GitLab over HTTPS only.
type Client struct {
	httpClient       *http.Client
	githubAPIBaseURL string
	gitlabAPIBaseURL string
}

func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		httpClient:       httpClient,
		githubAPIBaseURL: "https://api.github.com",
		gitlabAPIBaseURL: "https://gitlab.com/api/v4",
	}
}

func (c *Client) Resolve(ctx context.Context, repositoryURL, requestedRef, defaultRef string) (ResolvedRevision, error) {
	repository, err := ParseRepositoryURL(repositoryURL)
	if err != nil {
		return ResolvedRevision{}, err
	}

	ref := strings.TrimSpace(requestedRef)
	if ref == "" {
		ref = strings.TrimSpace(defaultRef)
	}
	if ref == "" {
		ref, err = c.defaultBranch(ctx, repository)
		if err != nil {
			return ResolvedRevision{}, err
		}
	}

	commitSHA, err := c.resolveCommit(ctx, repository, ref)
	if err != nil {
		return ResolvedRevision{}, err
	}
	return ResolvedRevision{
		Provider:    repository.Provider,
		ProjectPath: repository.ProjectPath,
		Ref:         ref,
		CommitSHA:   commitSHA,
		ArchiveURL:  c.archiveURL(repository, commitSHA),
	}, nil
}

func (c *Client) defaultBranch(ctx context.Context, repository Repository) (string, error) {
	var response struct {
		DefaultBranch string `json:"default_branch"`
	}
	endpoint := ""
	switch repository.Provider {
	case "github":
		endpoint = strings.TrimRight(c.githubAPIBaseURL, "/") + "/repos/" + repository.ProjectPath
	case "gitlab":
		endpoint = strings.TrimRight(c.gitlabAPIBaseURL, "/") + "/projects/" + url.PathEscape(repository.ProjectPath)
	default:
		return "", fmt.Errorf("unsupported Git provider: %s", repository.Provider)
	}
	if err := c.getJSON(ctx, endpoint, &response); err != nil {
		return "", fmt.Errorf("resolve %s default branch: %w", repository.Provider, err)
	}
	if strings.TrimSpace(response.DefaultBranch) == "" {
		return "", fmt.Errorf("%s repository returned an empty default branch", repository.Provider)
	}
	return response.DefaultBranch, nil
}

func (c *Client) resolveCommit(ctx context.Context, repository Repository, ref string) (string, error) {
	var response struct {
		SHA string `json:"sha"`
		ID  string `json:"id"`
	}
	endpoint := ""
	switch repository.Provider {
	case "github":
		endpoint = strings.TrimRight(c.githubAPIBaseURL, "/") + "/repos/" + repository.ProjectPath + "/commits/" + url.PathEscape(ref)
	case "gitlab":
		endpoint = strings.TrimRight(c.gitlabAPIBaseURL, "/") + "/projects/" + url.PathEscape(repository.ProjectPath) + "/repository/commits/" + url.PathEscape(ref)
	default:
		return "", fmt.Errorf("unsupported Git provider: %s", repository.Provider)
	}
	if err := c.getJSON(ctx, endpoint, &response); err != nil {
		return "", fmt.Errorf("resolve %s ref %q: %w", repository.Provider, ref, err)
	}
	commitSHA := strings.TrimSpace(response.SHA)
	if commitSHA == "" {
		commitSHA = strings.TrimSpace(response.ID)
	}
	if commitSHA == "" {
		return "", fmt.Errorf("%s ref %q returned an empty commit SHA", repository.Provider, ref)
	}
	return commitSHA, nil
}

func (c *Client) archiveURL(repository Repository, commitSHA string) string {
	switch repository.Provider {
	case "github":
		return strings.TrimRight(c.githubAPIBaseURL, "/") + "/repos/" + repository.ProjectPath + "/tarball/" + url.PathEscape(commitSHA)
	case "gitlab":
		return strings.TrimRight(c.gitlabAPIBaseURL, "/") + "/projects/" + url.PathEscape(repository.ProjectPath) + "/repository/archive.tar.gz?sha=" + url.QueryEscape(commitSHA)
	default:
		return ""
	}
}

func (c *Client) getJSON(ctx context.Context, endpoint string, destination any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "agentmate-registry")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxProviderResponseBytes+1))
	if err != nil {
		return err
	}
	if len(body) > maxProviderResponseBytes {
		return fmt.Errorf("provider response is too large")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		detail := strings.TrimSpace(string(body))
		if len(detail) > 300 {
			detail = detail[:300]
		}
		return fmt.Errorf("provider returned HTTP %d: %s", response.StatusCode, detail)
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return fmt.Errorf("decode provider response: %w", err)
	}
	return nil
}

// ParseRepositoryURL accepts public HTTPS GitHub/GitLab repository URLs only.
func ParseRepositoryURL(rawURL string) (Repository, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return Repository{}, fmt.Errorf("invalid repository_url: %w", err)
	}
	if parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return Repository{}, fmt.Errorf("repository_url must be a public HTTPS GitHub or GitLab URL")
	}

	host := strings.ToLower(parsed.Hostname())
	projectPath := strings.Trim(parsed.EscapedPath(), "/")
	projectPath = strings.TrimSuffix(projectPath, ".git")
	decodedPath, err := url.PathUnescape(projectPath)
	if err != nil {
		return Repository{}, fmt.Errorf("invalid repository path: %w", err)
	}
	parts := strings.Split(decodedPath, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return Repository{}, fmt.Errorf("invalid repository path")
		}
	}

	switch host {
	case "github.com":
		if len(parts) != 2 {
			return Repository{}, fmt.Errorf("GitHub repository_url must contain owner and repository")
		}
		return Repository{Provider: "github", ProjectPath: strings.Join(parts, "/")}, nil
	case "gitlab.com":
		if len(parts) < 2 {
			return Repository{}, fmt.Errorf("GitLab repository_url must contain namespace and project")
		}
		return Repository{Provider: "gitlab", ProjectPath: strings.Join(parts, "/")}, nil
	default:
		return Repository{}, fmt.Errorf("unsupported Git provider: %s", host)
	}
}
