package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const maxCatalogQueryRunes = 500

func (s *Service) Compile(ctx context.Context, accountID, versionID string) (*CompileSkillsResponse, error) {
	versionID = strings.TrimSpace(versionID)
	if versionID != "" {
		artifact, err := s.compileVersion(ctx, accountID, versionID)
		if err != nil {
			return nil, err
		}
		return &CompileSkillsResponse{
			Items:  []SkillCatalogItem{catalogItemFromArtifact(*artifact, true)},
			Errors: []IndexError{},
		}, nil
	}

	versions, err := s.repo.ListActiveVersions(ctx, accountID, "")
	if err != nil {
		return nil, err
	}
	response := &CompileSkillsResponse{
		Items:  make([]SkillCatalogItem, 0, len(versions)),
		Errors: make([]IndexError, 0),
	}
	for _, version := range versions {
		artifact, compileErr := s.compileLoadedVersion(ctx, version)
		if compileErr != nil {
			response.Errors = append(response.Errors, IndexError{SkillName: version.SkillName, Error: compileErr.Error()})
			continue
		}
		response.Items = append(response.Items, catalogItemFromArtifact(*artifact, true))
	}
	return response, nil
}

func (s *Service) compileVersion(ctx context.Context, accountID, versionID string) (*CompiledSkillCatalog, error) {
	version, err := s.repo.GetVersion(ctx, accountID, versionID)
	if err != nil {
		return nil, err
	}
	return s.compileLoadedVersion(ctx, *version)
}

func (s *Service) compileLoadedVersion(ctx context.Context, version SkillVersion) (*CompiledSkillCatalog, error) {
	accountID := valueOrEmpty(version.AccountID)
	if accountID == "" {
		return nil, fmt.Errorf("compiled catalog requires an account-owned skill version")
	}
	files, err := s.repo.ListVersionFiles(ctx, accountID, version.ID)
	if err != nil {
		return nil, err
	}
	artifact, err := CompileSkillVersion(version, files, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	return s.repo.UpsertCompiledCatalog(ctx, artifact)
}

func (s *Service) bestEffortCompile(ctx context.Context, accountID, versionID string) {
	_, _ = s.compileVersion(ctx, accountID, versionID)
}

func (s *Service) ListCatalog(ctx context.Context, accountID string, params SkillCatalogListParams) (*SkillCatalogListResponse, error) {
	if params.Limit == 0 {
		params.Limit = 20
	}
	if params.Limit < 1 || params.Limit > 100 {
		return nil, fmt.Errorf("limit must be between 1 and 100")
	}
	if params.Offset < 0 {
		return nil, fmt.Errorf("offset must be non-negative")
	}
	params.Query = strings.TrimSpace(params.Query)
	if err := validateCatalogQuery(params.Query); err != nil {
		return nil, err
	}

	records, err := s.repo.ListActiveCatalog(ctx, accountID, params)
	if err != nil {
		return nil, err
	}
	total, err := s.repo.CountActiveCatalog(ctx, accountID, params.Query)
	if err != nil {
		return nil, err
	}
	items := make([]SkillCatalogItem, 0, len(records))
	for _, record := range records {
		if record.Artifact != nil {
			items = append(items, catalogItemFromArtifact(*record.Artifact, true))
			continue
		}
		items = append(items, basicCatalogItem(record.Version))
	}
	return &SkillCatalogListResponse{
		Items:  items,
		Total:  total,
		Limit:  params.Limit,
		Offset: params.Offset,
	}, nil
}

func (s *Service) GetInstructions(ctx context.Context, accountID, versionID string) (*SkillInstructionsResponse, error) {
	version, err := s.repo.GetVersion(ctx, accountID, versionID)
	if err != nil {
		return nil, err
	}
	return &SkillInstructionsResponse{
		VersionID:    version.ID,
		SkillName:    version.SkillName,
		Version:      version.Version,
		Instructions: version.Content,
		ContentHash:  version.ContentHash,
		PublishedAt:  version.PublishedAt,
	}, nil
}

func (s *Service) GetResources(ctx context.Context, accountID, versionID string, params SkillResourceListParams) (*SkillResourcesResponse, error) {
	if params.Limit == 0 {
		params.Limit = 20
	}
	if params.Limit < 1 || params.Limit > 100 {
		return nil, fmt.Errorf("limit must be between 1 and 100")
	}
	if params.Offset < 0 {
		return nil, fmt.Errorf("offset must be non-negative")
	}
	version, err := s.repo.GetVersion(ctx, accountID, versionID)
	if err != nil {
		return nil, err
	}
	artifact, err := s.repo.GetCompiledCatalog(ctx, accountID, versionID)
	if err == nil {
		sortResourceManifest(artifact.ResourceManifest)
		return &SkillResourcesResponse{
			VersionID: version.ID,
			SkillName: version.SkillName,
			Version:   version.Version,
			Items:     paginateResourceManifest(artifact.ResourceManifest, params),
			Total:     len(artifact.ResourceManifest),
			Limit:     params.Limit,
			Offset:    params.Offset,
		}, nil
	}
	if !isNotFound(err) {
		return nil, err
	}

	files, err := s.repo.ListVersionFiles(ctx, accountID, versionID)
	if err != nil {
		return nil, err
	}
	if err := validateCompiledFiles(files); err != nil {
		return nil, err
	}
	manifest := resourceManifestFromFiles(files)
	return &SkillResourcesResponse{
		VersionID: version.ID,
		SkillName: version.SkillName,
		Version:   version.Version,
		Items:     paginateResourceManifest(manifest, params),
		Total:     len(manifest),
		Limit:     params.Limit,
		Offset:    params.Offset,
	}, nil
}

func (s *Service) GetResource(ctx context.Context, accountID, versionID, fileID string) (*SkillResourceResponse, error) {
	file, err := s.repo.GetTextResource(ctx, accountID, versionID, fileID)
	if err != nil {
		return nil, err
	}
	if !isIndexableText(file.Path, file.MimeType) {
		return nil, fmt.Errorf("resource is not text")
	}
	return &SkillResourceResponse{
		VersionID: versionID,
		FileID:    file.ID,
		Path:      file.Path,
		Kind:      file.Kind,
		SHA256:    file.SHA256,
		SizeBytes: file.SizeBytes,
		MimeType:  file.MimeType,
		Content:   file.ContentSnapshot,
	}, nil
}

func catalogItemFromArtifact(artifact CompiledSkillCatalog, available bool) SkillCatalogItem {
	return SkillCatalogItem{
		SkillVersionID:    artifact.SkillVersionID,
		SkillName:         artifact.SkillName,
		Version:           artifact.Version,
		SourceID:          artifact.SourceID,
		Description:       artifact.Description,
		Triggers:          nonNilStrings(artifact.Triggers),
		Capabilities:      nonNilStrings(artifact.Capabilities),
		Constraints:       nonNilStrings(artifact.Constraints),
		Dependencies:      nonNilStrings(artifact.Dependencies),
		CompilerName:      artifact.CompilerName,
		CompilerVersion:   artifact.CompilerVersion,
		PackageHash:       artifact.InputPackageHash,
		ResourceCount:     len(artifact.ResourceManifest),
		ResourceKinds:     resourceKinds(artifact.ResourceManifest),
		CompiledAt:        artifact.CompiledAt,
		PublishedAt:       artifact.PublishedAt,
		ArtifactAvailable: available,
	}
}

func basicCatalogItem(version SkillVersion) SkillCatalogItem {
	return SkillCatalogItem{
		SkillVersionID:    version.ID,
		SkillName:         version.SkillName,
		Version:           version.Version,
		SourceID:          version.SourceID,
		Description:       truncateRunes(extractSkillDescription(version.Content), maxDescriptionRunes),
		Triggers:          []string{},
		Capabilities:      []string{},
		Constraints:       []string{},
		Dependencies:      []string{},
		PackageHash:       version.PackageHash,
		ResourceKinds:     []string{},
		PublishedAt:       version.PublishedAt,
		ArtifactAvailable: false,
	}
}

func resourceManifestFromFiles(files []SkillVersionFile) []SkillResourceManifestItem {
	manifest := make([]SkillResourceManifestItem, 0, len(files))
	for _, file := range files {
		if file.Path == "SKILL.md" {
			continue
		}
		manifest = append(manifest, SkillResourceManifestItem{
			FileID:        file.ID,
			Path:          file.Path,
			Kind:          file.Kind,
			SHA256:        file.SHA256,
			SizeBytes:     file.SizeBytes,
			MimeType:      file.MimeType,
			Indexable:     file.Indexable,
			TextAvailable: file.Indexable && file.ContentSnapshot != "" && isIndexableText(file.Path, file.MimeType),
		})
	}
	sortResourceManifest(manifest)
	return manifest
}

func sortResourceManifest(manifest []SkillResourceManifestItem) {
	sort.Slice(manifest, func(i, j int) bool {
		if manifest[i].Path == manifest[j].Path {
			return manifest[i].FileID < manifest[j].FileID
		}
		return manifest[i].Path < manifest[j].Path
	})
}

func resourceKinds(manifest []SkillResourceManifestItem) []string {
	unique := make(map[string]struct{})
	for _, resource := range manifest {
		kind := strings.TrimSpace(resource.Kind)
		if kind != "" {
			unique[kind] = struct{}{}
		}
	}
	kinds := make([]string, 0, len(unique))
	for kind := range unique {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

func paginateResourceManifest(manifest []SkillResourceManifestItem, params SkillResourceListParams) []SkillResourceManifestItem {
	if params.Offset >= len(manifest) {
		return []SkillResourceManifestItem{}
	}
	end := params.Offset + params.Limit
	if end > len(manifest) {
		end = len(manifest)
	}
	return manifest[params.Offset:end]
}

func validateCatalogQuery(query string) error {
	if utf8.RuneCountInString(strings.TrimSpace(query)) > maxCatalogQueryRunes {
		return fmt.Errorf("query must be at most %d Unicode code points", maxCatalogQueryRunes)
	}
	return nil
}
