package reports

import "context"

type Service struct {
	repo *Repo
}

func NewService(repo *Repo) *Service {
	return &Service{repo: repo}
}

func (s *Service) Create(ctx context.Context, userID string, req CreateReportRequest, sourceKeyID *string) (*Report, error) {
	return s.repo.Create(ctx, userID, req, sourceKeyID)
}

func (s *Service) Get(ctx context.Context, userID, id string) (*Report, error) {
	return s.repo.Get(ctx, userID, id)
}

func (s *Service) Count(ctx context.Context, userID string, params ListReportsParams) (int, error) {
	return s.repo.Count(ctx, userID, params)
}

func (s *Service) List(ctx context.Context, userID string, params ListReportsParams) ([]Report, error) {
	return s.repo.List(ctx, userID, params)
}

func (s *Service) Update(ctx context.Context, userID, id string, req UpdateReportRequest) (*Report, error) {
	return s.repo.Update(ctx, userID, id, req)
}

func (s *Service) Delete(ctx context.Context, userID, id string) error {
	return s.repo.Delete(ctx, userID, id)
}

func (s *Service) ListSources(ctx context.Context, userID string) ([]SourceStat, error) {
	return s.repo.ListSources(ctx, userID)
}
