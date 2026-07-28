package contextpack

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// allocate splits the total budget across the requested layers by their default
// shares, renormalised over just those layers so disabling a layer hands its
// share to the rest rather than wasting it.
//
// Within a pack the per-layer budget is then fixed: leftover budget in one layer
// is NOT lent to another. Lending would make the result depend on assembly
// order, so the same request could return different packs after an unrelated
// change to how early a layer runs. A predictable pack is worth more than a
// marginally fuller one.
func allocate(maxChars int, layers []string) map[string]int {
	if len(layers) == 0 {
		return map[string]int{}
	}
	total := 0.0
	for _, layer := range layers {
		total += defaultLayerShare[layer]
	}
	budgets := make(map[string]int, len(layers))
	if total <= 0 {
		// Unknown layers only: split evenly rather than dividing by zero.
		even := maxChars / len(layers)
		for _, layer := range layers {
			budgets[layer] = even
		}
		return budgets
	}
	assigned := 0
	for _, layer := range layers {
		budget := int(float64(maxChars) * defaultLayerShare[layer] / total)
		budgets[layer] = budget
		assigned += budget
	}
	// Integer division loses a few characters; give the remainder to the largest
	// share so the total is spent exactly.
	if remainder := maxChars - assigned; remainder > 0 {
		largest := layers[0]
		for _, layer := range layers {
			if defaultLayerShare[layer] > defaultLayerShare[largest] {
				largest = layer
			}
		}
		budgets[largest] += remainder
	}
	return budgets
}

// minItemChars is the shortest content worth including. Below this an item is
// dropped rather than truncated: a 40-character fragment of a chunk carries no
// evidence and still costs the model attention.
const minItemChars = 80

// fit packs items into a character budget in the order given, which is the
// order the provider ranked them. It truncates the first item that does not fit
// when the remaining budget can still hold something meaningful, and drops the
// rest.
//
// Truncation happens at the last paragraph or sentence boundary before the
// limit so an item does not end mid-word. The item is flagged either way.
func fit(items []Item, budget int) (kept []Item, used int, dropped int, truncated int) {
	kept = make([]Item, 0, len(items))
	for index, item := range items {
		length := utf8.RuneCountInString(item.Content)
		remaining := budget - used
		switch {
		case remaining <= 0:
			return kept, used, len(items) - index, truncated
		case length <= remaining:
			kept = append(kept, item)
			used += length
		case remaining >= minItemChars:
			item.Content = truncateAtBoundary(item.Content, remaining)
			item.Truncated = true
			kept = append(kept, item)
			used += utf8.RuneCountInString(item.Content)
			truncated++
			return kept, used, len(items) - index - 1, truncated
		default:
			return kept, used, len(items) - index, truncated
		}
	}
	return kept, used, 0, truncated
}

// truncateAtBoundary cuts text to at most limit runes, preferring a paragraph
// break, then a sentence end, then a whitespace boundary. Falls back to a hard
// rune cut so the budget is always respected.
func truncateAtBoundary(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	window := string(runes[:limit])
	// Only accept a boundary that keeps a useful majority of the window;
	// otherwise a boundary near the start would throw away most of the content.
	floor := limit / 2
	for _, separator := range []string{"\n\n", "。", ". ", "\n", " "} {
		if index := strings.LastIndex(window, separator); index > 0 {
			candidate := window[:index+len(separator)]
			if utf8.RuneCountInString(candidate) >= floor {
				return strings.TrimRight(candidate, " \n")
			}
		}
	}
	return window
}

// resolveLayers validates and orders the requested layers. An empty request
// means every layer, in canonical order.
func resolveLayers(requested []string) ([]string, error) {
	if len(requested) == 0 {
		return append([]string(nil), LayerOrder...), nil
	}
	valid := make(map[string]struct{}, len(LayerOrder))
	for _, layer := range LayerOrder {
		valid[layer] = struct{}{}
	}
	seen := make(map[string]struct{}, len(requested))
	resolved := make([]string, 0, len(requested))
	for _, layer := range requested {
		normalized := strings.ToUpper(strings.TrimSpace(layer))
		if _, ok := valid[normalized]; !ok {
			return nil, invalidInputf("unknown layer %q", layer)
		}
		if _, duplicate := seen[normalized]; duplicate {
			continue
		}
		seen[normalized] = struct{}{}
		resolved = append(resolved, normalized)
	}
	if len(resolved) == 0 {
		return nil, invalidInputf("layers must not be empty")
	}
	// Render order is canonical regardless of the order the caller listed them,
	// so two callers asking for the same layers get identical packs.
	position := make(map[string]int, len(LayerOrder))
	for index, layer := range LayerOrder {
		position[layer] = index
	}
	sort.Slice(resolved, func(i, j int) bool {
		return position[resolved[i]] < position[resolved[j]]
	})
	return resolved, nil
}
