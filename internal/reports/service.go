package reports

import (
	"context"

	"github.com/wellxie/agentmate/internal/ownership"
)

type Service struct {
	repo *Repo
}

func NewService(repo *Repo) *Service {
	return &Service{repo: repo}
}

func (s *Service) Create(ctx context.Context, owner ownership.Owner, req CreateReportRequest) (*Report, error) {
	return s.repo.Create(ctx, owner, req)
}

func (s *Service) Get(ctx context.Context, accountID, id string) (*Report, error) {
	return s.repo.Get(ctx, accountID, id)
}

func (s *Service) PublicGet(ctx context.Context, id string) (*PublicReport, error) {
	return s.repo.PublicGet(ctx, id)
}

func (s *Service) Count(ctx context.Context, accountID string, params ListReportsParams) (int, error) {
	return s.repo.Count(ctx, accountID, params)
}

func (s *Service) PublicCount(ctx context.Context, params ListReportsParams) (int, error) {
	return s.repo.PublicCount(ctx, params)
}

func (s *Service) List(ctx context.Context, accountID string, params ListReportsParams) ([]Report, error) {
	return s.repo.List(ctx, accountID, params)
}

func (s *Service) PublicList(ctx context.Context, params ListReportsParams) ([]PublicReport, error) {
	return s.repo.PublicList(ctx, params)
}

func (s *Service) Update(ctx context.Context, accountID, id string, req UpdateReportRequest) (*Report, error) {
	return s.repo.Update(ctx, accountID, id, req)
}

func (s *Service) Delete(ctx context.Context, accountID, id string) error {
	return s.repo.Delete(ctx, accountID, id)
}

func (s *Service) ListSources(ctx context.Context, accountID string) ([]SourceStat, error) {
	return s.repo.ListSources(ctx, accountID)
}

func (s *Service) PublicListSources(ctx context.Context) ([]SourceStat, error) {
	return s.repo.PublicListSources(ctx)
}
