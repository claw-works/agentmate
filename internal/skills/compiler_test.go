package skills

import (
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestCompileSkillVersionDeterministic(t *testing.T) {
	accountID := "account-1"
	compiledAt := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	version := SkillVersion{
		ID:          "version-1",
		AccountID:   &accountID,
		SkillName:   "fallback-name",
		Version:     "v3",
		PackageHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Content: `---
name: catalog-skill
description: "Stable catalog card"
triggers:
  - first trigger
  - 'second trigger'
capabilities: [compile, "retrieve, inspect"]
constraints:
  - no secrets
dependencies: []
---

# Instructions
`,
	}
	files := []SkillVersionFile{
		{ID: "file-z", Path: "z.txt", Kind: "document", SHA256: "z", SizeBytes: 3, MimeType: "text/plain", Indexable: true, ContentSnapshot: "zzz"},
		{ID: "skill", Path: "SKILL.md", Kind: "instruction", SHA256: "s", SizeBytes: 10, MimeType: "text/markdown", Indexable: true, ContentSnapshot: version.Content},
		{ID: "file-a", Path: "a.txt", Kind: "document", SHA256: "a", SizeBytes: 1, MimeType: "text/plain"},
	}

	first, err := CompileSkillVersion(version, files, compiledAt)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	second, err := CompileSkillVersion(version, []SkillVersionFile{files[2], files[0], files[1]}, compiledAt)
	if err != nil {
		t.Fatalf("compile reordered files: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("compiler output changed with file order:\nfirst=%+v\nsecond=%+v", first, second)
	}
	if first.SkillName != "fallback-name" || first.Description != "Stable catalog card" {
		t.Fatalf("unexpected canonical card: %+v", first)
	}
	if !reflect.DeepEqual(first.Triggers, []string{"first trigger", "second trigger"}) {
		t.Fatalf("triggers = %#v", first.Triggers)
	}
	if !reflect.DeepEqual(first.Capabilities, []string{"compile", "retrieve, inspect"}) {
		t.Fatalf("capabilities = %#v", first.Capabilities)
	}
	if len(first.ResourceManifest) != 2 || first.ResourceManifest[0].Path != "a.txt" || first.ResourceManifest[1].Path != "z.txt" {
		t.Fatalf("manifest = %#v", first.ResourceManifest)
	}
	if first.ResourceManifest[0].TextAvailable || !first.ResourceManifest[1].TextAvailable {
		t.Fatalf("text availability = %#v", first.ResourceManifest)
	}
}

func TestCompileSkillVersionRejectsMalformedList(t *testing.T) {
	version := SkillVersion{
		SkillName:   "bad",
		PackageHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Content:     "---\nname: bad\ntriggers: not-a-list\n---\n",
	}
	if _, err := CompileSkillVersion(version, nil, time.Now()); err == nil {
		t.Fatal("expected malformed list error")
	}
}

func TestCompileSkillVersionAllowsMissingFrontmatter(t *testing.T) {
	version := SkillVersion{
		SkillName:   "legacy",
		PackageHash: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		Content:     "# Legacy instructions",
	}
	compiled, err := CompileSkillVersion(version, nil, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("compile legacy: %v", err)
	}
	if compiled.SkillName != "legacy" || compiled.Description != "" {
		t.Fatalf("compiled legacy = %+v", compiled)
	}
}

func TestCompileSkillVersionSupportsBlockDescriptionAndUnknownLists(t *testing.T) {
	version := SkillVersion{
		SkillName:   "block-description",
		PackageHash: strings.Repeat("d", 64),
		Content: `---
name: block-description
description: >
  First line.
  Second line.
owners:
  - team-a
triggers:
  - run compiler
---
`,
	}
	compiled, err := CompileSkillVersion(version, nil, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("compile block description: %v", err)
	}
	if compiled.Description != "First line. Second line." {
		t.Fatalf("description = %q", compiled.Description)
	}
	if !reflect.DeepEqual(compiled.Triggers, []string{"run compiler"}) {
		t.Fatalf("triggers = %#v", compiled.Triggers)
	}
}

func TestCompileSkillVersionRejectsOversizedMetadata(t *testing.T) {
	version := SkillVersion{
		SkillName:   "oversized",
		PackageHash: strings.Repeat("e", 64),
		Content:     "---\nname: oversized\ndescription: \"" + strings.Repeat("界", maxDescriptionRunes+1) + "\"\n---\n",
	}
	if _, err := CompileSkillVersion(version, nil, time.Now()); err == nil {
		t.Fatal("expected oversized description error")
	}
}

func TestCompileSkillVersionRejectsOversizedNameAndManifestMetadata(t *testing.T) {
	base := SkillVersion{
		SkillName:   "bounded",
		PackageHash: strings.Repeat("f", 64),
	}
	oversizedName := base
	oversizedName.Content = "---\nname: " + strings.Repeat("n", maxSkillNameRunes+1) + "\n---\n"
	if _, err := CompileSkillVersion(oversizedName, nil, time.Now()); err == nil {
		t.Fatal("expected oversized name error")
	}

	base.Content = "---\nname: bounded\n---\n"
	files := []SkillVersionFile{{
		ID:       "oversized-path",
		Path:     strings.Repeat("p", maxResourcePathRunes+1),
		Kind:     "document",
		MimeType: "text/plain",
		SHA256:   strings.Repeat("a", 64),
	}}
	if _, err := CompileSkillVersion(base, files, time.Now()); err == nil {
		t.Fatal("expected oversized resource path error")
	}
}

func TestCompiledCatalogIndexContentIsBounded(t *testing.T) {
	values := make([]string, maxMetadataListItems)
	for index := range values {
		values[index] = strings.Repeat("v", maxMetadataItemRunes)
	}
	content := compiledCatalogIndexContent(SkillCatalogItem{
		SkillName:     "bounded-index",
		Version:       "v1",
		Description:   strings.Repeat("d", maxDescriptionRunes),
		Triggers:      values,
		Capabilities:  values,
		Constraints:   values,
		Dependencies:  values,
		ResourceKinds: values,
	})
	if utf8.RuneCountInString(content) != maxCatalogIndexRunes {
		t.Fatalf("index content runes = %d, want %d", utf8.RuneCountInString(content), maxCatalogIndexRunes)
	}
}
