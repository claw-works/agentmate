// Package contextpack assembles the minimal execution context for an agent
// from the three planes (Skill / Knowledge / Memory) plus live app facts and the
// task statement, in one call.
//
// The point is not to concatenate three searches. Concatenation is something the
// caller can already do, and doing it naively blows the context window. The
// value here is a budget: each layer gets a bounded share, content is truncated
// at a declared boundary, and the response reports exactly what was spent and
// what was dropped. An agent can then trust that the pack fits.
//
// Every item carries a source label and a traceable identifier so the model can
// tell authority apart — a compiled skill instruction is not the same kind of
// claim as a remembered failure — and so a later attribution query can find its
// way back to the origin.
package contextpack

// Layer names double as the render labels, which is why they are upper case.
// The order is the render order and is deliberate: instructions first (how to
// act), then evidence, then experience, then volatile state, then the task.
const (
	LayerSkill     = "SKILL"
	LayerKnowledge = "KNOWLEDGE"
	LayerMemory    = "MEMORY"
	LayerFacts     = "FACTS"
	LayerTask      = "TASK"
)

// LayerOrder is the canonical assembly and render order.
var LayerOrder = []string{LayerSkill, LayerKnowledge, LayerMemory, LayerFacts, LayerTask}

// Budgets are measured in characters, not tokens.
//
// Token counting requires a model-specific tokenizer: the same text costs
// differently across providers, and embedding a tokenizer would tie the server
// to one vendor's version. Characters are exact, stable and explainable, and the
// caller can apply its own chars-per-token ratio. The tradeoff is that a
// character budget is a proxy — it is documented rather than hidden.
const (
	DefaultMaxChars = 12000
	MinMaxChars     = 500
	MaxMaxChars     = 200000
)

// Default per-layer shares of the total budget. Skill and knowledge dominate
// because instructions and evidence are what actually drive execution; facts and
// task are small by nature.
var defaultLayerShare = map[string]float64{
	LayerSkill:     0.30,
	LayerKnowledge: 0.35,
	LayerMemory:    0.20,
	LayerFacts:     0.10,
	LayerTask:      0.05,
}

type PackRequest struct {
	// Task is the goal statement. It is also the default retrieval query for
	// every layer, so a vague task yields a vague pack.
	Task string `json:"task" binding:"required"`
	// SkillName pins the skill instead of selecting one by retrieval. Dynamic
	// discovery driven by a Skill's knowledge contract is K4; until then the
	// caller either names the skill or accepts the top retrieval hit.
	SkillName string `json:"skill_name"`
	// Query overrides the retrieval query when the task statement is a poor
	// search string (long, or written as an imperative).
	Query string `json:"query"`
	// SessionID scopes the task layer's recent-activity slice and is recorded
	// for attribution.
	SessionID string `json:"session_id"`
	// KnowledgeDomain and KnowledgeSourceIDs narrow evidence retrieval. Both
	// compose by intersection, never widening.
	KnowledgeDomain    string   `json:"knowledge_domain"`
	KnowledgeSourceIDs []string `json:"knowledge_source_ids"`
	MemoryScopeType    string   `json:"memory_scope_type"`
	MemoryScopeKey     string   `json:"memory_scope_key"`

	MaxChars int `json:"max_chars"`
	// Layers restricts assembly to the named layers. Empty means all of them.
	Layers []string `json:"layers"`
	// TopK caps items per retrieval-backed layer before budgeting.
	TopK int `json:"top_k"`
	// Render additionally returns the labelled plain-text form ready to paste
	// into a prompt.
	Render bool `json:"render"`
}

// Item is one piece of context. Content is what the model reads; the remaining
// fields exist so a claim can be traced and weighed.
type Item struct {
	Layer string `json:"layer"`
	// Source is the concrete origin kind, for example "skill_instructions",
	// "knowledge_chunk", "memory_entry", "todo", "note", "memory_event".
	Source string `json:"source"`
	// Ref is the traceable identifier within that source kind.
	Ref     string `json:"ref,omitempty"`
	Title   string `json:"title,omitempty"`
	Content string `json:"content"`
	// Citation locates the claim inside its origin, for example a document path
	// plus heading path. Empty when the source is not citable.
	Citation string   `json:"citation,omitempty"`
	Score    *float64 `json:"score,omitempty"`
	// Truncated marks an item whose content was cut to fit the layer budget.
	// Surfacing this per item matters: a truncated instruction can change
	// behaviour, and the agent cannot notice the cut on its own.
	Truncated bool `json:"truncated,omitempty"`
}

// LayerResult reports one layer's outcome, including what it could not include.
type LayerResult struct {
	Layer      string `json:"layer"`
	Items      []Item `json:"items"`
	CharBudget int    `json:"char_budget"`
	CharsUsed  int    `json:"chars_used"`
	// Dropped counts items retrieved but excluded for lack of budget.
	Dropped int `json:"dropped"`
	// Truncated counts included items whose content was cut.
	Truncated int `json:"truncated"`
	// Note explains a degraded or empty layer instead of leaving the caller to
	// guess whether there was nothing to find or nothing wired up.
	Note string `json:"note,omitempty"`
}

type PackResponse struct {
	Task      string        `json:"task"`
	SessionID string        `json:"session_id,omitempty"`
	Layers    []LayerResult `json:"layers"`
	MaxChars  int           `json:"max_chars"`
	CharsUsed int           `json:"chars_used"`
	// Rendered is the labelled plain-text pack, present only when requested.
	Rendered string `json:"rendered,omitempty"`
	// Warnings collects cross-layer problems: a layer that failed, a pinned
	// skill that does not exist, a provider that is not configured. A pack is
	// still returned — partial context beats no context — but never silently.
	Warnings []string `json:"warnings,omitempty"`
}
