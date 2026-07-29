// Package pkgpath derives registry-level semantics from a package path.
//
// Skill and Knowledge sources both address a package by a relative path inside
// a Git repository. Once packages are organised by domain (for example
// "platform/retrieval"), the path carries two pieces of information that the
// registry needs as first-class data: a source name and the owning domain. Both
// registries must agree on these rules, otherwise domain grouping means
// different things per registry, so the logic lives here instead of being
// duplicated.
package pkgpath

import "strings"

// Separator joins path segments into a source name.
const Separator = "-"

// Segments splits a normalized relative package path into its segments,
// dropping empty ones. A blank path yields nil.
func Segments(packagePath string) []string {
	packagePath = strings.Trim(strings.TrimSpace(strings.ReplaceAll(packagePath, "\\", "/")), "/")
	if packagePath == "" || packagePath == "." {
		return nil
	}
	parts := strings.Split(packagePath, "/")
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		segments = append(segments, part)
	}
	if len(segments) == 0 {
		return nil
	}
	return segments
}

// SourceName joins every path segment so that packages sharing a leaf name
// under different domains stay distinct. "platform/retrieval" and
// "product/retrieval" therefore yield "platform-retrieval" and
// "product-retrieval" rather than colliding on "retrieval". Single-segment
// paths are returned unchanged, keeping flat layouts backward compatible.
//
// The result is NOT a unique identifier, and callers must not treat it as one.
// The encoding is lossy in both directions: "platform/retrieval" and the flat
// package "platform-retrieval" produce the same name, and so do "a/b-c" and
// "a-b/c".
//
// The collision is a deliberate trade-off, not an oversight. Escaping the
// separator would make names unreadable, and names are user-facing: they appear
// in the catalog, in retrieval hits and in agent-visible output. The consequence
// is therefore handled per registry, and the two registries differ because they
// are keyed differently:
//
//   - knowledge_sources is keyed by (account_id, name), so a collision would let
//     a second registration silently repoint the first source at a different
//     repository. That was a real defect, fixed in commit 4c3b80c: the upsert now
//     compares type, repository URL and package path, and rejects a registration
//     whose derived name is already taken by a different package, naming the
//     occupant. Register one of them under an explicit distinct name.
//   - skill_sources is keyed by (account_id, type, repository_url, package_path)
//     and has no uniqueness constraint on name at all, so nothing is overwritten.
//     Two skill sources can legitimately share a derived name, which makes the
//     name ambiguous for a human reading a listing but never loses data.
//
// If skill sources ever gain a name-keyed lookup, that difference stops being
// harmless and the knowledge-side rejection has to be mirrored there.
func SourceName(packagePath string) string {
	return strings.Join(Segments(packagePath), Separator)
}

// Domain returns the owning domain of a package, which is the first path
// segment. A single-segment path has no domain: a flat package is not
// organised by domain, so reporting its own name as the domain would invent a
// grouping that does not exist. Callers treat "" as unclassified.
func Domain(packagePath string) string {
	segments := Segments(packagePath)
	if len(segments) < 2 {
		return ""
	}
	return segments[0]
}
