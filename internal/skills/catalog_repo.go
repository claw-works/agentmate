package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type catalogRecord struct {
	Version  SkillVersion
	Artifact *CompiledSkillCatalog
}

const catalogVersionColumns = `version.id, version.account_id, version.user_id, version.key_id, version.source_id, version.source_revision_id, version.skill_name, version.version, version.content, version.content_hash, version.package_hash, version.agent_id, version.change_summary, version.eval_pass_rate, version.is_active, version.published_at`

func (r *Repo) GetVersion(ctx context.Context, accountID, versionID string) (*SkillVersion, error) {
	var version SkillVersion
	err := r.pool.QueryRow(ctx,
		`SELECT `+skillVersionColumns+` FROM skill_versions WHERE account_id = $1 AND id = $2`,
		accountID, versionID,
	).Scan(scanVersion(&version)...)
	if err != nil {
		return nil, err
	}
	return &version, nil
}

func (r *Repo) UpsertCompiledCatalog(ctx context.Context, artifact CompiledSkillCatalog) (*CompiledSkillCatalog, error) {
	triggers, err := json.Marshal(artifact.Triggers)
	if err != nil {
		return nil, err
	}
	capabilities, err := json.Marshal(artifact.Capabilities)
	if err != nil {
		return nil, err
	}
	constraints, err := json.Marshal(artifact.Constraints)
	if err != nil {
		return nil, err
	}
	dependencies, err := json.Marshal(artifact.Dependencies)
	if err != nil {
		return nil, err
	}
	manifest, err := json.Marshal(artifact.ResourceManifest)
	if err != nil {
		return nil, err
	}
	// nil contract → SQL NULL, keeping the column's NULL = "consults no knowledge"
	// convention aligned with the Go pointer. Marshalling a nil pointer would store
	// the JSON literal null, which is a present-but-empty value — a third state
	// nobody needs.
	var contract []byte
	if artifact.KnowledgeContract != nil {
		contract, err = json.Marshal(artifact.KnowledgeContract)
		if err != nil {
			return nil, err
		}
	}

	var stored CompiledSkillCatalog
	var rawTriggers, rawCapabilities, rawConstraints, rawDependencies, rawManifest, rawContract []byte
	err = r.pool.QueryRow(ctx,
		`INSERT INTO skill_compiled_catalogs
		 (account_id, skill_version_id, skill_name, compiler_name, compiler_version, input_package_hash,
		  description, triggers, capabilities, constraints, dependencies, resource_manifest,
		  knowledge_contract, knowledge_contract_identity, compiled_at)
		 SELECT $1, version.id, $3, $4, $5, $6, $7, $8::jsonb, $9::jsonb, $10::jsonb, $11::jsonb, $12::jsonb, $13::jsonb, $14, $15
		 FROM skill_versions AS version
		 WHERE version.account_id = $1 AND version.id = $2
		 ON CONFLICT (account_id, skill_version_id)
		 DO UPDATE SET
		   skill_name = EXCLUDED.skill_name,
		   compiler_name = EXCLUDED.compiler_name,
		   compiler_version = EXCLUDED.compiler_version,
		   input_package_hash = EXCLUDED.input_package_hash,
		   description = EXCLUDED.description,
		   triggers = EXCLUDED.triggers,
		   capabilities = EXCLUDED.capabilities,
		   constraints = EXCLUDED.constraints,
		   dependencies = EXCLUDED.dependencies,
		   resource_manifest = EXCLUDED.resource_manifest,
		   knowledge_contract = EXCLUDED.knowledge_contract,
		   knowledge_contract_identity = EXCLUDED.knowledge_contract_identity,
		   compiled_at = EXCLUDED.compiled_at
		 RETURNING id, account_id, skill_version_id, skill_name, compiler_name, compiler_version,
		           input_package_hash, description, triggers, capabilities, constraints, dependencies,
		           resource_manifest, knowledge_contract, knowledge_contract_identity, compiled_at`,
		artifact.AccountID, artifact.SkillVersionID, artifact.SkillName, artifact.CompilerName,
		artifact.CompilerVersion, artifact.InputPackageHash, artifact.Description, triggers,
		capabilities, constraints, dependencies, manifest, contract, artifact.KnowledgeContractIdentity, artifact.CompiledAt,
	).Scan(
		&stored.ID, &stored.AccountID, &stored.SkillVersionID, &stored.SkillName,
		&stored.CompilerName, &stored.CompilerVersion, &stored.InputPackageHash, &stored.Description,
		&rawTriggers, &rawCapabilities, &rawConstraints, &rawDependencies, &rawManifest,
		&rawContract, &stored.KnowledgeContractIdentity, &stored.CompiledAt,
	)
	if err != nil {
		return nil, err
	}
	stored.Version = artifact.Version
	stored.SourceID = artifact.SourceID
	stored.PublishedAt = artifact.PublishedAt
	if err := decodeCompiledJSON(&stored, rawTriggers, rawCapabilities, rawConstraints, rawDependencies, rawManifest, rawContract); err != nil {
		return nil, err
	}
	return &stored, nil
}

func (r *Repo) GetCompiledCatalog(ctx context.Context, accountID, versionID string) (*CompiledSkillCatalog, error) {
	var artifact CompiledSkillCatalog
	var rawTriggers, rawCapabilities, rawConstraints, rawDependencies, rawManifest, rawContract []byte
	err := r.pool.QueryRow(ctx,
		`SELECT catalog.id, catalog.account_id, catalog.skill_version_id, catalog.skill_name,
		        version.version, version.source_id, catalog.compiler_name, catalog.compiler_version, catalog.input_package_hash,
		        catalog.description, catalog.triggers, catalog.capabilities, catalog.constraints,
		        catalog.dependencies, catalog.resource_manifest, catalog.knowledge_contract,
		        catalog.knowledge_contract_identity, catalog.compiled_at, version.published_at
		 FROM skill_compiled_catalogs AS catalog
		 JOIN skill_versions AS version
		   ON version.id = catalog.skill_version_id AND version.account_id = catalog.account_id
		 WHERE catalog.account_id = $1 AND catalog.skill_version_id = $2`,
		accountID, versionID,
	).Scan(
		&artifact.ID, &artifact.AccountID, &artifact.SkillVersionID, &artifact.SkillName, &artifact.Version, &artifact.SourceID,
		&artifact.CompilerName, &artifact.CompilerVersion, &artifact.InputPackageHash, &artifact.Description,
		&rawTriggers, &rawCapabilities, &rawConstraints, &rawDependencies, &rawManifest,
		&rawContract, &artifact.KnowledgeContractIdentity,
		&artifact.CompiledAt, &artifact.PublishedAt,
	)
	if err != nil {
		return nil, err
	}
	if err := decodeCompiledJSON(&artifact, rawTriggers, rawCapabilities, rawConstraints, rawDependencies, rawManifest, rawContract); err != nil {
		return nil, err
	}
	return &artifact, nil
}

func (r *Repo) ListActiveCatalog(ctx context.Context, accountID string, params SkillCatalogListParams) ([]catalogRecord, error) {
	query := strings.ToLower(strings.TrimSpace(params.Query))
	rows, err := r.pool.Query(ctx,
		`SELECT `+catalogVersionColumns+`,
		        catalog.id, catalog.skill_name, catalog.compiler_name, catalog.compiler_version, catalog.input_package_hash,
		        catalog.description, catalog.triggers, catalog.capabilities, catalog.constraints,
		        catalog.dependencies, catalog.resource_manifest, catalog.knowledge_contract,
		        catalog.knowledge_contract_identity, catalog.compiled_at
		 FROM skill_versions AS version
		 LEFT JOIN skill_compiled_catalogs AS catalog
		   ON catalog.account_id = version.account_id AND catalog.skill_version_id = version.id
		 WHERE version.account_id = $1
		   AND version.is_active = true
		   AND ($2 = '' OR lower(version.skill_name) LIKE '%' || $2 || '%'
		        OR lower(COALESCE(catalog.skill_name, '')) LIKE '%' || $2 || '%'
		        OR lower(COALESCE(catalog.description, '')) LIKE '%' || $2 || '%'
		        OR lower(COALESCE(catalog.triggers::text, '')) LIKE '%' || $2 || '%'
		        OR lower(COALESCE(catalog.capabilities::text, '')) LIKE '%' || $2 || '%'
		        OR (catalog.id IS NULL AND lower(version.content) LIKE '%' || $2 || '%'))
		 ORDER BY lower(version.skill_name), version.skill_name, version.published_at DESC, version.id
		 LIMIT $3 OFFSET $4`,
		accountID, query, params.Limit, params.Offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := make([]catalogRecord, 0)
	for rows.Next() {
		var record catalogRecord
		versionDestinations := scanVersion(&record.Version)
		var artifactID, artifactSkillName, compilerName, compilerVersion, inputPackageHash, description *string
		var rawTriggers, rawCapabilities, rawConstraints, rawDependencies, rawManifest, rawContract []byte
		var contractIdentity *string
		var compiledAt *time.Time
		destinations := append(versionDestinations,
			&artifactID, &artifactSkillName, &compilerName, &compilerVersion, &inputPackageHash, &description,
			&rawTriggers, &rawCapabilities, &rawConstraints, &rawDependencies, &rawManifest,
			&rawContract, &contractIdentity, &compiledAt,
		)
		if err := rows.Scan(destinations...); err != nil {
			return nil, err
		}
		if artifactID != nil {
			artifact := &CompiledSkillCatalog{
				ID:                        *artifactID,
				AccountID:                 accountID,
				SkillVersionID:            record.Version.ID,
				SourceID:                  record.Version.SourceID,
				SkillName:                 stringValue(artifactSkillName),
				Version:                   record.Version.Version,
				CompilerName:              stringValue(compilerName),
				CompilerVersion:           stringValue(compilerVersion),
				InputPackageHash:          stringValue(inputPackageHash),
				Description:               stringValue(description),
				KnowledgeContractIdentity: stringValue(contractIdentity),
				CompiledAt:                *compiledAt,
				PublishedAt:               record.Version.PublishedAt,
			}
			if err := decodeCompiledJSON(artifact, rawTriggers, rawCapabilities, rawConstraints, rawDependencies, rawManifest, rawContract); err != nil {
				return nil, err
			}
			record.Artifact = artifact
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (r *Repo) CountActiveCatalog(ctx context.Context, accountID, query string) (int, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	var total int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*)
		 FROM skill_versions AS version
		 LEFT JOIN skill_compiled_catalogs AS catalog
		   ON catalog.account_id = version.account_id AND catalog.skill_version_id = version.id
		 WHERE version.account_id = $1
		   AND version.is_active = true
		   AND ($2 = '' OR lower(version.skill_name) LIKE '%' || $2 || '%'
		        OR lower(COALESCE(catalog.skill_name, '')) LIKE '%' || $2 || '%'
		        OR lower(COALESCE(catalog.description, '')) LIKE '%' || $2 || '%'
		        OR lower(COALESCE(catalog.triggers::text, '')) LIKE '%' || $2 || '%'
		        OR lower(COALESCE(catalog.capabilities::text, '')) LIKE '%' || $2 || '%'
		        OR (catalog.id IS NULL AND lower(version.content) LIKE '%' || $2 || '%'))`,
		accountID, query,
	).Scan(&total)
	return total, err
}

func (r *Repo) GetTextResource(ctx context.Context, accountID, versionID, fileID string) (*SkillVersionFile, error) {
	var file SkillVersionFile
	err := r.pool.QueryRow(ctx,
		`SELECT file.id, file.account_id, file.user_id, file.key_id, file.source_revision_id,
		        file.version_id, file.path, file.kind, file.sha256, file.size_bytes,
		        file.mime_type, file.indexable, file.content_snapshot, file.created_at
		 FROM skill_version_files AS file
		 JOIN skill_versions AS version
		   ON version.id = file.version_id AND version.account_id = file.account_id
		 WHERE version.account_id = $1
		   AND version.id = $2
		   AND file.account_id = $1
		   AND file.version_id = $2
		   AND file.id = $3
		   AND file.path <> 'SKILL.md'
		   AND file.indexable = true
		   AND file.content_snapshot <> ''`,
		accountID, versionID, fileID,
	).Scan(scanVersionFile(&file)...)
	if err != nil {
		return nil, err
	}
	return &file, nil
}

func decodeCompiledJSON(artifact *CompiledSkillCatalog, rawTriggers, rawCapabilities, rawConstraints, rawDependencies, rawManifest, rawContract []byte) error {
	for _, target := range []struct {
		raw   []byte
		value any
	}{
		{rawTriggers, &artifact.Triggers},
		{rawCapabilities, &artifact.Capabilities},
		{rawConstraints, &artifact.Constraints},
		{rawDependencies, &artifact.Dependencies},
		{rawManifest, &artifact.ResourceManifest},
	} {
		if len(target.raw) == 0 {
			continue
		}
		if err := json.Unmarshal(target.raw, target.value); err != nil {
			return fmt.Errorf("decode compiled catalog: %w", err)
		}
	}
	// SQL NULL arrives as an empty slice and stays a nil pointer: the column's NULL
	// means "consults no knowledge", not "empty contract". An artifact written before
	// migration 000034 also lands here — its contract is recovered by the read
	// fallback that parses the immutable version content, not by this decoder.
	if len(rawContract) > 0 {
		contract := &KnowledgeContract{}
		if err := json.Unmarshal(rawContract, contract); err != nil {
			return fmt.Errorf("decode compiled catalog: %w", err)
		}
		artifact.KnowledgeContract = contract
	}
	artifact.Triggers = nonNilStrings(artifact.Triggers)
	artifact.Capabilities = nonNilStrings(artifact.Capabilities)
	artifact.Constraints = nonNilStrings(artifact.Constraints)
	artifact.Dependencies = nonNilStrings(artifact.Dependencies)
	if artifact.ResourceManifest == nil {
		artifact.ResourceManifest = []SkillResourceManifestItem{}
	}
	sortResourceManifest(artifact.ResourceManifest)
	return nil
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func isNotFound(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
