package skills

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	SkillCompilerName         = "agentmate-skill-compiler"
	SkillCompilerVersion      = "1.0.0"
	maxSkillNameRunes         = 100
	maxDescriptionRunes       = 2000
	maxChangeSummaryRunes     = 2000
	maxMetadataListItems      = 64
	maxMetadataItemRunes      = 500
	maxMetadataAggregateRunes = 16000
	maxResourcePathRunes      = 1024
	maxResourceKindRunes      = 64
	maxResourceMimeRunes      = 255
	maxManifestMetadataBytes  = 512 * 1024
	maxCatalogIndexRunes      = 32768
)

type skillFrontmatter struct {
	Name         string
	Description  string
	Triggers     []string
	Capabilities []string
	Constraints  []string
	Dependencies []string
	// Knowledge is parsed separately by a real YAML parser: the scanner below is flat and
	// would silently drop a nested block.
	Knowledge *KnowledgeContract
}

func CompileSkillVersion(version SkillVersion, files []SkillVersionFile, compiledAt time.Time) (CompiledSkillCatalog, error) {
	metadata, err := parseSkillFrontmatter(version.Content)
	if err != nil {
		return CompiledSkillCatalog{}, err
	}
	if err := validateSkillFrontmatter(metadata); err != nil {
		return CompiledSkillCatalog{}, err
	}
	// A malformed contract fails the compile rather than being dropped. Discovery, budgets
	// and authorisation all read this block, so a Skill that ships with an unparseable one
	// would look like a Skill that needs no knowledge — and would then quietly answer from
	// nothing.
	if err := ValidateKnowledgeContract(metadata.Knowledge); err != nil {
		return CompiledSkillCatalog{}, fmt.Errorf("knowledge contract: %w", err)
	}
	if err := validateCompiledFiles(files); err != nil {
		return CompiledSkillCatalog{}, err
	}
	name := strings.TrimSpace(version.SkillName)
	if name == "" {
		return CompiledSkillCatalog{}, fmt.Errorf("skill name required")
	}
	if utf8.RuneCountInString(name) > maxSkillNameRunes {
		return CompiledSkillCatalog{}, fmt.Errorf("skill name exceeds %d Unicode code points", maxSkillNameRunes)
	}
	packageHash := strings.ToLower(strings.TrimSpace(version.PackageHash))
	if !isSHA256Hex(packageHash) {
		return CompiledSkillCatalog{}, fmt.Errorf("invalid input package hash")
	}

	return CompiledSkillCatalog{
		AccountID:         valueOrEmpty(version.AccountID),
		SkillVersionID:    version.ID,
		SourceID:          version.SourceID,
		SkillName:         name,
		Version:           version.Version,
		CompilerName:      SkillCompilerName,
		CompilerVersion:   SkillCompilerVersion,
		InputPackageHash:  packageHash,
		Description:       metadata.Description,
		Triggers:          nonNilStrings(metadata.Triggers),
		Capabilities:      nonNilStrings(metadata.Capabilities),
		Constraints:       nonNilStrings(metadata.Constraints),
		Dependencies:      nonNilStrings(metadata.Dependencies),
		ResourceManifest:  resourceManifestFromFiles(files),
		KnowledgeContract: metadata.Knowledge,
		KnowledgeContractIdentity: func() string {
			if metadata.Knowledge == nil {
				return ""
			}
			return ContractIdentity(metadata.Knowledge)
		}(),
		CompiledAt:  compiledAt.UTC(),
		PublishedAt: version.PublishedAt,
	}, nil
}

func parseSkillFrontmatter(content string) (skillFrontmatter, error) {
	var result skillFrontmatter
	contract, contractErr := ParseKnowledgeContract(content)
	if contractErr != nil {
		return result, contractErr
	}
	result.Knowledge = contract
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return result, nil
	}

	listValues := map[string]*[]string{
		"triggers":     &result.Triggers,
		"capabilities": &result.Capabilities,
		"constraints":  &result.Constraints,
		"dependencies": &result.Dependencies,
	}
	activeList := ""
	ignoreUnknownList := false
	closed := false
	for index := 1; index < len(lines); index++ {
		rawLine := lines[index]
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "---" {
			closed = true
			break
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "-") {
			if activeList == "" && ignoreUnknownList {
				continue
			}
			if activeList == "" {
				return result, fmt.Errorf("frontmatter list item without a supported key")
			}
			value := cleanYAMLString(strings.TrimSpace(strings.TrimPrefix(trimmed, "-")))
			if value != "" {
				*listValues[activeList] = append(*listValues[activeList], value)
			}
			continue
		}

		key, rawValue, ok := strings.Cut(trimmed, ":")
		if !ok {
			activeList = ""
			ignoreUnknownList = false
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		rawValue = strings.TrimSpace(rawValue)
		activeList = ""
		ignoreUnknownList = false
		switch key {
		case "name":
			result.Name = cleanYAMLString(rawValue)
		case "description":
			if rawValue == "|" || rawValue == ">" {
				block, next := readIndentedBlock(lines, index+1, leadingWhitespace(rawLine))
				index = next - 1
				if rawValue == ">" {
					result.Description = foldYAMLBlock(block)
				} else {
					result.Description = strings.TrimSpace(strings.Join(block, "\n"))
				}
			} else {
				result.Description = cleanYAMLString(rawValue)
			}
		case "triggers", "capabilities", "constraints", "dependencies":
			activeList = key
			values, err := parseYAMLStringList(rawValue)
			if err != nil {
				return result, fmt.Errorf("frontmatter %s: %w", key, err)
			}
			*listValues[key] = append(*listValues[key], values...)
		default:
			ignoreUnknownList = rawValue == ""
		}
	}
	if !closed {
		return result, fmt.Errorf("unterminated frontmatter")
	}
	return result, nil
}

func validateSkillFrontmatter(metadata skillFrontmatter) error {
	nameRunes := utf8.RuneCountInString(metadata.Name)
	if nameRunes > maxSkillNameRunes {
		return fmt.Errorf("frontmatter name exceeds %d Unicode code points", maxSkillNameRunes)
	}
	descriptionRunes := utf8.RuneCountInString(metadata.Description)
	if descriptionRunes > maxDescriptionRunes {
		return fmt.Errorf("frontmatter description exceeds %d Unicode code points", maxDescriptionRunes)
	}
	totalRunes := nameRunes + descriptionRunes
	for _, group := range []struct {
		name   string
		values []string
	}{
		{name: "triggers", values: metadata.Triggers},
		{name: "capabilities", values: metadata.Capabilities},
		{name: "constraints", values: metadata.Constraints},
		{name: "dependencies", values: metadata.Dependencies},
	} {
		if len(group.values) > maxMetadataListItems {
			return fmt.Errorf("frontmatter %s has more than %d items", group.name, maxMetadataListItems)
		}
		for index, value := range group.values {
			itemRunes := utf8.RuneCountInString(value)
			if itemRunes > maxMetadataItemRunes {
				return fmt.Errorf("frontmatter %s item %d exceeds %d Unicode code points", group.name, index+1, maxMetadataItemRunes)
			}
			totalRunes += itemRunes
		}
	}
	if totalRunes > maxMetadataAggregateRunes {
		return fmt.Errorf("frontmatter routing metadata exceeds %d Unicode code points", maxMetadataAggregateRunes)
	}
	return nil
}

func validateCompiledFiles(files []SkillVersionFile) error {
	totalBytes := 0
	for _, file := range files {
		if file.Path == "SKILL.md" {
			continue
		}
		if file.Path == "" || utf8.RuneCountInString(file.Path) > maxResourcePathRunes {
			return fmt.Errorf("resource path must be between 1 and %d Unicode code points", maxResourcePathRunes)
		}
		if utf8.RuneCountInString(file.Kind) > maxResourceKindRunes {
			return fmt.Errorf("resource kind for %s exceeds %d Unicode code points", file.Path, maxResourceKindRunes)
		}
		if utf8.RuneCountInString(file.MimeType) > maxResourceMimeRunes {
			return fmt.Errorf("resource mime type for %s exceeds %d Unicode code points", file.Path, maxResourceMimeRunes)
		}
		totalBytes += len(file.Path) + len(file.Kind) + len(file.MimeType) + len(file.SHA256)
		if totalBytes > maxManifestMetadataBytes {
			return fmt.Errorf("resource manifest metadata exceeds %d bytes", maxManifestMetadataBytes)
		}
	}
	return nil
}

func readIndentedBlock(lines []string, start, parentIndent int) ([]string, int) {
	block := make([]string, 0)
	minimumIndent := -1
	end := start
	for ; end < len(lines); end++ {
		line := lines[end]
		if strings.TrimSpace(line) != "" {
			indent := leadingWhitespace(line)
			if indent <= parentIndent {
				break
			}
			if minimumIndent == -1 || indent < minimumIndent {
				minimumIndent = indent
			}
		}
		block = append(block, line)
	}
	if minimumIndent < 0 {
		return []string{}, end
	}
	for index, line := range block {
		if strings.TrimSpace(line) == "" {
			block[index] = ""
			continue
		}
		if len(line) >= minimumIndent {
			block[index] = line[minimumIndent:]
		}
	}
	return block, end
}

func leadingWhitespace(value string) int {
	for index, character := range value {
		if character != ' ' && character != '\t' {
			return index
		}
	}
	return len(value)
}

func foldYAMLBlock(lines []string) string {
	paragraphs := make([]string, 0)
	current := make([]string, 0)
	flush := func() {
		if len(current) == 0 {
			return
		}
		paragraphs = append(paragraphs, strings.Join(current, " "))
		current = current[:0]
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			flush()
			continue
		}
		current = append(current, trimmed)
	}
	flush()
	return strings.TrimSpace(strings.Join(paragraphs, "\n"))
}

func parseYAMLStringList(raw string) ([]string, error) {
	if raw == "" {
		return []string{}, nil
	}
	if !strings.HasPrefix(raw, "[") || !strings.HasSuffix(raw, "]") {
		return nil, fmt.Errorf("expected a YAML string list")
	}
	inner := strings.TrimSpace(raw[1 : len(raw)-1])
	if inner == "" {
		return []string{}, nil
	}
	parts, err := splitInlineYAMLList(inner)
	if err != nil {
		return nil, err
	}
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		value := cleanYAMLString(strings.TrimSpace(part))
		if value != "" {
			values = append(values, value)
		}
	}
	return values, nil
}

func splitInlineYAMLList(value string) ([]string, error) {
	parts := make([]string, 0)
	start := 0
	var quote byte
	escaped := false
	for index := 0; index < len(value); index++ {
		character := value[index]
		if escaped {
			escaped = false
			continue
		}
		if quote == '"' && character == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if character == quote {
				quote = 0
			}
			continue
		}
		if character == '\'' || character == '"' {
			quote = character
			continue
		}
		if character == ',' {
			parts = append(parts, value[start:index])
			start = index + 1
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quoted string")
	}
	parts = append(parts, value[start:])
	return parts, nil
}

func truncateRunes(value string, maximum int) string {
	if maximum <= 0 || utf8.RuneCountInString(value) <= maximum {
		return value
	}
	runes := []rune(value)
	return string(runes[:maximum])
}

func cleanYAMLString(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		if (value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"') {
			value = value[1 : len(value)-1]
		}
	}
	return strings.TrimSpace(value)
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
