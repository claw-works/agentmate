package contextpack

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestAllocateSpendsTheWholeBudget(t *testing.T) {
	budgets := allocate(10000, LayerOrder)
	total := 0
	for _, budget := range budgets {
		total += budget
	}
	if total != 10000 {
		t.Fatalf("allocated %d of 10000", total)
	}
}

// Disabling a layer must hand its share to the remaining layers rather than
// leaving the budget unspent.
func TestAllocateRenormalisesOverRequestedLayers(t *testing.T) {
	budgets := allocate(10000, []string{LayerSkill, LayerKnowledge})
	total := budgets[LayerSkill] + budgets[LayerKnowledge]
	if total != 10000 {
		t.Fatalf("two-layer allocation = %d, want 10000 (%#v)", total, budgets)
	}
	if budgets[LayerKnowledge] <= budgets[LayerSkill] {
		t.Fatalf("knowledge should keep the larger share: %#v", budgets)
	}
}

func TestAllocateHandlesUnknownLayersWithoutDividingByZero(t *testing.T) {
	budgets := allocate(900, []string{"MYSTERY", "OTHER"})
	if budgets["MYSTERY"] != 450 || budgets["OTHER"] != 450 {
		t.Fatalf("even split expected, got %#v", budgets)
	}
}

func TestFitKeepsItemsThatFitAndDropsTheRest(t *testing.T) {
	items := []Item{
		{Content: strings.Repeat("a", 100)},
		{Content: strings.Repeat("b", 100)},
		{Content: strings.Repeat("c", 100)},
	}
	kept, used, dropped, truncated := fit(items, 210)
	if len(kept) != 2 || used != 200 || dropped != 1 || truncated != 0 {
		t.Fatalf("kept=%d used=%d dropped=%d truncated=%d", len(kept), used, dropped, truncated)
	}
}

// A remaining budget large enough to carry evidence should truncate rather than
// drop, so the layer uses what it was given.
func TestFitTruncatesWhenRemainingBudgetIsUseful(t *testing.T) {
	items := []Item{
		{Content: strings.Repeat("a", 100)},
		{Content: strings.Repeat("b", 500)},
	}
	kept, used, dropped, truncated := fit(items, 300)
	if len(kept) != 2 || truncated != 1 || dropped != 0 {
		t.Fatalf("kept=%d truncated=%d dropped=%d", len(kept), truncated, dropped)
	}
	if !kept[1].Truncated {
		t.Fatal("truncated item must be flagged")
	}
	if used > 300 {
		t.Fatalf("used %d exceeds the budget", used)
	}
}

// Below the minimum an item carries no evidence, so it is dropped instead of
// being cut into a useless fragment.
func TestFitDropsRatherThanEmitTinyFragment(t *testing.T) {
	items := []Item{
		{Content: strings.Repeat("a", 100)},
		{Content: strings.Repeat("b", 500)},
	}
	kept, used, dropped, truncated := fit(items, 140)
	if len(kept) != 1 || dropped != 1 || truncated != 0 {
		t.Fatalf("kept=%d dropped=%d truncated=%d", len(kept), dropped, truncated)
	}
	if used != 100 {
		t.Fatalf("used = %d, want 100", used)
	}
}

func TestTruncateAtBoundaryPrefersParagraphThenSentence(t *testing.T) {
	text := "第一段内容在这里。\n\n第二段内容也在这里，并且更长一些。"
	got := truncateAtBoundary(text, 16)
	if utf8.RuneCountInString(got) > 16 {
		t.Fatalf("result exceeds the limit: %q", got)
	}
	if strings.Contains(got, "第二段") {
		t.Fatalf("cut should land before the second paragraph: %q", got)
	}
}

// A boundary very close to the start would discard most of the window, so the
// hard cut is preferred in that case.
func TestTruncateAtBoundaryIgnoresBoundaryNearTheStart(t *testing.T) {
	text := "a\n" + strings.Repeat("b", 400)
	got := truncateAtBoundary(text, 100)
	if utf8.RuneCountInString(got) != 100 {
		t.Fatalf("expected a hard cut at 100 runes, got %d", utf8.RuneCountInString(got))
	}
}

func TestTruncateAtBoundaryCountsRunesNotBytes(t *testing.T) {
	text := strings.Repeat("知", 200)
	got := truncateAtBoundary(text, 50)
	if utf8.RuneCountInString(got) != 50 {
		t.Fatalf("rune count = %d, want 50", utf8.RuneCountInString(got))
	}
}

func TestResolveLayersDefaultsToCanonicalOrder(t *testing.T) {
	layers, err := resolveLayers(nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if strings.Join(layers, ",") != strings.Join(LayerOrder, ",") {
		t.Fatalf("layers = %v", layers)
	}
}

// Two callers requesting the same layers in different orders must get identical
// packs, so the order is canonicalised rather than taken from the request.
func TestResolveLayersCanonicalisesOrderAndDeduplicates(t *testing.T) {
	layers, err := resolveLayers([]string{"memory", "SKILL", "Memory"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if strings.Join(layers, ",") != "SKILL,MEMORY" {
		t.Fatalf("layers = %v, want [SKILL MEMORY]", layers)
	}
}

func TestResolveLayersRejectsUnknownLayer(t *testing.T) {
	if _, err := resolveLayers([]string{"SKILL", "WISHES"}); err == nil {
		t.Fatal("expected an unknown layer to be rejected")
	}
}
