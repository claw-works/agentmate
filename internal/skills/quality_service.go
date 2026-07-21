package skills

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func (s *Service) RunQuality(ctx context.Context, accountID, versionID string, request CreateQualityRunRequest) (*QualityRun, error) {
	versionID = strings.TrimSpace(versionID)
	if versionID == "" {
		return nil, fmt.Errorf("version_id required")
	}
	baselineID := strings.TrimSpace(request.BaselineVersionID)
	input, err := s.repo.LoadQualityEngineInput(ctx, accountID, versionID, baselineID)
	if err != nil {
		return nil, err
	}
	version := input.Package.Version
	if input.Baseline != nil {
		baselineVersion := input.Baseline.Version
		if baselineVersion.ID == version.ID {
			return nil, fmt.Errorf("baseline_version_id must differ from version_id")
		}
		if baselineVersion.SkillName != version.SkillName {
			return nil, fmt.Errorf("baseline version must belong to the same skill")
		}
	}

	report := EvaluateSkillQuality(input)
	completedAt := time.Now().UTC()
	run := QualityRun{
		AccountID:        accountID,
		SkillVersionID:   version.ID,
		EngineVersion:    QualityEngineVersion,
		ChecksetVersion:  QualityChecksetVersion,
		InputPackageHash: version.PackageHash,
		TelemetryCutoff:  input.TelemetryCutoff,
		Status:           "completed",
		Report:           report,
		CompletedAt:      &completedAt,
	}
	if input.Baseline != nil {
		run.BaselineVersionID = &input.Baseline.Version.ID
		run.BaselinePackageHash = &input.Baseline.Version.PackageHash
	}
	return s.repo.CreateQualityRun(ctx, run)
}

func (s *Service) GetQualityRun(ctx context.Context, accountID, runID string) (*QualityRun, error) {
	return s.repo.GetQualityRun(ctx, accountID, strings.TrimSpace(runID))
}

func (s *Service) ListQualityRuns(ctx context.Context, accountID, versionID string, params QualityRunListParams) (*QualityRunListResponse, error) {
	if params.Limit == 0 {
		params.Limit = 20
	}
	if params.Limit < 1 || params.Limit > 100 {
		return nil, fmt.Errorf("limit must be between 1 and 100")
	}
	if params.Offset < 0 {
		return nil, fmt.Errorf("offset must be non-negative")
	}
	if _, err := s.repo.GetVersion(ctx, accountID, versionID); err != nil {
		return nil, err
	}
	items, err := s.repo.ListQualityRuns(ctx, accountID, versionID, params)
	if err != nil {
		return nil, err
	}
	total, err := s.repo.CountQualityRuns(ctx, accountID, versionID)
	if err != nil {
		return nil, err
	}
	return &QualityRunListResponse{Items: items, Total: total, Limit: params.Limit, Offset: params.Offset}, nil
}
