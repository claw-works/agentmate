package knowledge

import (
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/goccy/go-yaml"
)

// ManifestFileName is the required root manifest of every knowledge package.
const ManifestFileName = "KNOWLEDGE.yaml"

const (
	maxManifestBytes          = 64 * 1024
	maxManifestNameRunes      = 100
	maxDescriptionRunes       = 2000
	maxProfileRunes           = 100
	maxLanguageRunes          = 50
	maxGlobListItems          = 64
	maxGlobItemRunes          = 500
	citationPolicyRequired    = "required"
	citationPolicyOptional    = "optional"
	citationPolicyUnspecified = ""
)

// Manifest is the parsed root KNOWLEDGE.yaml. It describes package identity
// metadata and the include/exclude document selection rules.
type Manifest struct {
	Name           string   `json:"name" yaml:"name"`
	Description    string   `json:"description,omitempty" yaml:"description"`
	Profile        string   `json:"profile,omitempty" yaml:"profile"`
	Language       string   `json:"language,omitempty" yaml:"language"`
	Include        []string `json:"include,omitempty" yaml:"include"`
	Exclude        []string `json:"exclude,omitempty" yaml:"exclude"`
	CitationPolicy string   `json:"citation_policy,omitempty" yaml:"citation_policy"`
}

// ParseManifest parses and validates a root KNOWLEDGE.yaml document.
// go.mod already carries goccy/go-yaml, so no new YAML dependency is
// introduced; the input is size-bounded before parsing.
func ParseManifest(content string) (Manifest, error) {
	var manifest Manifest
	if strings.TrimSpace(content) == "" {
		return manifest, fmt.Errorf("%s is empty", ManifestFileName)
	}
	if len(content) > maxManifestBytes {
		return manifest, fmt.Errorf("%s exceeds %d bytes", ManifestFileName, maxManifestBytes)
	}
	if err := yaml.Unmarshal([]byte(content), &manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse %s: %w", ManifestFileName, err)
	}
	manifest.Name = strings.TrimSpace(manifest.Name)
	manifest.Description = strings.TrimSpace(manifest.Description)
	manifest.Profile = strings.TrimSpace(manifest.Profile)
	manifest.Language = strings.TrimSpace(manifest.Language)
	manifest.CitationPolicy = strings.TrimSpace(strings.ToLower(manifest.CitationPolicy))
	manifest.Include = trimStrings(manifest.Include)
	manifest.Exclude = trimStrings(manifest.Exclude)
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func validateManifest(manifest Manifest) error {
	if manifest.Name == "" {
		return fmt.Errorf("%s name is required", ManifestFileName)
	}
	if utf8.RuneCountInString(manifest.Name) > maxManifestNameRunes {
		return fmt.Errorf("name exceeds %d Unicode code points", maxManifestNameRunes)
	}
	if utf8.RuneCountInString(manifest.Description) > maxDescriptionRunes {
		return fmt.Errorf("description exceeds %d Unicode code points", maxDescriptionRunes)
	}
	if utf8.RuneCountInString(manifest.Profile) > maxProfileRunes {
		return fmt.Errorf("profile exceeds %d Unicode code points", maxProfileRunes)
	}
	if utf8.RuneCountInString(manifest.Language) > maxLanguageRunes {
		return fmt.Errorf("language exceeds %d Unicode code points", maxLanguageRunes)
	}
	switch manifest.CitationPolicy {
	case citationPolicyUnspecified, citationPolicyRequired, citationPolicyOptional:
	default:
		return fmt.Errorf("citation_policy must be required or optional")
	}
	for _, group := range []struct {
		name   string
		values []string
	}{
		{name: "include", values: manifest.Include},
		{name: "exclude", values: manifest.Exclude},
	} {
		if len(group.values) > maxGlobListItems {
			return fmt.Errorf("%s has more than %d items", group.name, maxGlobListItems)
		}
		for index, pattern := range group.values {
			if pattern == "" {
				return fmt.Errorf("%s item %d is empty", group.name, index+1)
			}
			if utf8.RuneCountInString(pattern) > maxGlobItemRunes {
				return fmt.Errorf("%s item %d exceeds %d Unicode code points", group.name, index+1, maxGlobItemRunes)
			}
			if err := validateGlobPattern(pattern); err != nil {
				return fmt.Errorf("%s item %d: %w", group.name, index+1, err)
			}
		}
	}
	return nil
}

// SelectsDocument reports whether a relative package file path passes the
// manifest include/exclude rules. An empty include list selects every file.
// The manifest file itself is never a selectable document.
func (m Manifest) SelectsDocument(filePath string) bool {
	if filePath == ManifestFileName {
		return false
	}
	if len(m.Include) > 0 {
		included := false
		for _, pattern := range m.Include {
			if matchGlob(pattern, filePath) {
				included = true
				break
			}
		}
		if !included {
			return false
		}
	}
	for _, pattern := range m.Exclude {
		if matchGlob(pattern, filePath) {
			return false
		}
	}
	return true
}

func validateGlobPattern(pattern string) error {
	if strings.HasPrefix(pattern, "/") {
		return fmt.Errorf("glob must be relative")
	}
	for _, segment := range strings.Split(pattern, "/") {
		if segment == "**" {
			continue
		}
		if _, err := path.Match(segment, "probe"); err != nil {
			return fmt.Errorf("invalid glob pattern %q", pattern)
		}
	}
	return nil
}

// matchGlob matches slash-separated glob patterns against relative paths.
// "**" matches zero or more path segments; other segments use path.Match
// semantics. Matching uses iterative dynamic programming so tenant-supplied
// patterns with many "**" segments stay O(len(pattern) * len(path)) instead
// of backtracking exponentially.
func matchGlob(pattern, name string) bool {
	patternSegments := strings.Split(pattern, "/")
	nameSegments := strings.Split(name, "/")

	// reachable[j] reports whether the first j name segments can be consumed
	// by the pattern segments processed so far.
	reachable := make([]bool, len(nameSegments)+1)
	next := make([]bool, len(nameSegments)+1)
	reachable[0] = true

	for patternIndex, segment := range patternSegments {
		for j := range next {
			next[j] = false
		}
		if segment == "**" {
			if patternIndex == len(patternSegments)-1 {
				// A trailing "**" selects entries under the prefix, not the
				// prefix itself: it must consume at least one segment.
				for j := 0; j < len(nameSegments); j++ {
					if reachable[j] {
						return true
					}
				}
				return false
			}
			// "**" consumes zero or more segments: propagate reachability
			// forward across every suffix position.
			carry := false
			for j := 0; j <= len(nameSegments); j++ {
				carry = carry || reachable[j]
				next[j] = carry
			}
		} else {
			for j := 0; j < len(nameSegments); j++ {
				if !reachable[j] {
					continue
				}
				matched, err := path.Match(segment, nameSegments[j])
				if err == nil && matched {
					next[j+1] = true
				}
			}
		}
		reachable, next = next, reachable
	}
	return reachable[len(nameSegments)]
}

func trimStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, strings.TrimSpace(value))
	}
	return result
}
