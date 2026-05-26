package skills

import "context"

type Service struct {
	repo *Repo
}

func NewService(repo *Repo) *Service {
	return &Service{repo: repo}
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
