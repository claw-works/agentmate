package bookmarks

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

func (s *Service) Create(ctx context.Context, owner ownership.Owner, req CreateRequest) (*Bookmark, error) {
	return s.repo.Create(ctx, owner, req)
}

func (s *Service) Get(ctx context.Context, accountID, id string) (*Bookmark, error) {
	return s.repo.Get(ctx, accountID, id)
}

func (s *Service) Count(ctx context.Context, accountID string, params ListParams) (int, error) {
	return s.repo.Count(ctx, accountID, params)
}

func (s *Service) List(ctx context.Context, accountID string, params ListParams) ([]Bookmark, error) {
	return s.repo.List(ctx, accountID, params)
}

func (s *Service) Update(ctx context.Context, accountID, id string, req UpdateRequest) (*Bookmark, error) {
	return s.repo.Update(ctx, accountID, id, req)
}

func (s *Service) Delete(ctx context.Context, accountID, id string) error {
	return s.repo.Delete(ctx, accountID, id)
}
