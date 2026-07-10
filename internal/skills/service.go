package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/wellxie/agentmate/internal/ownership"
	"github.com/wellxie/agentmate/internal/retrieval"
)

type Service struct {
	repo      *Repo
	retrieval *retrieval.Service
}

func NewService(repo *Repo, retrievalSvc ...*retrieval.Service) *Service {
	s := &Service{repo: repo}
	if len(retrievalSvc) > 0 {
		s.retrieval = retrievalSvc[0]
	}
	return s
}

func (s *Service) CreateLog(ctx context.Context, owner ownership.Owner, req CreateLogRequest) (*SkillLog, error) {
	return s.repo.CreateLog(ctx, owner, req)
}

func (s *Service) ListLogs(ctx context.Context, accountID string, params LogListParams) ([]SkillLog, error) {
	return s.repo.ListLogs(ctx, accountID, params)
}

func (s *Service) CountLogs(ctx context.Context, accountID string, params LogListParams) (int, error) {
	return s.repo.CountLogs(ctx, accountID, params)
}

func (s *Service) CreateVersion(ctx context.Context, owner ownership.Owner, req CreateVersionRequest) (*SkillVersion, error) {
	return s.repo.CreateVersion(ctx, owner, req)
}

func (s *Service) ListVersions(ctx context.Context, accountID string, params VersionListParams) ([]SkillVersion, error) {
	return s.repo.ListVersions(ctx, accountID, params)
}

func (s *Service) GetActiveVersion(ctx context.Context, accountID, skillName string) (*SkillVersion, error) {
	return s.repo.GetActiveVersion(ctx, accountID, skillName)
}

func (s *Service) ActivateVersion(ctx context.Context, accountID, id string) (*SkillVersion, error) {
	return s.repo.ActivateVersion(ctx, accountID, id)
}

func (s *Service) GetSkillStats(ctx context.Context, accountID, skillName string) (*SkillStats, error) {
	return s.repo.GetSkillStats(ctx, accountID, skillName)
}

func (s *Service) SkillSignals(ctx context.Context, accountID, skillName string, limit int) ([]SkillLog, error) {
	return s.repo.SkillSignals(ctx, accountID, skillName, limit)
}

func (s *Service) CreateSource(ctx context.Context, owner ownership.Owner, req CreateSkillSourceRequest) (*SkillSource, error) {
	normalized, err := normalizeSourceRequest(req)
	if err != nil {
		return nil, err
	}
	return s.repo.UpsertSource(ctx, owner, normalized)
}

func (s *Service) ListSources(ctx context.Context, accountID string, params SkillSourceListParams) ([]SkillSource, error) {
	params.Type = strings.TrimSpace(strings.ToLower(params.Type))
	params.Status = strings.TrimSpace(strings.ToLower(params.Status))
	return s.repo.ListSources(ctx, accountID, params)
}

func (s *Service) GetSource(ctx context.Context, accountID, id string) (*SkillSource, error) {
	return s.repo.GetSource(ctx, accountID, id)
}

func (s *Service) ListSourceRevisions(ctx context.Context, accountID, sourceID string, limit, offset int) ([]SkillSourceRevision, error) {
	if _, err := s.repo.GetSource(ctx, accountID, sourceID); err != nil {
		return nil, err
	}
	return s.repo.ListSourceRevisions(ctx, accountID, sourceID, limit, offset)
}

func (s *Service) ListVersionFiles(ctx context.Context, accountID, versionID string) ([]SkillVersionFile, error) {
	return s.repo.ListVersionFiles(ctx, accountID, versionID)
}

func (s *Service) SubmitLocalSnapshot(ctx context.Context, owner ownership.Owner, sourceID string, req SubmitLocalSnapshotRequest) (*SubmitLocalSnapshotResponse, error) {
	source, err := s.repo.GetSource(ctx, owner.Account(), sourceID)
	if err != nil {
		return nil, err
	}
	if source.Type != "local" {
		return nil, fmt.Errorf("snapshots are only supported for local sources")
	}

	files, skillContent, packageHash, treeHash, err := normalizeSnapshotFiles(req)
	if err != nil {
		return nil, err
	}
	meta := extractSkillMetadata(skillContent)
	skillName := strings.TrimSpace(req.SkillName)
	if skillName == "" {
		skillName = meta["name"]
	}
	if skillName == "" {
		skillName = source.Name
	}
	if skillName == "" {
		skillName = "local-skill"
	}
	version := strings.TrimSpace(req.Version)
	if version == "" {
		version = "snap-" + shortHash(packageHash)
	}
	if len(version) > 20 {
		return nil, fmt.Errorf("version must be 20 characters or fewer")
	}
	if len(skillName) > 100 {
		return nil, fmt.Errorf("skill_name must be 100 characters or fewer")
	}
	agentID := strings.TrimSpace(req.AgentID)
	if agentID == "" {
		agentID = "agentmate-skill-sync"
	}
	changeSummary := strings.TrimSpace(req.ChangeSummary)
	if changeSummary == "" {
		changeSummary = "Imported local skill snapshot " + shortHash(packageHash)
	}
	snapshotID := strings.TrimSpace(req.SnapshotID)
	if snapshotID == "" {
		snapshotID = packageHash
	}

	activate := boolDefault(req.Activate, true)
	versionReq := CreateVersionRequest{
		SkillName:     skillName,
		Version:       version,
		Content:       skillContent,
		AgentID:       agentID,
		ChangeSummary: changeSummary,
		Activate:      activate,
	}
	revisionIn := SkillSourceRevision{
		LocalSnapshotID: snapshotID,
		TreeHash:        treeHash,
		PackageHash:     packageHash,
	}
	revision, skillVersion, storedFiles, err := s.repo.IngestLocalSnapshot(ctx, owner, source, versionReq, revisionIn, files)
	if err != nil {
		return nil, err
	}

	resp := &SubmitLocalSnapshotResponse{
		Source:   source,
		Revision: revision,
		Version:  skillVersion,
		Files:    storedFiles,
	}
	if boolDefault(req.Index, true) {
		indexResp, err := s.IndexActiveVersions(ctx, owner, skillName)
		if err != nil {
			indexResp = &IndexSkillsResponse{
				Indexed: []IndexedSkill{},
				Errors:  []IndexError{{SkillName: skillName, Error: err.Error()}},
			}
		}
		resp.Index = indexResp
	}
	return resp, nil
}

func (s *Service) IndexActiveVersions(ctx context.Context, owner ownership.Owner, skillName string) (*IndexSkillsResponse, error) {
	if s.retrieval == nil {
		return nil, fmt.Errorf("retrieval service is not configured")
	}
	skillName = strings.TrimSpace(skillName)
	versions, err := s.repo.ListActiveVersions(ctx, owner.Account(), skillName)
	if err != nil {
		return nil, err
	}

	resp := &IndexSkillsResponse{
		Indexed: make([]IndexedSkill, 0, len(versions)),
		Errors:  make([]IndexError, 0),
	}
	for _, version := range versions {
		doc, err := s.retrieval.IndexDocument(ctx, owner, retrieval.UpsertDocumentInput{
			Namespace:  retrieval.NamespaceSkills,
			SourceType: "skill_version",
			SourceID:   version.SkillName,
			ChunkKey:   "active",
			Title:      version.SkillName,
			Content:    skillIndexContent(version),
			Metadata: map[string]any{
				"skill_name":     version.SkillName,
				"version":        version.Version,
				"version_id":     version.ID,
				"agent_id":       version.AgentID,
				"change_summary": version.ChangeSummary,
				"published_at":   version.PublishedAt.Format(time.RFC3339),
				"is_active":      version.IsActive,
				"description":    extractSkillDescription(version.Content),
			},
		})
		if err != nil {
			resp.Errors = append(resp.Errors, IndexError{SkillName: version.SkillName, Error: err.Error()})
			continue
		}
		resp.Indexed = append(resp.Indexed, IndexedSkill{
			SkillName:  version.SkillName,
			Version:    version.Version,
			VersionID:  version.ID,
			DocumentID: doc.ID,
		})
	}
	return resp, nil
}

func (s *Service) Search(ctx context.Context, owner ownership.Owner, req SearchSkillsRequest) (*SearchSkillsResponse, error) {
	if s.retrieval == nil {
		return nil, fmt.Errorf("retrieval service is not configured")
	}
	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		return nil, fmt.Errorf("query required")
	}
	topK := req.TopK
	if topK <= 0 || topK > 20 {
		topK = 5
	}
	results, err := s.retrieval.Search(ctx, owner, retrieval.SearchRequest{
		Namespace: retrieval.NamespaceSkills,
		Query:     req.Query,
		TopK:      topK,
		Filters: map[string]any{
			"source_type": "skill_version",
		},
		Metadata: map[string]any{
			"feature": "skill_search",
		},
	})
	if err != nil {
		return nil, err
	}

	items := make([]SkillSearchItem, 0, len(results))
	for _, result := range results {
		if result.Document == nil {
			continue
		}
		meta := documentMetadata(result.Document.Metadata)
		item := SkillSearchItem{
			SkillName:     stringMeta(meta, "skill_name", result.Document.SourceID),
			Version:       stringMeta(meta, "version", ""),
			VersionID:     stringMeta(meta, "version_id", ""),
			Title:         result.Document.Title,
			Description:   stringMeta(meta, "description", ""),
			Score:         result.Score,
			Rank:          result.Rank,
			DocumentID:    result.Document.ID,
			ChangeSummary: stringMeta(meta, "change_summary", ""),
		}
		if publishedAt := stringMeta(meta, "published_at", ""); publishedAt != "" {
			if t, err := time.Parse(time.RFC3339, publishedAt); err == nil {
				item.PublishedAt = t
			}
		}
		if req.IncludeContent {
			item.Content = result.Document.Content
		}
		items = append(items, item)
	}
	return &SearchSkillsResponse{Items: items, Total: len(items)}, nil
}

func skillIndexContent(version SkillVersion) string {
	description := extractSkillDescription(version.Content)
	parts := []string{
		"Skill: " + version.SkillName,
		"Version: " + version.Version,
	}
	if description != "" {
		parts = append(parts, "Description: "+description)
	}
	if version.ChangeSummary != "" {
		parts = append(parts, "Change summary: "+version.ChangeSummary)
	}
	parts = append(parts, "Instructions:\n"+trimForEmbedding(version.Content))
	return strings.Join(parts, "\n\n")
}

func normalizeSourceRequest(req CreateSkillSourceRequest) (CreateSkillSourceRequest, error) {
	req.Type = strings.TrimSpace(strings.ToLower(req.Type))
	req.RepositoryURL = strings.TrimSpace(req.RepositoryURL)
	req.PackagePath = normalizeOptionalRelativePath(req.PackagePath)
	req.DefaultRef = strings.TrimSpace(req.DefaultRef)
	req.SyncMode = strings.TrimSpace(strings.ToLower(req.SyncMode))
	req.Visibility = strings.TrimSpace(strings.ToLower(req.Visibility))
	req.Status = strings.TrimSpace(strings.ToLower(req.Status))
	req.Name = strings.TrimSpace(req.Name)

	if req.Type != "git" && req.Type != "local" {
		return req, fmt.Errorf("type must be git or local")
	}
	if req.RepositoryURL == "" {
		return req, fmt.Errorf("repository_url required")
	}
	if req.PackagePath == ".." || strings.HasPrefix(req.PackagePath, "../") || path.IsAbs(req.PackagePath) {
		return req, fmt.Errorf("package_path must be relative")
	}
	if req.Type == "git" {
		if req.SyncMode == "" {
			req.SyncMode = "server_pull"
		}
		if req.DefaultRef == "" {
			req.DefaultRef = "main"
		}
	} else if req.SyncMode == "" {
		req.SyncMode = "client_push"
	}
	if req.Type == "git" && req.SyncMode != "server_pull" {
		return req, fmt.Errorf("git sources must use server_pull sync_mode")
	}
	if req.Type == "local" && req.SyncMode != "client_push" {
		return req, fmt.Errorf("local sources must use client_push sync_mode")
	}
	if req.Visibility == "" {
		req.Visibility = "private"
	}
	if req.Visibility != "private" && req.Visibility != "shared" && req.Visibility != "public" {
		return req, fmt.Errorf("visibility must be private, shared, or public")
	}
	if req.Status == "" {
		req.Status = "active"
	}
	if req.Status != "active" && req.Status != "disabled" && req.Status != "error" {
		return req, fmt.Errorf("status must be active, disabled, or error")
	}
	if req.Name == "" {
		req.Name = inferSourceName(req)
	}
	return req, nil
}

func normalizeSnapshotFiles(req SubmitLocalSnapshotRequest) ([]SkillVersionFile, string, string, string, error) {
	const maxFiles = 300
	const maxSnapshotContentBytes = 8 * 1024 * 1024

	if len(req.Files) == 0 {
		return nil, "", "", "", fmt.Errorf("files required")
	}
	if len(req.Files) > maxFiles {
		return nil, "", "", "", fmt.Errorf("too many files: max %d", maxFiles)
	}

	seen := make(map[string]struct{}, len(req.Files))
	files := make([]SkillVersionFile, 0, len(req.Files))
	var skillContent string
	totalContentBytes := 0

	for _, input := range req.Files {
		normalizedPath, err := normalizeSnapshotPath(input.Path)
		if err != nil {
			return nil, "", "", "", err
		}
		if _, ok := seen[normalizedPath]; ok {
			return nil, "", "", "", fmt.Errorf("duplicate file path: %s", normalizedPath)
		}
		seen[normalizedPath] = struct{}{}

		content := input.Content
		contentBytes := []byte(content)
		totalContentBytes += len(contentBytes)
		if totalContentBytes > maxSnapshotContentBytes {
			return nil, "", "", "", fmt.Errorf("snapshot text content is too large")
		}

		sha := strings.TrimSpace(strings.ToLower(input.SHA256))
		if sha == "" && content != "" {
			sha = sha256HexString(content)
		}
		if sha == "" {
			return nil, "", "", "", fmt.Errorf("sha256 required for %s when content is omitted", normalizedPath)
		}
		if !isSHA256Hex(sha) {
			return nil, "", "", "", fmt.Errorf("invalid sha256 for %s", normalizedPath)
		}
		if content != "" && sha != sha256HexString(content) {
			return nil, "", "", "", fmt.Errorf("sha256 mismatch for %s", normalizedPath)
		}

		size := input.SizeBytes
		if size == 0 {
			size = input.Size
		}
		if size == 0 && content != "" {
			size = int64(len(contentBytes))
		}
		if size < 0 {
			return nil, "", "", "", fmt.Errorf("size_bytes must be non-negative for %s", normalizedPath)
		}

		mimeType := strings.TrimSpace(input.MimeType)
		if mimeType == "" {
			mimeType = inferMimeType(normalizedPath)
		}
		kind := strings.TrimSpace(input.Kind)
		if kind == "" {
			kind = inferFileKind(normalizedPath)
		}
		indexable := input.Indexable || (content != "" && isIndexableText(normalizedPath, mimeType))
		contentSnapshot := ""
		if indexable {
			contentSnapshot = content
		}
		if normalizedPath == "SKILL.md" {
			if content == "" {
				return nil, "", "", "", fmt.Errorf("SKILL.md content required")
			}
			indexable = true
			contentSnapshot = content
			skillContent = content
		}
		files = append(files, SkillVersionFile{
			Path:            normalizedPath,
			Kind:            kind,
			SHA256:          sha,
			SizeBytes:       size,
			MimeType:        mimeType,
			Indexable:       indexable,
			ContentSnapshot: contentSnapshot,
		})
	}
	if skillContent == "" {
		return nil, "", "", "", fmt.Errorf("root SKILL.md required")
	}

	packageHash := strings.TrimSpace(strings.ToLower(req.PackageHash))
	if packageHash == "" {
		packageHash = computePackageHash(files)
	} else if !isSHA256Hex(packageHash) {
		return nil, "", "", "", fmt.Errorf("invalid package_hash")
	}
	treeHash := strings.TrimSpace(strings.ToLower(req.TreeHash))
	if treeHash == "" {
		treeHash = packageHash
	} else if !isSHA256Hex(treeHash) {
		return nil, "", "", "", fmt.Errorf("invalid tree_hash")
	}
	return files, skillContent, packageHash, treeHash, nil
}

func extractSkillDescription(content string) string {
	return extractSkillMetadata(content)["description"]
}

func extractSkillMetadata(content string) map[string]string {
	values := map[string]string{}
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return values
	}
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "---" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.ToLower(key))
		if key != "name" && key != "description" {
			continue
		}
		values[key] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return values
}

func trimForEmbedding(content string) string {
	const maxRunes = 20000
	runes := []rune(content)
	if len(runes) <= maxRunes {
		return content
	}
	return string(runes[:maxRunes])
}

func documentMetadata(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var metadata map[string]any
	if err := json.Unmarshal(raw, &metadata); err != nil || metadata == nil {
		return map[string]any{}
	}
	return metadata
}

func stringMeta(metadata map[string]any, key, fallback string) string {
	value, ok := metadata[key]
	if !ok || value == nil {
		return fallback
	}
	if s, ok := value.(string); ok {
		return s
	}
	return fallback
}

func normalizeOptionalRelativePath(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" {
		return ""
	}
	cleaned := path.Clean(value)
	if cleaned == "." {
		return ""
	}
	return cleaned
}

func normalizeSnapshotPath(value string) (string, error) {
	cleaned := normalizeOptionalRelativePath(value)
	if cleaned == "" {
		return "", fmt.Errorf("file path required")
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || path.IsAbs(cleaned) {
		return "", fmt.Errorf("file path must be relative: %s", value)
	}
	return cleaned, nil
}

func inferSourceName(req CreateSkillSourceRequest) string {
	if req.PackagePath != "" {
		return path.Base(req.PackagePath)
	}
	repository := strings.TrimSuffix(strings.TrimRight(req.RepositoryURL, "/"), ".git")
	if repository != "" {
		base := path.Base(repository)
		if base != "." && base != "/" {
			return base
		}
	}
	return req.Type + "-skill-source"
}

func inferFileKind(filePath string) string {
	base := strings.ToLower(path.Base(filePath))
	ext := strings.ToLower(path.Ext(filePath))
	switch {
	case base == "skill.md":
		return "instruction"
	case ext == ".md" || ext == ".mdx" || ext == ".txt":
		return "document"
	case ext == ".go" || ext == ".py" || ext == ".js" || ext == ".ts" || ext == ".tsx" || ext == ".rs" || ext == ".sh":
		return "code"
	default:
		return "file"
	}
}

func inferMimeType(filePath string) string {
	switch strings.ToLower(path.Ext(filePath)) {
	case ".md", ".mdx":
		return "text/markdown"
	case ".txt":
		return "text/plain"
	case ".json":
		return "application/json"
	case ".yaml", ".yml":
		return "application/yaml"
	case ".go", ".py", ".js", ".ts", ".tsx", ".rs", ".sh":
		return "text/plain"
	default:
		return ""
	}
}

func isIndexableText(filePath, mimeType string) bool {
	if strings.HasPrefix(strings.ToLower(mimeType), "text/") {
		return true
	}
	switch strings.ToLower(path.Ext(filePath)) {
	case ".md", ".mdx", ".txt", ".json", ".yaml", ".yml", ".go", ".py", ".js", ".ts", ".tsx", ".rs", ".sh":
		return true
	default:
		return false
	}
}

func computePackageHash(files []SkillVersionFile) string {
	lines := make([]string, 0, len(files))
	for _, file := range files {
		lines = append(lines, fmt.Sprintf("%s\x00%s\x00%d", file.Path, file.SHA256, file.SizeBytes))
	}
	sort.Strings(lines)
	return sha256HexString(strings.Join(lines, "\n"))
}

func sha256HexString(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func isSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func shortHash(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}

func boolDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}
