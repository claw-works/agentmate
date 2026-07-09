package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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

func (s *Service) CreateLog(ctx context.Context, userID string, req CreateLogRequest) (*SkillLog, error) {
	return s.repo.CreateLog(ctx, userID, req)
}

func (s *Service) ListLogs(ctx context.Context, userID string, params LogListParams) ([]SkillLog, error) {
	return s.repo.ListLogs(ctx, userID, params)
}

func (s *Service) CountLogs(ctx context.Context, userID string, params LogListParams) (int, error) {
	return s.repo.CountLogs(ctx, userID, params)
}

func (s *Service) CreateVersion(ctx context.Context, userID string, req CreateVersionRequest) (*SkillVersion, error) {
	return s.repo.CreateVersion(ctx, userID, req)
}

func (s *Service) ListVersions(ctx context.Context, userID string, params VersionListParams) ([]SkillVersion, error) {
	return s.repo.ListVersions(ctx, userID, params)
}

func (s *Service) GetActiveVersion(ctx context.Context, userID, skillName string) (*SkillVersion, error) {
	return s.repo.GetActiveVersion(ctx, userID, skillName)
}

func (s *Service) ActivateVersion(ctx context.Context, userID, id string) (*SkillVersion, error) {
	return s.repo.ActivateVersion(ctx, userID, id)
}

func (s *Service) GetSkillStats(ctx context.Context, userID, skillName string) (*SkillStats, error) {
	return s.repo.GetSkillStats(ctx, userID, skillName)
}

func (s *Service) SkillSignals(ctx context.Context, userID, skillName string, limit int) ([]SkillLog, error) {
	return s.repo.SkillSignals(ctx, userID, skillName, limit)
}

func (s *Service) IndexActiveVersions(ctx context.Context, userID, skillName string) (*IndexSkillsResponse, error) {
	if s.retrieval == nil {
		return nil, fmt.Errorf("retrieval service is not configured")
	}
	skillName = strings.TrimSpace(skillName)
	versions, err := s.repo.ListActiveVersions(ctx, userID, skillName)
	if err != nil {
		return nil, err
	}

	resp := &IndexSkillsResponse{
		Indexed: make([]IndexedSkill, 0, len(versions)),
		Errors:  make([]IndexError, 0),
	}
	for _, version := range versions {
		doc, err := s.retrieval.IndexDocument(ctx, userID, retrieval.UpsertDocumentInput{
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

func (s *Service) Search(ctx context.Context, userID string, req SearchSkillsRequest) (*SearchSkillsResponse, error) {
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
	results, err := s.retrieval.Search(ctx, userID, retrieval.SearchRequest{
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

func extractSkillDescription(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "---" {
			break
		}
		if strings.HasPrefix(line, "description:") {
			value := strings.TrimSpace(strings.TrimPrefix(line, "description:"))
			return strings.Trim(value, `"'`)
		}
	}
	return ""
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
