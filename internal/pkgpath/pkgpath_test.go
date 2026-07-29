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

// TestSourceNameHasKnownCollisions pins two specific collisions as known
// behaviour rather than leaving them to be discovered again.
//
// The encoding is lossy: a separator inside a segment is indistinguishable from a
// path boundary. This test does not claim the encoding is otherwise well-behaved —
// two passing cases prove nothing about the rest of the input space. What it
// protects is name stability: knowledge_sources is keyed by (account_id, name), so
// a change that makes either of these pairs produce different names diverges from
// the names already persisted. Existing rows are not rewritten by such a change;
// they simply stop matching what the code now derives, which is worse than being
// renamed because nothing announces it. That may still be the right change, but it
// has to arrive with a decision about those rows rather than as a side effect.
func TestSourceNameHasKnownCollisions(t *testing.T) {
	for _, collision := range [][2]string{
		// A domain-organised package and a flat package named after it.
		{"platform/retrieval", "platform-retrieval"},
		// The separator inside a segment moves without changing the name.
		{"a/b-c", "a-b/c"},
	} {
		first := SourceName(collision[0])
		second := SourceName(collision[1])
		if first != second {
			t.Errorf("SourceName(%q)=%q and SourceName(%q)=%q no longer collide. "+
				"knowledge_sources rows persisted under the old name will no longer match what this derives, "+
				"so decide how to reconcile them before removing this case.",
				collision[0], first, collision[1], second)
		}
	}
}
