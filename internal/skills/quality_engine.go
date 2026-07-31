package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
)

const maxSelectedResourceBytes = 8 * 1024 * 1024

func EvaluateSkillQuality(input QualityEngineInput) QualityReport {
	lint, eval := evaluatePackage(input.Package)
	report := QualityReport{
		SchemaVersion: "1.0",
		EngineVersion: QualityEngineVersion,
		Checkset:      QualityChecksetVersion,
		Input:         packageRef(input.Package.Version),
		Lint:          lint,
		Eval:          eval,
		Comparison:    comparePackages(input.Package, input.Baseline, lint, eval),
		Telemetry:     buildQualityTelemetry(input.Package.Version.ID, input.TelemetryCutoff, input.TelemetryLogs),
	}
	return report
}

func evaluatePackage(pkg QualityPackage) ([]QualityCheck, []QualityCheck) {
	version := pkg.Version
	files := append([]SkillVersionFile(nil), pkg.Files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	rootFile, hasRootFile := rootSkillFile(files)
	requiresFile := version.SourceRevisionID != nil || len(files) > 0
	rootPassed := strings.TrimSpace(version.Content) != "" && (!requiresFile || hasRootFile)
	rootEvidence := map[string]any{"content_present": strings.TrimSpace(version.Content) != "", "file_record_required": requiresFile, "file_record_present": hasRootFile}
	if hasRootFile {
		rootEvidence["file_id"] = rootFile.ID
		rootEvidence["sha256"] = rootFile.SHA256
	}

	strictApplicable := len(files) > 0
	strictPassed := true
	for _, file := range files {
		strictPassed = strictPassed && valueOrEmpty(file.AccountID) == valueOrEmpty(version.AccountID) && file.VersionID != nil && *file.VersionID == version.ID
	}

	canonicalApplicable := true
	computedPackageHash := ""
	canonicalEvidence := map[string]any{"stored_hash": version.PackageHash, "stored_content_hash": version.ContentHash}
	canonicalPassed := false
	if len(files) > 0 {
		computedPackageHash = computePackageHash(files)
		canonicalPassed = computedPackageHash == version.PackageHash
		canonicalEvidence["identity_mode"] = "package_files"
	} else {
		computedPackageHash = sha256HexString(version.Content)
		canonicalPassed = computedPackageHash == version.ContentHash && computedPackageHash == version.PackageHash
		canonicalEvidence["identity_mode"] = "direct_body"
	}
	canonicalEvidence["computed_hash"] = computedPackageHash

	textApplicable := false
	textPassed := true
	textChecked := 0
	for _, file := range files {
		if file.ContentSnapshot == "" {
			continue
		}
		textApplicable = true
		textChecked++
		textPassed = textPassed && sha256HexString(file.ContentSnapshot) == file.SHA256 && int64(len([]byte(file.ContentSnapshot))) == file.SizeBytes
		if file.Path == "SKILL.md" {
			textPassed = textPassed && file.ContentSnapshot == version.Content
		}
	}

	hasFrontmatter := strings.HasPrefix(strings.TrimSpace(strings.ReplaceAll(version.Content, "\r\n", "\n")), "---\n")
	metadata, frontmatterErr := parseSkillFrontmatter(version.Content)
	// The knowledge contract is checked separately from the rest of the frontmatter because
	// the two answer different questions: whether the block parses and can be executed, and
	// then whether what it says is a good idea. Only the first can fail a compile.
	contractErr := ValidateKnowledgeContract(metadata.Knowledge)
	contractApplicable := metadata.Knowledge != nil
	contractEvidence := map[string]any{"contract_present": contractApplicable}
	if contractApplicable {
		contractEvidence["mode"] = metadata.Knowledge.Mode
		contractEvidence["requirement_count"] = len(metadata.Knowledge.Requirements)
		contractEvidence["identity"] = ContractIdentity(metadata.Knowledge)
	}
	if contractErr != nil {
		contractEvidence["error"] = contractErr.Error()
	}
	contractFindings := LintKnowledgeContract(metadata.Knowledge)
	// One quality check per advisory rule, so a report says which concern fired rather than
	// only that something did. A single aggregate check would make every finding look alike.
	contractAdvisory := map[string][]map[string]any{}
	for _, finding := range contractFindings {
		contractAdvisory[finding.Rule] = append(contractAdvisory[finding.Rule], map[string]any{
			"requirement_id": finding.RequirementID, "detail": finding.Detail,
		})
	}
	advisoryCheck := func(rule string) QualityCheck {
		hits := contractAdvisory[rule]
		evidence := map[string]any{"findings": len(hits)}
		if len(hits) > 0 {
			evidence["details"] = hits
		}
		// Not applicable without a contract: a Skill that consults no knowledge has not
		// passed this rule, it simply has no opinion to hold.
		return qualityCheck(rule, len(hits) == 0, contractApplicable && contractErr == nil, evidence)
	}
	if frontmatterErr == nil {
		frontmatterErr = validateSkillFrontmatter(metadata)
	}
	frontmatterApplicable := hasFrontmatter
	frontmatterPassed := !frontmatterApplicable || frontmatterErr == nil
	frontmatterEvidence := map[string]any{"frontmatter_present": hasFrontmatter}
	if frontmatterErr != nil {
		frontmatterEvidence["error"] = frontmatterErr.Error()
	}

	metadataApplicable := frontmatterApplicable && frontmatterErr == nil
	nameApplicable := metadataApplicable
	namePassed := !nameApplicable || metadata.Name != "" && metadata.Name == version.SkillName
	descriptionApplicable := metadataApplicable
	descriptionPassed := !descriptionApplicable || strings.TrimSpace(metadata.Description) != ""
	routingApplicable := metadataApplicable
	routingPassed := !routingApplicable || len(metadata.Triggers) > 0 || len(metadata.Capabilities) > 0
	duplicateApplicable := metadataApplicable
	duplicates := routingDuplicates(metadata)
	duplicatePassed := !duplicateApplicable || len(duplicates) == 0
	selfDependencyApplicable := metadataApplicable
	selfDependencies := matchingSelfDependencies(version.SkillName, metadata)
	selfDependencyPassed := !selfDependencyApplicable || len(selfDependencies) == 0

	artifactApplicable := true
	artifactPassed := compiledArtifactCurrent(pkg)
	artifactEvidence := map[string]any{"artifact_present": pkg.Artifact != nil, "expected_compiler": SkillCompilerName, "expected_compiler_version": SkillCompilerVersion}
	if pkg.Artifact != nil {
		artifactEvidence["actual_compiler"] = pkg.Artifact.CompilerName
		artifactEvidence["actual_compiler_version"] = pkg.Artifact.CompilerVersion
		artifactEvidence["input_package_hash"] = pkg.Artifact.InputPackageHash
	}

	expectedManifest := resourceManifestFromFiles(files)
	manifestApplicable := pkg.Artifact != nil
	manifestPassed := manifestApplicable && reflect.DeepEqual(expectedManifest, pkg.Artifact.ResourceManifest)
	manifestEvidence := map[string]any{"expected_count": len(expectedManifest)}
	if pkg.Artifact != nil {
		manifestEvidence["actual_count"] = len(pkg.Artifact.ResourceManifest)
	}

	lint := []QualityCheck{
		qualityCheck("root_skill_content_exists", rootPassed, true, rootEvidence),
		qualityCheck("strict_account_version_files", strictPassed, strictApplicable, map[string]any{"file_count": len(files)}),
		qualityCheck("canonical_package_hash_matches", canonicalPassed, canonicalApplicable, canonicalEvidence),
		qualityCheck("text_snapshot_hash_size_matches", textPassed, textApplicable, map[string]any{"checked_snapshots": textChecked}),
		qualityCheck("frontmatter_valid", frontmatterPassed, frontmatterApplicable, frontmatterEvidence),
		qualityCheck("knowledge_contract_valid", contractErr == nil, contractApplicable, contractEvidence),
		advisoryCheck(lintContractCitationsOnRequired),
		advisoryCheck(lintContractFreshness),
		advisoryCheck(lintContractBudgetHeadroom),
		advisoryCheck(lintContractMatchDiscriminates),
		advisoryCheck(lintContractPinNamesBuild),
		advisoryCheck(lintContractPurposeDocumented),
		qualityCheck("frontmatter_name_matches", namePassed, nameApplicable, map[string]any{"frontmatter_name": metadata.Name, "registry_name": version.SkillName}),
		qualityCheck("description_present", descriptionPassed, descriptionApplicable, map[string]any{"present": strings.TrimSpace(metadata.Description) != ""}),
		qualityCheck("routing_metadata_present", routingPassed, routingApplicable, map[string]any{"trigger_count": len(metadata.Triggers), "capability_count": len(metadata.Capabilities)}),
		qualityCheck("routing_metadata_no_duplicates", duplicatePassed, duplicateApplicable, map[string]any{"duplicates": duplicates}),
		qualityCheck("self_dependency_absent", selfDependencyPassed, selfDependencyApplicable, map[string]any{"matches": selfDependencies}),
		qualityCheck("compiled_artifact_current", artifactPassed, artifactApplicable, artifactEvidence),
		qualityCheck("resource_manifest_consistent", manifestPassed, manifestApplicable, manifestEvidence),
	}

	compileTime := time.Unix(0, 0).UTC()
	first, firstErr := CompileSkillVersion(version, files, compileTime)
	second, secondErr := CompileSkillVersion(version, files, compileTime)
	compileRepeatable := firstErr == nil && secondErr == nil && reflect.DeepEqual(first, second)
	compileEvidence := map[string]any{"first_succeeded": firstErr == nil, "second_succeeded": secondErr == nil}
	if firstErr != nil {
		compileEvidence["error"] = firstErr.Error()
	}

	reversed := append([]SkillVersionFile(nil), files...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	reordered, reorderedErr := CompileSkillVersion(version, reversed, compileTime)
	orderInvariant := firstErr == nil && reorderedErr == nil && reflect.DeepEqual(first, reordered)
	orderEvidence := map[string]any{"file_count": len(files), "compile_succeeded": reorderedErr == nil}
	if reorderedErr != nil {
		orderEvidence["error"] = reorderedErr.Error()
	}

	l0Applicable := firstErr == nil
	l0Passed := false
	l0Bytes := 0
	if l0Applicable {
		encoded, marshalErr := json.Marshal(first)
		l0Bytes = len(encoded)
		lower := strings.ToLower(string(encoded))
		l0Passed = marshalErr == nil && !strings.Contains(lower, `"content"`) && !strings.Contains(lower, `"instructions"`)
		if version.Content != "" {
			l0Passed = l0Passed && !strings.Contains(string(encoded), version.Content)
		}
	}

	stableManifestApplicable := firstErr == nil
	stableManifestPassed := stableManifestApplicable && reflect.DeepEqual(first.ResourceManifest, expectedManifest)
	selectedApplicable := len(expectedManifest) > 0
	selectedPassed, selectedEvidence := selectedResourceContract(files, expectedManifest)

	eval := []QualityCheck{
		qualityCheck("compile_repeatability", compileRepeatable, true, compileEvidence),
		qualityCheck("file_order_invariance", orderInvariant, true, orderEvidence),
		qualityCheck("l0_excludes_instruction_and_resource_body", l0Passed, l0Applicable, map[string]any{"encoded_bytes": l0Bytes}),
		qualityCheck("resource_manifest_exact_and_stable", stableManifestPassed, stableManifestApplicable, map[string]any{"resource_count": len(expectedManifest)}),
		qualityCheck("selected_resource_isolation_and_bounds", selectedPassed, selectedApplicable, selectedEvidence),
	}
	return lint, eval
}

func qualityCheck(checkID string, passed, applicable bool, evidence map[string]any) QualityCheck {
	if evidence == nil {
		evidence = map[string]any{}
	}
	return QualityCheck{ID: checkID, Severity: qualityCheckSeverity(checkID), Passed: passed, Applicable: applicable, Evidence: evidence}
}

func qualityCheckSeverity(checkID string) QualitySeverity {
	switch checkID {
	case "root_skill_content_exists", "strict_account_version_files", "canonical_package_hash_matches", "text_snapshot_hash_size_matches":
		return QualitySeverityBlocker
	case "frontmatter_valid", "frontmatter_name_matches", "self_dependency_absent", "resource_manifest_consistent",
		"compile_repeatability", "file_order_invariance", "l0_excludes_instruction_and_resource_body",
		"resource_manifest_exact_and_stable", "selected_resource_isolation_and_bounds":
		return QualitySeverityError
	case "description_present", "routing_metadata_present", "routing_metadata_no_duplicates", "compiled_artifact_current":
		return QualitySeverityWarning
	case "knowledge_contract_valid":
		// Error, matching frontmatter_valid: an unexecutable contract is not advice, it is a
		// Skill that will not compile.
		return QualitySeverityError
	case lintContractCitationsOnRequired, lintContractFreshness, lintContractPinNamesBuild,
		lintContractMatchDiscriminates:
		return QualitySeverityWarning
	case lintContractBudgetHeadroom, lintContractPurposeDocumented:
		// Info-level concerns. There is no info severity here, and warning is the closest
		// honest level: both are worth knowing and neither is worth blocking on.
		return QualitySeverityWarning
	default:
		return QualitySeverityError
	}
}

func normalizeQualityReport(report *QualityReport) {
	if report == nil {
		return
	}
	for index := range report.Lint {
		if report.Lint[index].Severity == "" {
			report.Lint[index].Severity = qualityCheckSeverity(report.Lint[index].ID)
		}
		if report.Lint[index].Evidence == nil {
			report.Lint[index].Evidence = map[string]any{}
		}
	}
	for index := range report.Eval {
		if report.Eval[index].Severity == "" {
			report.Eval[index].Severity = qualityCheckSeverity(report.Eval[index].ID)
		}
		if report.Eval[index].Evidence == nil {
			report.Eval[index].Evidence = map[string]any{}
		}
	}
}

func rootSkillFile(files []SkillVersionFile) (SkillVersionFile, bool) {
	for _, file := range files {
		if file.Path == "SKILL.md" {
			return file, true
		}
	}
	return SkillVersionFile{}, false
}

func routingDuplicates(metadata skillFrontmatter) []string {
	duplicates := make([]string, 0)
	for _, group := range []struct {
		name   string
		values []string
	}{
		{"triggers", metadata.Triggers},
		{"capabilities", metadata.Capabilities},
		{"constraints", metadata.Constraints},
		{"dependencies", metadata.Dependencies},
	} {
		seen := map[string]struct{}{}
		for _, value := range group.values {
			key := strings.ToLower(strings.TrimSpace(value))
			if _, exists := seen[key]; exists {
				duplicates = append(duplicates, group.name+":"+key)
				continue
			}
			seen[key] = struct{}{}
		}
	}
	sort.Strings(duplicates)
	return duplicates
}

func matchingSelfDependencies(skillName string, metadata skillFrontmatter) []string {
	names := map[string]struct{}{strings.ToLower(strings.TrimSpace(skillName)): {}}
	if metadata.Name != "" {
		names[strings.ToLower(strings.TrimSpace(metadata.Name))] = struct{}{}
	}
	matches := make([]string, 0)
	for _, dependency := range metadata.Dependencies {
		if _, exists := names[strings.ToLower(strings.TrimSpace(dependency))]; exists {
			matches = append(matches, dependency)
		}
	}
	sort.Strings(matches)
	return matches
}

func compiledArtifactCurrent(pkg QualityPackage) bool {
	if pkg.Artifact == nil {
		return false
	}
	artifact := pkg.Artifact
	if artifact.SkillVersionID != pkg.Version.ID ||
		artifact.SkillName != pkg.Version.SkillName ||
		artifact.Version != pkg.Version.Version ||
		artifact.CompilerName != SkillCompilerName ||
		artifact.CompilerVersion != SkillCompilerVersion ||
		artifact.InputPackageHash != pkg.Version.PackageHash {
		return false
	}
	expected, err := CompileSkillVersion(pkg.Version, pkg.Files, artifact.CompiledAt)
	if err != nil {
		return false
	}
	return expected.Description == artifact.Description &&
		reflect.DeepEqual(expected.Triggers, artifact.Triggers) &&
		reflect.DeepEqual(expected.Capabilities, artifact.Capabilities) &&
		reflect.DeepEqual(expected.Constraints, artifact.Constraints) &&
		reflect.DeepEqual(expected.Dependencies, artifact.Dependencies) &&
		reflect.DeepEqual(expected.ResourceManifest, artifact.ResourceManifest)
}

func selectedResourceContract(files []SkillVersionFile, manifest []SkillResourceManifestItem) (bool, map[string]any) {
	filesByID := make(map[string]SkillVersionFile, len(files))
	for _, file := range files {
		filesByID[file.ID] = file
	}
	passed := true
	unique := map[string]struct{}{}
	textAvailable := 0
	for _, item := range manifest {
		file, exists := filesByID[item.FileID]
		_, duplicate := unique[item.FileID]
		unique[item.FileID] = struct{}{}
		passed = passed && exists && !duplicate && item.Path != "SKILL.md" && file.Path == item.Path && file.SHA256 == item.SHA256 && file.SizeBytes == item.SizeBytes
		if item.TextAvailable {
			textAvailable++
			passed = passed && file.ContentSnapshot != "" && len([]byte(file.ContentSnapshot)) <= maxSelectedResourceBytes && sha256HexString(file.ContentSnapshot) == file.SHA256
		}
	}
	return passed, map[string]any{"resource_count": len(manifest), "text_available_count": textAvailable, "max_selected_resource_bytes": maxSelectedResourceBytes}
}

func comparePackages(current QualityPackage, baseline *QualityPackage, currentLint, currentEval []QualityCheck) QualityComparison {
	result := QualityComparison{
		Status:          "not_available",
		FilesAdded:      []QualityFileChange{},
		FilesRemoved:    []QualityFileChange{},
		FilesModified:   []QualityFileChange{},
		RoutingDiffs:    []QualityRoutingDiff{},
		LintRegressions: []string{},
		EvalRegressions: []string{},
	}
	if baseline == nil {
		return result
	}
	baselineID := baseline.Version.ID
	result.BaselineVersionID = &baselineID
	baselineLint, baselineEval := evaluatePackage(*baseline)
	result.FilesAdded, result.FilesRemoved, result.FilesModified = compareFiles(current.Files, baseline.Files)
	result.PackageHashChanged = current.Version.PackageHash != baseline.Version.PackageHash
	currentArtifact := comparisonArtifact(current)
	baselineArtifact := comparisonArtifact(*baseline)
	result.ResourceManifestChanged = compareResourceManifestBehavior(currentArtifact, baselineArtifact)
	result.LintRegressions = regressions(currentLint, baselineLint)
	result.EvalRegressions = regressions(currentEval, baselineEval)
	if checksBlocked(currentLint) || checksBlocked(baselineLint) {
		result.Status = "blocked"
		return result
	}
	result.RoutingDiffs = compareRouting(currentArtifact, baselineArtifact)
	if len(result.LintRegressions)+len(result.EvalRegressions) > 0 {
		result.Status = "static_regression_detected"
	} else if result.PackageHashChanged || result.ResourceManifestChanged || len(result.FilesAdded)+len(result.FilesRemoved)+len(result.FilesModified)+len(result.RoutingDiffs) > 0 {
		result.Status = "review_required"
	} else {
		result.Status = "no_static_regression_detected"
	}
	return result
}

func comparisonArtifact(pkg QualityPackage) *CompiledSkillCatalog {
	compiled, err := CompileSkillVersion(pkg.Version, pkg.Files, time.Unix(0, 0).UTC())
	if err == nil {
		return &compiled
	}
	return pkg.Artifact
}

type resourceManifestBehavior struct {
	Path          string
	Kind          string
	MimeType      string
	Indexable     bool
	TextAvailable bool
}

func compareResourceManifestBehavior(current, baseline *CompiledSkillCatalog) bool {
	if current == nil || baseline == nil {
		return false
	}
	return !reflect.DeepEqual(manifestBehavior(current.ResourceManifest), manifestBehavior(baseline.ResourceManifest))
}

func manifestBehavior(manifest []SkillResourceManifestItem) []resourceManifestBehavior {
	result := make([]resourceManifestBehavior, 0, len(manifest))
	for _, item := range manifest {
		result = append(result, resourceManifestBehavior{
			Path:          item.Path,
			Kind:          item.Kind,
			MimeType:      item.MimeType,
			Indexable:     item.Indexable,
			TextAvailable: item.TextAvailable,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Path != result[j].Path {
			return result[i].Path < result[j].Path
		}
		if result[i].Kind != result[j].Kind {
			return result[i].Kind < result[j].Kind
		}
		return result[i].MimeType < result[j].MimeType
	})
	return result
}

func compareFiles(current, baseline []SkillVersionFile) ([]QualityFileChange, []QualityFileChange, []QualityFileChange) {
	currentByPath := make(map[string]string, len(current))
	baselineByPath := make(map[string]string, len(baseline))
	for _, file := range current {
		currentByPath[file.Path] = file.SHA256
	}
	for _, file := range baseline {
		baselineByPath[file.Path] = file.SHA256
	}
	added := make([]QualityFileChange, 0)
	removed := make([]QualityFileChange, 0)
	modified := make([]QualityFileChange, 0)
	for pathValue, after := range currentByPath {
		before, exists := baselineByPath[pathValue]
		if !exists {
			added = append(added, QualityFileChange{Path: pathValue, AfterHash: after})
		} else if before != after {
			modified = append(modified, QualityFileChange{Path: pathValue, BeforeHash: before, AfterHash: after})
		}
	}
	for pathValue, before := range baselineByPath {
		if _, exists := currentByPath[pathValue]; !exists {
			removed = append(removed, QualityFileChange{Path: pathValue, BeforeHash: before})
		}
	}
	for _, items := range [][]QualityFileChange{added, removed, modified} {
		sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	}
	return added, removed, modified
}

func compareRouting(current, baseline *CompiledSkillCatalog) []QualityRoutingDiff {
	if current == nil || baseline == nil {
		return []QualityRoutingDiff{}
	}
	fields := []struct {
		name   string
		before []string
		after  []string
	}{
		{"description", []string{baseline.Description}, []string{current.Description}},
		{"triggers", baseline.Triggers, current.Triggers},
		{"capabilities", baseline.Capabilities, current.Capabilities},
		{"constraints", baseline.Constraints, current.Constraints},
		{"dependencies", baseline.Dependencies, current.Dependencies},
	}
	diffs := make([]QualityRoutingDiff, 0)
	for _, field := range fields {
		before := append([]string(nil), field.before...)
		after := append([]string(nil), field.after...)
		if !reflect.DeepEqual(before, after) {
			diffs = append(diffs, QualityRoutingDiff{Field: field.name, Before: before, After: after})
		}
	}
	return diffs
}

func regressions(current, baseline []QualityCheck) []string {
	baselineByID := make(map[string]QualityCheck, len(baseline))
	for _, check := range baseline {
		baselineByID[check.ID] = check
	}
	result := make([]string, 0)
	for _, check := range current {
		before, exists := baselineByID[check.ID]
		if exists && before.Applicable && before.Passed && check.Applicable && !check.Passed {
			result = append(result, check.ID)
		}
	}
	sort.Strings(result)
	return result
}

func checksBlocked(checks []QualityCheck) bool {
	for _, check := range checks {
		if check.Severity == QualitySeverityBlocker && check.Applicable && !check.Passed {
			return true
		}
	}
	return false
}

func buildQualityTelemetry(versionID string, cutoff time.Time, input []SkillLog) QualityTelemetry {
	logs := make([]SkillLog, 0, len(input))
	for _, logEntry := range input {
		if logEntry.SkillVersionID == nil || *logEntry.SkillVersionID != versionID || logEntry.CreatedAt.After(cutoff) {
			continue
		}
		logs = append(logs, logEntry)
	}
	sort.Slice(logs, func(i, j int) bool {
		if logs[i].CreatedAt.Equal(logs[j].CreatedAt) {
			return logs[i].ID > logs[j].ID
		}
		return logs[i].CreatedAt.After(logs[j].CreatedAt)
	})
	if len(logs) > maxTelemetryLogs {
		logs = logs[:maxTelemetryLogs]
	}

	telemetry := QualityTelemetry{
		Status:      "insufficient",
		Cutoff:      cutoff.UTC(),
		Considered:  len(logs),
		Suggestions: []QualitySuggestion{},
	}
	categoryIDs := map[string][]string{"bypass": {}, "correction": {}, "failure": {}, "partial": {}}
	for _, logEntry := range logs {
		if !logEntry.WasTriggered {
			telemetry.Bypass++
			categoryIDs["bypass"] = append(categoryIDs["bypass"], logEntry.ID)
			continue
		}
		telemetry.Triggered++
		switch logEntry.Outcome {
		case "success":
			telemetry.Outcomes.Success++
		case "failure":
			telemetry.Outcomes.Failure++
			categoryIDs["failure"] = append(categoryIDs["failure"], logEntry.ID)
		case "partial":
			telemetry.Outcomes.Partial++
			categoryIDs["partial"] = append(categoryIDs["partial"], logEntry.ID)
		case "user_corrected":
			telemetry.Outcomes.UserCorrected++
			categoryIDs["correction"] = append(categoryIDs["correction"], logEntry.ID)
		default:
			telemetry.Outcomes.Other++
		}
	}
	telemetry.OutcomeDenominator = telemetry.Triggered
	if telemetry.Triggered < minTelemetryTriggered {
		return telemetry
	}
	telemetry.Status = "sufficient"
	for _, category := range []string{"bypass", "correction", "failure", "partial"} {
		ids := categoryIDs[category]
		denominator := telemetry.Triggered
		if category == "bypass" {
			denominator = telemetry.Considered
		}
		if denominator == 0 || len(ids) < 3 || float64(len(ids))/float64(denominator) < 0.10 {
			continue
		}
		telemetry.Suggestions = append(telemetry.Suggestions, QualitySuggestion{
			Category:    category,
			Count:       len(ids),
			Denominator: denominator,
			Rate:        float64(len(ids)) / float64(denominator),
			Fingerprint: suggestionFingerprint(versionID, category, ids),
			LogIDs:      append([]string(nil), ids...),
		})
	}
	return telemetry
}

func suggestionFingerprint(versionID, category string, logIDs []string) string {
	payload := fmt.Sprintf("%s\x00%s\x00%s", versionID, category, strings.Join(logIDs, "\x00"))
	hash := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(hash[:])
}

func packageRef(version SkillVersion) QualityPackageRef {
	return QualityPackageRef{VersionID: version.ID, SkillName: version.SkillName, Version: version.Version, PackageHash: version.PackageHash}
}
