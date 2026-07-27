// Package pkgpath derives registry-level semantics from a package path.
//
// Skill and Knowledge sources both address a package by a relative path inside
// a Git repository. Once packages are organised by domain (for example
// "platform/retrieval"), the path carries two pieces of information that the
// registry needs as first-class data: a unique source name and the owning
// domain. Both registries must agree on these rules, otherwise domain grouping
// means different things per registry, so the logic lives here instead of being
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
