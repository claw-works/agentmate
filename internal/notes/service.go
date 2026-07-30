package notes

import (
	"context"

	"github.com/claw-works/agentmate/internal/ownership"
)

type Service struct {
	repo *Repo
}

func NewService(repo *Repo) *Service {
	return &Service{repo: repo}
}

func (s *Service) Create(ctx context.Context, owner ownership.Owner, req CreateRequest) (*Note, error) {
	return s.repo.Create(ctx, owner, req)
}

func (s *Service) Get(ctx context.Context, accountID, id string) (*Note, error) {
	return s.repo.Get(ctx, accountID, id)
}

func (s *Service) Count(ctx context.Context, accountID string, params ListNotesParams) (int, error) {
	return s.repo.Count(ctx, accountID, params)
}

func (s *Service) List(ctx context.Context, accountID string, params ListNotesParams) ([]Note, error) {
	return s.repo.List(ctx, accountID, params)
}

func (s *Service) Update(ctx context.Context, accountID, id string, req UpdateRequest) (*Note, error) {
	return s.repo.Update(ctx, accountID, id, req)
}

func (s *Service) Append(ctx context.Context, id, accountID, content string) (*Note, error) {
	return s.repo.Append(ctx, id, accountID, content)
}

func (s *Service) Delete(ctx context.Context, accountID, id string) error {
	return s.repo.Delete(ctx, accountID, id)
}

func (s *Service) Search(ctx context.Context, accountID, query string) ([]Note, error) {
	return s.repo.Search(ctx, accountID, query)
}
