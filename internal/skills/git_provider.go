package skills

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

type gitRepository struct {
	Provider    string
	ProjectPath string
}

type resolvedGitRevision struct {
	Provider    string
	ProjectPath string
	Ref         string
	CommitSHA   string
	ArchiveURL  string
}

type gitProviderClient struct {
	httpClient       *http.Client
	githubAPIBaseURL string
	gitlabAPIBaseURL string
}

func newGitProviderClient(httpClient *http.Client) *gitProviderClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &gitProviderClient{
		httpClient:       httpClient,
		githubAPIBaseURL: "https://api.github.com",
		gitlabAPIBaseURL: "https://gitlab.com/api/v4",
	}
}

func (c *gitProviderClient) Resolve(ctx context.Context, repositoryURL, requestedRef, defaultRef string) (resolvedGitRevision, error) {
	repository, err := parseGitRepositoryURL(repositoryURL)
	if err != nil {
		return resolvedGitRevision{}, err
	}

	ref := strings.TrimSpace(requestedRef)
	if ref == "" {
		ref = strings.TrimSpace(defaultRef)
	}
	if ref == "" {
		ref, err = c.defaultBranch(ctx, repository)
		if err != nil {
			return resolvedGitRevision{}, err
		}
	}

	commitSHA, err := c.resolveCommit(ctx, repository, ref)
	if err != nil {
		return resolvedGitRevision{}, err
	}
	return resolvedGitRevision{
		Provider:    repository.Provider,
		ProjectPath: repository.ProjectPath,
		Ref:         ref,
		CommitSHA:   commitSHA,
		ArchiveURL:  c.archiveURL(repository, commitSHA),
	}, nil
}

func (c *gitProviderClient) defaultBranch(ctx context.Context, repository gitRepository) (string, error) {
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

func (c *gitProviderClient) resolveCommit(ctx context.Context, repository gitRepository, ref string) (string, error) {
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

func (c *gitProviderClient) archiveURL(repository gitRepository, commitSHA string) string {
	switch repository.Provider {
	case "github":
		return strings.TrimRight(c.githubAPIBaseURL, "/") + "/repos/" + repository.ProjectPath + "/tarball/" + url.PathEscape(commitSHA)
	case "gitlab":
		return strings.TrimRight(c.gitlabAPIBaseURL, "/") + "/projects/" + url.PathEscape(repository.ProjectPath) + "/repository/archive.tar.gz?sha=" + url.QueryEscape(commitSHA)
	default:
		return ""
	}
}

func (c *gitProviderClient) getJSON(ctx context.Context, endpoint string, destination any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "agentmate-skill-registry")

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

func parseGitRepositoryURL(rawURL string) (gitRepository, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return gitRepository{}, fmt.Errorf("invalid repository_url: %w", err)
	}
	if parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return gitRepository{}, fmt.Errorf("repository_url must be a public HTTPS GitHub or GitLab URL")
	}

	host := strings.ToLower(parsed.Hostname())
	projectPath := strings.Trim(parsed.EscapedPath(), "/")
	projectPath = strings.TrimSuffix(projectPath, ".git")
	decodedPath, err := url.PathUnescape(projectPath)
	if err != nil {
		return gitRepository{}, fmt.Errorf("invalid repository path: %w", err)
	}
	parts := strings.Split(decodedPath, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return gitRepository{}, fmt.Errorf("invalid repository path")
		}
	}

	switch host {
	case "github.com":
		if len(parts) != 2 {
			return gitRepository{}, fmt.Errorf("GitHub repository_url must contain owner and repository")
		}
		return gitRepository{Provider: "github", ProjectPath: strings.Join(parts, "/")}, nil
	case "gitlab.com":
		if len(parts) < 2 {
			return gitRepository{}, fmt.Errorf("GitLab repository_url must contain namespace and project")
		}
		return gitRepository{Provider: "gitlab", ProjectPath: strings.Join(parts, "/")}, nil
	default:
		return gitRepository{}, fmt.Errorf("unsupported Git provider: %s", host)
	}
}
