package skills

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestEvaluateSkillQualityDeterministicAndBodyFree(t *testing.T) {
	pkg := qualityTestPackage(t, "version-2", "v2", "resource-v2")
	baseline := qualityTestPackage(t, "version-1", "v1", "resource-v1")
	cutoff := time.Date(2026, time.July, 22, 1, 2, 3, 0, time.UTC)
	logs := qualityTestLogs(pkg.Version.ID, cutoff, 20, 3)
	input := QualityEngineInput{Package: pkg, Baseline: &baseline, TelemetryLogs: logs, TelemetryCutoff: cutoff}

	first := EvaluateSkillQuality(input)
	second := EvaluateSkillQuality(input)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("quality report is not deterministic:\nfirst=%#v\nsecond=%#v", first, second)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if len(encoded) > maxQualityReportBytes {
		t.Fatalf("report bytes = %d, max %d", len(encoded), maxQualityReportBytes)
	}
	for _, forbidden := range []string{"# Quality test instructions", "resource-v2", "failure body", "correction body"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("quality report leaked body %q", forbidden)
		}
	}
	if first.Comparison.Status != "review_required" {
		t.Fatalf("comparison status = %q, want review_required", first.Comparison.Status)
	}
	if !first.Comparison.PackageHashChanged {
		t.Fatal("comparison did not report changed package hash")
	}
	if first.Telemetry.Status != "sufficient" || len(first.Telemetry.Suggestions) != 1 || first.Telemetry.Suggestions[0].Category != "failure" {
		t.Fatalf("telemetry = %#v", first.Telemetry)
	}
	for _, check := range append(append([]QualityCheck{}, first.Lint...), first.Eval...) {
		if !check.Applicable || !check.Passed {
			t.Fatalf("check failed: %#v", check)
		}
	}
}

func TestQualityTelemetryExcludesUnassignedAndUsesTriggeredDenominator(t *testing.T) {
	versionID := "version-telemetry"
	cutoff := time.Date(2026, time.July, 22, 2, 0, 0, 0, time.UTC)
	logs := qualityTestLogs(versionID, cutoff, 17, 3)
	logs = append(logs,
		SkillLog{ID: "unassigned", SkillVersionID: nil, WasTriggered: true, Outcome: "failure", CreatedAt: cutoff.Add(-time.Minute)},
		SkillLog{ID: "other-version", SkillVersionID: stringPointer("other"), WasTriggered: true, Outcome: "failure", CreatedAt: cutoff.Add(-time.Minute)},
		SkillLog{ID: "future", SkillVersionID: &versionID, WasTriggered: true, Outcome: "failure", CreatedAt: cutoff.Add(time.Second)},
		SkillLog{ID: "bypass-1", SkillVersionID: &versionID, WasTriggered: false, Outcome: "failure", CreatedAt: cutoff.Add(-time.Minute)},
		SkillLog{ID: "bypass-2", SkillVersionID: &versionID, WasTriggered: false, Outcome: "failure", CreatedAt: cutoff.Add(-2 * time.Minute)},
		SkillLog{ID: "bypass-3", SkillVersionID: &versionID, WasTriggered: false, Outcome: "failure", CreatedAt: cutoff.Add(-3 * time.Minute)},
	)

	telemetry := buildQualityTelemetry(versionID, cutoff, logs)
	if telemetry.Considered != 23 || telemetry.Triggered != 20 || telemetry.OutcomeDenominator != 20 {
		t.Fatalf("telemetry sample = %#v", telemetry)
	}
	if telemetry.Outcomes.Failure != 3 || telemetry.Bypass != 3 {
		t.Fatalf("telemetry counts = %#v", telemetry)
	}
	if len(telemetry.Suggestions) != 2 || telemetry.Suggestions[0].Category != "bypass" || telemetry.Suggestions[1].Category != "failure" {
		t.Fatalf("suggestions = %#v", telemetry.Suggestions)
	}
}

func TestQualityTelemetrySampleGate(t *testing.T) {
	versionID := "version-small"
	cutoff := time.Date(2026, time.July, 22, 3, 0, 0, 0, time.UTC)
	telemetry := buildQualityTelemetry(versionID, cutoff, qualityTestLogs(versionID, cutoff, 16, 3))
	if telemetry.Status != "insufficient" || telemetry.Triggered != 19 || len(telemetry.Suggestions) != 0 {
		t.Fatalf("sample gate = %#v", telemetry)
	}
}

func TestQualityComparisonDetectsStaticRegression(t *testing.T) {
	current := qualityTestPackage(t, "current", "v2", "same")
	baseline := qualityTestPackage(t, "baseline", "v1", "same")
	current.Version.Content = strings.Replace(current.Version.Content, "description: Deterministic quality package", "description: ''", 1)
	current.Files[0].ContentSnapshot = current.Version.Content
	current.Files[0].SHA256 = sha256HexString(current.Version.Content)
	current.Files[0].SizeBytes = int64(len(current.Version.Content))
	current.Version.PackageHash = computePackageHash(current.Files)
	artifact, err := CompileSkillVersion(current.Version, current.Files, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("compile current: %v", err)
	}
	current.Artifact = &artifact

	report := EvaluateSkillQuality(QualityEngineInput{Package: current, Baseline: &baseline, TelemetryCutoff: time.Unix(1, 0)})
	if report.Comparison.Status != "static_regression_detected" {
		t.Fatalf("comparison status = %q", report.Comparison.Status)
	}
	if !containsString(report.Comparison.LintRegressions, "description_present") {
		t.Fatalf("lint regressions = %#v", report.Comparison.LintRegressions)
	}
}

func TestQualityDirectBodyOnlyIdentityAndComparison(t *testing.T) {
	baseline := qualityDirectBodyPackage("direct-baseline", "v1", "baseline body")
	current := qualityDirectBodyPackage("direct-current", "v2", "current body")

	report := EvaluateSkillQuality(QualityEngineInput{Package: current, Baseline: &baseline, TelemetryCutoff: time.Unix(1, 0)})
	identity := qualityCheckByID(t, report.Lint, "canonical_package_hash_matches")
	if !identity.Applicable || !identity.Passed || identity.Severity != QualitySeverityBlocker {
		t.Fatalf("direct body identity check = %#v", identity)
	}
	if report.Comparison.Status != "review_required" || !report.Comparison.PackageHashChanged {
		t.Fatalf("direct body comparison = %#v", report.Comparison)
	}
	if len(report.Comparison.FilesAdded)+len(report.Comparison.FilesRemoved)+len(report.Comparison.FilesModified)+len(report.Comparison.RoutingDiffs) != 0 {
		t.Fatalf("direct body comparison should rely on package hash: %#v", report.Comparison)
	}

	current.Version.ContentHash = strings.Repeat("0", 64)
	invalid := EvaluateSkillQuality(QualityEngineInput{Package: current, TelemetryCutoff: time.Unix(1, 0)})
	identity = qualityCheckByID(t, invalid.Lint, "canonical_package_hash_matches")
	if identity.Passed {
		t.Fatalf("direct body identity accepted mismatched content hash: %#v", identity)
	}
}

func TestQualityComparisonDetectsResourceManifestMetadataOnlyChange(t *testing.T) {
	baseline := qualityTestPackage(t, "metadata-baseline", "v1", "same resource")
	current := qualityTestPackage(t, "metadata-current", "v2", "same resource")
	current.Files[1].Kind = "script"
	current.Files[1].MimeType = "application/x-agentmate-test"
	current.Files[1].Indexable = false
	artifact, err := CompileSkillVersion(current.Version, current.Files, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("compile metadata-only current package: %v", err)
	}
	current.Artifact = &artifact

	report := EvaluateSkillQuality(QualityEngineInput{Package: current, Baseline: &baseline, TelemetryCutoff: time.Unix(1, 0)})
	if report.Comparison.Status != "review_required" || !report.Comparison.ResourceManifestChanged {
		t.Fatalf("metadata-only comparison = %#v", report.Comparison)
	}
	if report.Comparison.PackageHashChanged {
		t.Fatalf("metadata-only change altered Phase 1 package identity: %#v", report.Comparison)
	}
	if len(report.Comparison.FilesAdded)+len(report.Comparison.FilesRemoved)+len(report.Comparison.FilesModified)+len(report.Comparison.RoutingDiffs) != 0 {
		t.Fatalf("metadata-only fixture unexpectedly changed files or routing: %#v", report.Comparison)
	}
}

func TestQualitySeverityMappingAndLegacyNormalization(t *testing.T) {
	checks := []struct {
		checkID  string
		severity QualitySeverity
	}{
		{"root_skill_content_exists", QualitySeverityBlocker},
		{"frontmatter_valid", QualitySeverityError},
		{"resource_manifest_consistent", QualitySeverityError},
		{"self_dependency_absent", QualitySeverityError},
		{"description_present", QualitySeverityWarning},
		{"routing_metadata_no_duplicates", QualitySeverityWarning},
		{"compiled_artifact_current", QualitySeverityWarning},
		{"compile_repeatability", QualitySeverityError},
	}
	for _, expected := range checks {
		check := qualityCheck(expected.checkID, true, true, nil)
		if check.Severity != expected.severity {
			t.Fatalf("severity for %s = %q, want %q", expected.checkID, check.Severity, expected.severity)
		}
	}
	legacy := QualityReport{Lint: []QualityCheck{{ID: "canonical_package_hash_matches"}}, Eval: []QualityCheck{{ID: "file_order_invariance"}}}
	normalizeQualityReport(&legacy)
	if legacy.Lint[0].Severity != QualitySeverityBlocker || legacy.Eval[0].Severity != QualitySeverityError {
		t.Fatalf("legacy report severities = %#v / %#v", legacy.Lint, legacy.Eval)
	}
}

func qualityDirectBodyPackage(versionID, version, body string) QualityPackage {
	accountID := "account-quality"
	content := "---\nname: quality-skill\ndescription: Stable routing\ntriggers: [quality check]\ncapabilities: [lint package]\n---\n\n# Instructions\n\n" + body + "\n"
	contentHash := sha256HexString(content)
	return QualityPackage{Version: SkillVersion{ID: versionID, AccountID: &accountID, SkillName: "quality-skill", Version: version, Content: content, ContentHash: contentHash, PackageHash: contentHash}}
}

func TestQualityCheckSelectedResourceBound(t *testing.T) {
	pkg := qualityTestPackage(t, "bounded", "v1", strings.Repeat("x", maxSelectedResourceBytes+1))
	report := EvaluateSkillQuality(QualityEngineInput{Package: pkg, TelemetryCutoff: time.Unix(1, 0)})
	check := qualityCheckByID(t, report.Eval, "selected_resource_isolation_and_bounds")
	if !check.Applicable || check.Passed {
		t.Fatalf("selected resource bound check = %#v", check)
	}
}

func qualityTestPackage(t *testing.T, versionID, label, resourceContent string) QualityPackage {
	t.Helper()
	accountID := "account-quality"
	versionIDCopy := versionID
	content := `---
name: quality-skill
description: Deterministic quality package
triggers: [quality check]
capabilities: [lint package]
constraints: [offline only]
dependencies: [shared-runtime]
---

# Quality test instructions
`
	files := []SkillVersionFile{
		{ID: versionID + "-skill", AccountID: &accountID, VersionID: &versionIDCopy, Path: "SKILL.md", Kind: "instruction", SHA256: sha256HexString(content), SizeBytes: int64(len([]byte(content))), MimeType: "text/markdown", Indexable: true, ContentSnapshot: content},
		{ID: versionID + "-resource", AccountID: &accountID, VersionID: &versionIDCopy, Path: "resources/data.txt", Kind: "document", SHA256: sha256HexString(resourceContent), SizeBytes: int64(len([]byte(resourceContent))), MimeType: "text/plain", Indexable: true, ContentSnapshot: resourceContent},
	}
	version := SkillVersion{ID: versionID, AccountID: &accountID, SourceRevisionID: stringPointer("revision-" + versionID), SkillName: "quality-skill", Version: label, Content: content, ContentHash: sha256HexString(content), PackageHash: computePackageHash(files), PublishedAt: time.Date(2026, time.July, 22, 0, 0, 0, 0, time.UTC)}
	artifact, err := CompileSkillVersion(version, files, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("compile quality test package: %v", err)
	}
	return QualityPackage{Version: version, Files: files, Artifact: &artifact}
}

func qualityTestLogs(versionID string, cutoff time.Time, successes, failures int) []SkillLog {
	logs := make([]SkillLog, 0, successes+failures)
	for index := 0; index < successes; index++ {
		logs = append(logs, SkillLog{ID: "success-" + string(rune('a'+index)), SkillVersionID: &versionID, WasTriggered: true, Outcome: "success", CreatedAt: cutoff.Add(-time.Duration(index+1) * time.Minute)})
	}
	for index := 0; index < failures; index++ {
		logs = append(logs, SkillLog{ID: "failure-" + string(rune('a'+index)), SkillVersionID: &versionID, WasTriggered: true, Outcome: "failure", FailureReason: "failure body", UserCorrection: "correction body", CreatedAt: cutoff.Add(-time.Duration(successes+index+1) * time.Minute)})
	}
	return logs
}

func qualityCheckByID(t *testing.T, checks []QualityCheck, checkID string) QualityCheck {
	t.Helper()
	for _, check := range checks {
		if check.ID == checkID {
			return check
		}
	}
	t.Fatalf("check %q not found", checkID)
	return QualityCheck{}
}

func stringPointer(value string) *string { return &value }

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestQualityMissingFrontmatterIsNotFatal(t *testing.T) {
	pkg := qualityTestPackage(t, "no-frontmatter", "v1", "resource")
	pkg.Version.Content = "# Instructions\n"
	pkg.Files[0].ContentSnapshot = pkg.Version.Content
	pkg.Files[0].SHA256 = sha256HexString(pkg.Version.Content)
	pkg.Files[0].SizeBytes = int64(len([]byte(pkg.Version.Content)))
	pkg.Version.ContentHash = sha256HexString(pkg.Version.Content)
	pkg.Version.PackageHash = computePackageHash(pkg.Files)
	artifact, err := CompileSkillVersion(pkg.Version, pkg.Files, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("compile package without frontmatter: %v", err)
	}
	pkg.Artifact = &artifact

	report := EvaluateSkillQuality(QualityEngineInput{Package: pkg, TelemetryCutoff: time.Unix(1, 0)})
	check := qualityCheckByID(t, report.Lint, "frontmatter_valid")
	if check.Applicable || !check.Passed {
		t.Fatalf("missing frontmatter check = %#v, want non-applicable pass", check)
	}
}

func TestQualityComparisonUsesInMemoryArtifactWhenStoredArtifactIsStale(t *testing.T) {
	current := qualityTestPackage(t, "stale-current", "v2", "current")
	baseline := qualityTestPackage(t, "stale-baseline", "v1", "baseline")
	current.Artifact.InputPackageHash = strings.Repeat("0", 64)

	report := EvaluateSkillQuality(QualityEngineInput{Package: current, Baseline: &baseline, TelemetryCutoff: time.Unix(1, 0)})
	if report.Comparison.Status != "static_regression_detected" {
		t.Fatalf("comparison status = %q, want static_regression_detected", report.Comparison.Status)
	}
	artifactCheck := qualityCheckByID(t, report.Lint, "compiled_artifact_current")
	if artifactCheck.Passed || artifactCheck.Severity != QualitySeverityWarning {
		t.Fatalf("stale artifact check = %#v", artifactCheck)
	}
}

func TestQualityTelemetryUsesLatestTwoHundred(t *testing.T) {
	versionID := "latest-200"
	cutoff := time.Date(2026, time.July, 22, 4, 0, 0, 0, time.UTC)
	logs := make([]SkillLog, 0, 205)
	for index := 0; index < 205; index++ {
		outcome := "success"
		if index >= 200 {
			outcome = "failure"
		}
		logs = append(logs, SkillLog{
			ID:             fmt.Sprintf("log-%03d", index),
			SkillVersionID: &versionID,
			WasTriggered:   true,
			Outcome:        outcome,
			CreatedAt:      cutoff.Add(-time.Duration(index+1) * time.Minute),
		})
	}

	telemetry := buildQualityTelemetry(versionID, cutoff, logs)
	if telemetry.Considered != maxTelemetryLogs || telemetry.Triggered != maxTelemetryLogs {
		t.Fatalf("telemetry sample = %#v", telemetry)
	}
	if telemetry.Outcomes.Failure != 0 || telemetry.Outcomes.Success != maxTelemetryLogs {
		t.Fatalf("latest sample outcomes = %#v", telemetry.Outcomes)
	}
}
