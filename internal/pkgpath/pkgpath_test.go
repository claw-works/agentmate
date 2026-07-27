package pkgpath

import (
	"strings"
	"testing"
)

func TestSourceNameJoinsEverySegment(t *testing.T) {
	cases := []struct {
		name        string
		packagePath string
		want        string
	}{
		{"flat path unchanged", "agentmate-platform", "agentmate-platform"},
		{"domain qualified", "platform/retrieval", "platform-retrieval"},
		{"three segments", "platform/registry/skills", "platform-registry-skills"},
		{"surrounding slashes trimmed", "/platform/retrieval/", "platform-retrieval"},
		{"backslashes normalized", `platform\retrieval`, "platform-retrieval"},
		{"empty path", "", ""},
		{"dot path", ".", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SourceName(tc.packagePath); got != tc.want {
				t.Fatalf("SourceName(%q) = %q, want %q", tc.packagePath, got, tc.want)
			}
		})
	}
}

// The collision this guards against is the reason SourceName exists: knowledge
// sources are unique per (account_id, name), so two domains owning a package
// with the same leaf name silently overwrote each other.
func TestSourceNameDistinguishesSharedLeafNames(t *testing.T) {
	platform := SourceName("platform/retrieval")
	product := SourceName("product/retrieval")
	if platform == product {
		t.Fatalf("expected distinct source names, both resolved to %q", platform)
	}
}

func TestDomainIsFirstSegmentOnlyWhenQualified(t *testing.T) {
	cases := []struct {
		name        string
		packagePath string
		want        string
	}{
		{"domain qualified", "platform/retrieval", "platform"},
		{"deep path keeps first segment", "platform/registry/skills", "platform"},
		{"flat path has no domain", "grounded-answer", ""},
		{"empty path has no domain", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Domain(tc.packagePath); got != tc.want {
				t.Fatalf("Domain(%q) = %q, want %q", tc.packagePath, got, tc.want)
			}
		})
	}
}

func TestSourceNameContainsDomainPrefix(t *testing.T) {
	const packagePath = "platform/retrieval"
	name := SourceName(packagePath)
	domain := Domain(packagePath)
	if !strings.HasPrefix(name, domain+Separator) {
		t.Fatalf("source name %q should start with domain %q", name, domain)
	}
}
