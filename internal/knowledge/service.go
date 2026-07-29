package knowledge

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
	"unicode/utf8"

	"github.com/wellxie/agentmate/internal/gitfetch"
	"github.com/wellxie/agentmate/internal/llm"
	"github.com/wellxie/agentmate/internal/ownership"
	"github.com/wellxie/agentmate/internal/pkgpath"
	"github.com/wellxie/agentmate/internal/retrieval"
)

const (
	maxSnapshotFiles        = 500
	maxSnapshotContentBytes = 16 * 1024 * 1024
	maxSourceNameRunes      = 160
	// maxDocumentPathRunes mirrors the skills resource path bound.
	maxDocumentPathRunes = 1024
)

type Service struct {
	repo      *Repo
	retrieval *retrieval.Service
	gitClient *gitfetch.Client

	// compiler writes wiki pages; reviewer judges their faithfulness. Both are
	// optional: every K1/K2 path works without a model, and Compile reports
	// ErrCompilerUnavailable rather than failing a build when none is configured.
	compiler             llm.Client
	reviewer             llm.Client
	reviewerIndependence string
}

// NewService keeps the retrieval dependency optional (mirroring the skills
// constructor): catalog/index/search require it, K1 ingest paths do not.
func NewService(repo *Repo, retrievalSvc ...*retrieval.Service) *Service {
	s := &Service{
		repo:      repo,
		gitClient: gitfetch.NewClient(nil),
		// Absent configuration, independence is "unavailable" rather than empty,
		// so a build never records a blank claim about how it was reviewed.
		reviewerIndependence: llm.IndependenceUnavailable,
	}
	if len(retrievalSvc) > 0 {
		s.retrieval = retrievalSvc[0]
	}
	return s
}

// WithLLM attaches the two model roles. It is a separate setter rather than
// constructor arguments so that the many existing call sites — tests included —
// keep compiling and keep meaning "no compiler configured".
func (s *Service) WithLLM(compiler, reviewer llm.Client, independence string) *Service {
	s.compiler = compiler
	s.reviewer = reviewer
	if independence == "" {
		independence = llm.IndependenceUnavailable
	}
	s.reviewerIndependence = independence
	return s
}

// ─── Sources ───

func (s *Service) CreateSource(ctx context.Context, owner ownership.Owner, req CreateKnowledgeSourceRequest) (*KnowledgeSource, error) {
	normalized, err := normalizeSourceRequest(req)
	if err != nil {
		return nil, err
	}
	return s.repo.UpsertSource(ctx, owner, normalized)
}

func (s *Service) ListSources(ctx context.Context, accountID string, params KnowledgeSourceListParams) ([]KnowledgeSource, error) {
	params.Type = strings.TrimSpace(strings.ToLower(params.Type))
	params.Status = strings.TrimSpace(strings.ToLower(params.Status))
	return s.repo.ListSources(ctx, accountID, params)
}

func (s *Service) GetSource(ctx context.Context, accountID, id string) (*KnowledgeSource, error) {
	return s.repo.GetSource(ctx, accountID, id)
}

func (s *Service) ListSourceRevisions(ctx context.Context, accountID, sourceID string, limit, offset int) ([]KnowledgeSourceRevision, error) {
	if _, err := s.repo.GetSource(ctx, accountID, sourceID); err != nil {
		return nil, err
	}
	return s.repo.ListSourceRevisions(ctx, accountID, sourceID, limit, offset)
}

// ─── Local snapshot ingest ───

func (s *Service) SubmitSnapshot(ctx context.Context, owner ownership.Owner, sourceID string, req SubmitSnapshotRequest) (*SubmitSnapshotResponse, error) {
	source, err := s.repo.GetSource(ctx, owner.Account(), sourceID)
	if err != nil {
		return nil, err
	}
	if source.Type != "local" {
		return nil, fmt.Errorf("snapshots are only supported for local sources")
	}
	if source.Status == "disabled" {
		return nil, fmt.Errorf("source is disabled")
	}

	manifest, documents, packageHash, err := normalizeSnapshotFiles(req)
	if err != nil {
		return nil, s.recordIngestFailure(ctx, owner.Account(), source.ID, GitSourceSyncState{}, err)
	}

	snapshotID := strings.TrimSpace(req.SnapshotID)
	if snapshotID == "" {
		snapshotID = packageHash
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	revisionIn := KnowledgeSourceRevision{
		RevisionKey:     packageRevisionKey(packageHash),
		LocalSnapshotID: snapshotID,
		TreeHash:        packageHash,
		PackageHash:     packageHash,
		Manifest:        manifestJSON,
	}
	revision, summaries, err := s.repo.IngestRevision(ctx, owner, source, revisionIn, documents, deriveDocumentLinks(documents), nil)
	if err != nil {
		return nil, s.recordIngestFailure(ctx, owner.Account(), source.ID, GitSourceSyncState{PackageHash: packageHash}, err)
	}
	return &SubmitSnapshotResponse{
		Source:    source,
		Revision:  revision,
		Manifest:  manifest,
		Documents: summaries,
	}, nil
}

// ─── Git ingest ───

func (s *Service) SyncGitSource(ctx context.Context, owner ownership.Owner, sourceID string, req SyncGitSourceRequest) (*SyncGitSourceResponse, error) {
	source, err := s.repo.GetSource(ctx, owner.Account(), sourceID)
	if err != nil {
		return nil, err
	}
	if source.Type != "git" {
		return nil, fmt.Errorf("sync is only supported for git sources")
	}
	if source.Status == "disabled" {
		return nil, fmt.Errorf("source is disabled")
	}

	state := GitSourceSyncState{}
	resolved, err := s.gitClient.Resolve(ctx, source.RepositoryURL, req.Ref, source.DefaultRef)
	if err != nil {
		return nil, s.recordIngestFailure(ctx, owner.Account(), source.ID, state, err)
	}
	state.Provider = resolved.Provider
	state.Ref = resolved.Ref
	state.CommitSHA = resolved.CommitSHA

	packageFiles, err := s.gitClient.FetchPackage(ctx, resolved, source.PackagePath, gitfetch.DefaultArchiveLimits())
	if err != nil {
		return nil, s.recordIngestFailure(ctx, owner.Account(), source.ID, state, err)
	}
	manifest, documents, packageHash, err := normalizeGitPackageFiles(packageFiles)
	if err != nil {
		return nil, s.recordIngestFailure(ctx, owner.Account(), source.ID, state, err)
	}
	state.PackageHash = packageHash
	state.Status = "succeeded"
	state.SyncedAt = time.Now().UTC()

	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	revisionIn := KnowledgeSourceRevision{
		RevisionKey: packageRevisionKey(packageHash),
		CommitSHA:   resolved.CommitSHA,
		TreeHash:    packageHash,
		PackageHash: packageHash,
		Manifest:    manifestJSON,
	}
	revision, summaries, err := s.repo.IngestRevision(ctx, owner, source, revisionIn, documents, deriveDocumentLinks(documents), &state)
	if err != nil {
		return nil, s.recordIngestFailure(ctx, owner.Account(), source.ID, state, err)
	}
	return &SyncGitSourceResponse{
		Source:    source,
		Provider:  resolved.Provider,
		Ref:       resolved.Ref,
		CommitSHA: resolved.CommitSHA,
		Revision:  revision,
		Manifest:  manifest,
		Documents: summaries,
	}, nil
}

func (s *Service) recordIngestFailure(ctx context.Context, accountID, sourceID string, state GitSourceSyncState, ingestErr error) error {
	state.Status = "failed"
	state.Error = ingestErr.Error()
	state.SyncedAt = time.Now().UTC()
	if _, err := s.repo.RecordSourceError(ctx, accountID, sourceID, state); err != nil {
		return fmt.Errorf("%w; record ingest failure: %v", ingestErr, err)
	}
	return ingestErr
}

// ─── Documents ───

func (s *Service) ListRevisionDocuments(ctx context.Context, accountID, revisionID string, params DocumentListParams) (*DocumentListResponse, error) {
	revision, err := s.repo.GetRevision(ctx, accountID, revisionID)
	if err != nil {
		return nil, err
	}
	total, err := s.repo.CountRevisionDocuments(ctx, accountID, revision.ID)
	if err != nil {
		return nil, err
	}
	items, err := s.repo.ListRevisionDocuments(ctx, accountID, revision.ID, params)
	if err != nil {
		return nil, err
	}
	return &DocumentListResponse{
		RevisionID: revision.ID,
		Items:      items,
		Total:      total,
		Limit:      params.Limit,
		Offset:     params.Offset,
	}, nil
}

func (s *Service) GetDocument(ctx context.Context, accountID, revisionID, documentID string) (*KnowledgeDocument, error) {
	return s.repo.GetDocument(ctx, accountID, revisionID, documentID)
}

// ─── normalization ───

func normalizeSourceRequest(req CreateKnowledgeSourceRequest) (CreateKnowledgeSourceRequest, error) {
	req.Type = strings.TrimSpace(strings.ToLower(req.Type))
	req.RepositoryURL = strings.TrimSpace(req.RepositoryURL)
	req.PackagePath = gitfetch.NormalizeRelativePath(req.PackagePath)
	req.DefaultRef = strings.TrimSpace(req.DefaultRef)
	req.SyncMode = strings.TrimSpace(strings.ToLower(req.SyncMode))
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
		if _, err := gitfetch.ParseRepositoryURL(req.RepositoryURL); err != nil {
			return req, err
		}
		if req.SyncMode == "" {
			req.SyncMode = "server_pull"
		}
		if req.SyncMode != "server_pull" {
			return req, fmt.Errorf("git sources must use server_pull sync_mode")
		}
	} else {
		if req.SyncMode == "" {
			req.SyncMode = "client_push"
		}
		if req.SyncMode != "client_push" {
			return req, fmt.Errorf("local sources must use client_push sync_mode")
		}
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
	// Derived unconditionally: the package location is the only authority on
	// which domain owns the source.
	req.Domain = pkgpath.Domain(req.PackagePath)
	if utf8.RuneCountInString(req.Name) > maxSourceNameRunes {
		return req, fmt.Errorf("name must be %d characters or fewer", maxSourceNameRunes)
	}
	return req, nil
}

// normalizeSnapshotFiles validates a client-pushed snapshot with strict
// derivable-hash semantics (mirroring the skills local snapshot rules),
// parses the root KNOWLEDGE.yaml, applies include/exclude selection, and
// computes the canonical package hash.
func normalizeSnapshotFiles(req SubmitSnapshotRequest) (Manifest, []KnowledgeDocument, string, error) {
	if len(req.Files) == 0 {
		return Manifest{}, nil, "", fmt.Errorf("files required")
	}
	if len(req.Files) > maxSnapshotFiles {
		return Manifest{}, nil, "", fmt.Errorf("too many files: max %d", maxSnapshotFiles)
	}

	seen := make(map[string]struct{}, len(req.Files))
	files := make([]KnowledgeDocument, 0, len(req.Files))
	manifestContent := ""
	totalContentBytes := 0

	for _, input := range req.Files {
		normalizedPath, err := normalizeSnapshotPath(input.Path)
		if err != nil {
			return Manifest{}, nil, "", err
		}
		if _, ok := seen[normalizedPath]; ok {
			return Manifest{}, nil, "", fmt.Errorf("duplicate file path: %s", normalizedPath)
		}
		seen[normalizedPath] = struct{}{}

		content := input.Content
		totalContentBytes += len(content)
		if totalContentBytes > maxSnapshotContentBytes {
			return Manifest{}, nil, "", fmt.Errorf("snapshot text content is too large")
		}

		sha := strings.TrimSpace(strings.ToLower(input.SHA256))
		if sha == "" && content != "" {
			sha = sha256HexString(content)
		}
		if sha == "" {
			return Manifest{}, nil, "", fmt.Errorf("sha256 required for %s when content is omitted", normalizedPath)
		}
		if !isSHA256Hex(sha) {
			return Manifest{}, nil, "", fmt.Errorf("invalid sha256 for %s", normalizedPath)
		}
		if content != "" && sha != sha256HexString(content) {
			return Manifest{}, nil, "", fmt.Errorf("sha256 mismatch for %s", normalizedPath)
		}

		size := input.SizeBytes
		if size < 0 {
			return Manifest{}, nil, "", fmt.Errorf("size_bytes must be non-negative for %s", normalizedPath)
		}
		if content != "" {
			actualSize := int64(len(content))
			if size != 0 && size != actualSize {
				return Manifest{}, nil, "", fmt.Errorf("size_bytes mismatch for %s", normalizedPath)
			}
			size = actualSize
		}

		mimeType := strings.TrimSpace(input.MimeType)
		if mimeType == "" {
			mimeType = inferMimeType(normalizedPath)
		}
		isText := content != "" && isIndexableText(normalizedPath, mimeType)
		snapshot := ""
		if isText {
			snapshot = content
		}
		if normalizedPath == ManifestFileName {
			if content == "" {
				return Manifest{}, nil, "", fmt.Errorf("%s content required", ManifestFileName)
			}
			manifestContent = content
		}
		files = append(files, KnowledgeDocument{
			Path:            normalizedPath,
			SHA256:          sha,
			SizeBytes:       size,
			MimeType:        mimeType,
			Indexable:       isText,
			ContentSnapshot: snapshot,
		})
	}
	if manifestContent == "" {
		return Manifest{}, nil, "", fmt.Errorf("root %s required", ManifestFileName)
	}

	manifest, err := ParseManifest(manifestContent)
	if err != nil {
		return Manifest{}, nil, "", err
	}
	documents, packageHash := selectDocuments(manifest, files)
	if len(documents) == 0 {
		return Manifest{}, nil, "", fmt.Errorf("manifest selects no documents")
	}
	if err := verifyDeclaredHashes(req.PackageHash, req.TreeHash, packageHash); err != nil {
		return Manifest{}, nil, "", err
	}
	return manifest, documents, packageHash, nil
}

// normalizeGitPackageFiles converts extracted Git archive bytes into the same
// validated shape as a local snapshot: text files keep a content snapshot,
// binary files contribute hash and size only.
func normalizeGitPackageFiles(packageFiles []gitfetch.File) (Manifest, []KnowledgeDocument, string, error) {
	if len(packageFiles) > maxSnapshotFiles {
		return Manifest{}, nil, "", fmt.Errorf("too many files: max %d", maxSnapshotFiles)
	}
	files := make([]KnowledgeDocument, 0, len(packageFiles))
	manifestContent := ""
	seen := make(map[string]struct{}, len(packageFiles))

	for _, input := range packageFiles {
		normalizedPath, err := normalizeSnapshotPath(input.Path)
		if err != nil {
			return Manifest{}, nil, "", err
		}
		if _, ok := seen[normalizedPath]; ok {
			return Manifest{}, nil, "", fmt.Errorf("duplicate file path: %s", normalizedPath)
		}
		seen[normalizedPath] = struct{}{}

		mimeType := inferMimeType(normalizedPath)
		isText := isIndexableText(normalizedPath, mimeType) && utf8.Valid(input.Content)
		snapshot := ""
		if isText {
			snapshot = string(input.Content)
		}
		if normalizedPath == ManifestFileName {
			if !isText {
				return Manifest{}, nil, "", fmt.Errorf("%s must be UTF-8 text", ManifestFileName)
			}
			manifestContent = snapshot
		}
		files = append(files, KnowledgeDocument{
			Path:            normalizedPath,
			SHA256:          sha256HexBytes(input.Content),
			SizeBytes:       int64(len(input.Content)),
			MimeType:        mimeType,
			Indexable:       isText,
			ContentSnapshot: snapshot,
		})
	}
	if manifestContent == "" {
		return Manifest{}, nil, "", fmt.Errorf("root %s required", ManifestFileName)
	}
	manifest, err := ParseManifest(manifestContent)
	if err != nil {
		return Manifest{}, nil, "", err
	}
	documents, packageHash := selectDocuments(manifest, files)
	if len(documents) == 0 {
		return Manifest{}, nil, "", fmt.Errorf("manifest selects no documents")
	}
	return manifest, documents, packageHash, nil
}

// selectDocuments applies the manifest include/exclude rules. The manifest
// file always participates in package identity but is never returned as a
// document.
func selectDocuments(manifest Manifest, files []KnowledgeDocument) ([]KnowledgeDocument, string) {
	identity := make([]KnowledgeDocument, 0, len(files))
	documents := make([]KnowledgeDocument, 0, len(files))
	for _, file := range files {
		if file.Path == ManifestFileName {
			identity = append(identity, file)
			continue
		}
		if !manifest.SelectsDocument(file.Path) {
			continue
		}
		identity = append(identity, file)
		documents = append(documents, file)
	}
	return documents, computePackageHash(identity)
}

func verifyDeclaredHashes(declaredPackageHash, declaredTreeHash, computed string) error {
	packageHash := strings.TrimSpace(strings.ToLower(declaredPackageHash))
	if packageHash != "" {
		if !isSHA256Hex(packageHash) {
			return fmt.Errorf("invalid package_hash")
		}
		if packageHash != computed {
			return fmt.Errorf("package_hash does not match snapshot files")
		}
	}
	treeHash := strings.TrimSpace(strings.ToLower(declaredTreeHash))
	if treeHash != "" {
		if !isSHA256Hex(treeHash) {
			return fmt.Errorf("invalid tree_hash")
		}
		if treeHash != computed {
			return fmt.Errorf("tree_hash does not match local package hash")
		}
	}
	return nil
}

func normalizeSnapshotPath(value string) (string, error) {
	cleaned := gitfetch.NormalizeRelativePath(value)
	if cleaned == "" {
		return "", fmt.Errorf("file path required")
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || path.IsAbs(cleaned) {
		return "", fmt.Errorf("file path must be relative: %s", value)
	}
	if utf8.RuneCountInString(cleaned) > maxDocumentPathRunes {
		return "", fmt.Errorf("file path exceeds %d characters: %s", maxDocumentPathRunes, cleaned)
	}
	return cleaned, nil
}

func inferSourceName(req CreateKnowledgeSourceRequest) string {
	if req.PackagePath != "" {
		return pkgpath.SourceName(req.PackagePath)
	}
	repository := strings.TrimSuffix(strings.TrimRight(req.RepositoryURL, "/"), ".git")
	if repository != "" {
		base := path.Base(repository)
		if base != "." && base != "/" {
			return base
		}
	}
	return req.Type + "-knowledge-source"
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
	case ".csv":
		return "text/csv"
	case ".html", ".htm":
		return "text/html"
	default:
		return ""
	}
}

func isIndexableText(filePath, mimeType string) bool {
	if strings.HasPrefix(strings.ToLower(mimeType), "text/") {
		return true
	}
	switch strings.ToLower(path.Ext(filePath)) {
	case ".md", ".mdx", ".txt", ".json", ".yaml", ".yml", ".csv", ".html", ".htm":
		return true
	default:
		return false
	}
}

func packageRevisionKey(packageHash string) string {
	return "package:" + packageHash
}

// computePackageHash builds the canonical package identity hash. The
// algorithm intentionally matches internal/skills computePackageHash —
// "path\x00sha256\x00size" lines, sorted, joined with "\n", then SHA-256 —
// so Skill and Knowledge package identities stay comparable. Keep both
// implementations consistent.
func computePackageHash(files []KnowledgeDocument) string {
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

func sha256HexBytes(value []byte) string {
	hash := sha256.Sum256(value)
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
