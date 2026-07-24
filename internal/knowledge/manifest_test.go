package knowledge

import (
	"strings"
	"testing"
	"time"
)

func TestParseManifest(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		content   string
		wantError string
		check     func(t *testing.T, manifest Manifest)
	}{
		{
			name: "full valid manifest",
			content: `name: product-support
description: 产品文档、FAQ 和故障排查知识
profile: linked-wiki-v1
language: zh-CN
include:
  - "raw/**/*.md"
exclude:
  - "raw/drafts/**"
citation_policy: required
`,
			check: func(t *testing.T, manifest Manifest) {
				if manifest.Name != "product-support" {
					t.Fatalf("name = %q", manifest.Name)
				}
				if manifest.Profile != "linked-wiki-v1" || manifest.Language != "zh-CN" {
					t.Fatalf("profile/language = %q/%q", manifest.Profile, manifest.Language)
				}
				if len(manifest.Include) != 1 || manifest.Include[0] != "raw/**/*.md" {
					t.Fatalf("include = %#v", manifest.Include)
				}
				if manifest.CitationPolicy != "required" {
					t.Fatalf("citation_policy = %q", manifest.CitationPolicy)
				}
			},
		},
		{
			name:    "minimal manifest",
			content: "name: minimal\n",
			check: func(t *testing.T, manifest Manifest) {
				if manifest.Name != "minimal" || len(manifest.Include) != 0 || manifest.CitationPolicy != "" {
					t.Fatalf("manifest = %#v", manifest)
				}
			},
		},
		{
			name:      "empty content",
			content:   "   \n",
			wantError: "is empty",
		},
		{
			name:      "missing name",
			content:   "description: no name\n",
			wantError: "name is required",
		},
		{
			name:      "name too long",
			content:   "name: " + strings.Repeat("界", 101) + "\n",
			wantError: "name exceeds 100",
		},
		{
			name:    "name at limit",
			content: "name: " + strings.Repeat("界", 100) + "\n",
		},
		{
			name:      "description too long",
			content:   "name: x\ndescription: " + strings.Repeat("说", 2001) + "\n",
			wantError: "description exceeds 2000",
		},
		{
			name:      "invalid citation policy",
			content:   "name: x\ncitation_policy: mandatory\n",
			wantError: "citation_policy must be required or optional",
		},
		{
			name:    "optional citation policy case-insensitive",
			content: "name: x\ncitation_policy: Optional\n",
			check: func(t *testing.T, manifest Manifest) {
				if manifest.CitationPolicy != "optional" {
					t.Fatalf("citation_policy = %q", manifest.CitationPolicy)
				}
			},
		},
		{
			name:      "too many include items",
			content:   "name: x\ninclude:\n" + strings.Repeat("  - \"a.md\"\n", 65),
			wantError: "include has more than 64 items",
		},
		{
			name:      "include item too long",
			content:   "name: x\ninclude:\n  - \"" + strings.Repeat("a", 501) + "\"\n",
			wantError: "include item 1 exceeds 500",
		},
		{
			name:      "empty exclude item",
			content:   "name: x\nexclude:\n  - \"\"\n",
			wantError: "exclude item 1 is empty",
		},
		{
			name:      "invalid glob pattern",
			content:   "name: x\ninclude:\n  - \"raw/[bad.md\"\n",
			wantError: "invalid glob pattern",
		},
		{
			name:      "absolute glob rejected",
			content:   "name: x\ninclude:\n  - \"/etc/**\"\n",
			wantError: "glob must be relative",
		},
		{
			name:      "not yaml",
			content:   "name: [unclosed\n",
			wantError: "parse KNOWLEDGE.yaml",
		},
		{
			name:      "oversized manifest",
			content:   "name: x\ndescription: ok\n# " + strings.Repeat("a", maxManifestBytes),
			wantError: "exceeds",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			manifest, err := ParseManifest(testCase.content)
			if testCase.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
					t.Fatalf("ParseManifest error = %v, want containing %q", err, testCase.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseManifest error = %v", err)
			}
			if testCase.check != nil {
				testCase.check(t, manifest)
			}
		})
	}
}

func TestManifestSelectsDocument(t *testing.T) {
	manifest := Manifest{
		Include: []string{"raw/**/*.md", "docs/*.txt"},
		Exclude: []string{"raw/drafts/**"},
	}
	for _, testCase := range []struct {
		path string
		want bool
	}{
		{path: "raw/faq.md", want: true},
		{path: "raw/nested/deep/guide.md", want: true},
		{path: "raw/drafts/wip.md", want: false},
		{path: "raw/drafts/nested/wip.md", want: false},
		{path: "docs/notes.txt", want: true},
		{path: "docs/nested/notes.txt", want: false},
		{path: "raw/image.png", want: false},
		{path: "KNOWLEDGE.yaml", want: false},
	} {
		if got := manifest.SelectsDocument(testCase.path); got != testCase.want {
			t.Errorf("SelectsDocument(%q) = %v, want %v", testCase.path, got, testCase.want)
		}
	}

	everything := Manifest{}
	if !everything.SelectsDocument("anything/at/all.bin") {
		t.Error("empty include list must select every file")
	}
	if everything.SelectsDocument("KNOWLEDGE.yaml") {
		t.Error("manifest file must never be selectable as a document")
	}
}

func TestMatchGlob(t *testing.T) {
	for _, testCase := range []struct {
		pattern string
		name    string
		want    bool
	}{
		{pattern: "**", name: "a/b/c.md", want: true},
		{pattern: "**/*.md", name: "c.md", want: true},
		{pattern: "**/*.md", name: "a/b/c.md", want: true},
		{pattern: "raw/**", name: "raw", want: false},
		{pattern: "raw/**", name: "raw/x.md", want: true},
		{pattern: "*.md", name: "a/b.md", want: false},
		{pattern: "a/?.md", name: "a/b.md", want: true},
		{pattern: "**/b", name: "b", want: true},
		{pattern: "**/b/**", name: "a/b/c", want: true},
		{pattern: "**/b/**", name: "a/b", want: false},
		{pattern: "a/**/**/z", name: "a/z", want: true},
		{pattern: "a/**/**/z", name: "a/x/y/z", want: true},
	} {
		if got := matchGlob(testCase.pattern, testCase.name); got != testCase.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", testCase.pattern, testCase.name, got, testCase.want)
		}
	}
}

// TestMatchGlobAdversarialPatternIsLinear guards the dynamic-programming
// matcher against exponential backtracking: a pattern alternating "**" with
// non-matching segments over a long path must finish immediately.
func TestMatchGlobAdversarialPatternIsLinear(t *testing.T) {
	pattern := strings.TrimSuffix(strings.Repeat("**/a/", 80), "/") + "/zzz"
	name := strings.TrimSuffix(strings.Repeat("a/", 200), "/")

	start := time.Now()
	if matchGlob(pattern, name) {
		t.Fatal("adversarial pattern must not match")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("adversarial glob took %v, expected linear-time matching", elapsed)
	}
}
