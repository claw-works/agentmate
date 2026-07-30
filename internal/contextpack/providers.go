package contextpack

import (
	"context"

	"github.com/claw-works/agentmate/internal/knowledge"
	"github.com/claw-works/agentmate/internal/memory"
	"github.com/claw-works/agentmate/internal/notes"
	"github.com/claw-works/agentmate/internal/ownership"
	"github.com/claw-works/agentmate/internal/skills"
	"github.com/claw-works/agentmate/internal/todo"
)

// The interfaces below are declared here, by the consumer, rather than exported
// from each domain. Each one names only the calls the pack needs, so a domain
// gaining methods does not widen this package's surface, and tests can inject a
// double for a single layer without standing up five services.
//
// They still reference the domains' request and response types: inventing a
// parallel set of DTOs would mean maintaining a second definition of every
// field and would hide changes in the domains behind a translation layer.

type SkillProvider interface {
	Search(ctx context.Context, owner ownership.Owner, req skills.SearchSkillsRequest) (*skills.SearchSkillsResponse, error)
	GetActiveVersion(ctx context.Context, accountID, skillName string) (*skills.SkillVersion, error)
}

type KnowledgeProvider interface {
	Search(ctx context.Context, owner ownership.Owner, req knowledge.SearchKnowledgeRequest) (*knowledge.SearchKnowledgeResponse, error)
}

type MemoryProvider interface {
	SearchEntries(ctx context.Context, owner ownership.Owner, req memory.SearchEntriesRequest) (*memory.SearchResponse, error)
	SessionTimeline(ctx context.Context, owner ownership.Owner, params memory.SessionTimelineParams) (*memory.SessionTimelineResponse, error)
	Resume(ctx context.Context, owner ownership.Owner, sessionID string) (*memory.ResumeResponse, error)
}

// FactProvider covers live application state. These are queried in real time and
// deliberately never embedded: task state changes constantly, so an indexed copy
// would serve stale facts with the confidence of retrieved evidence.
type TodoProvider interface {
	Search(ctx context.Context, accountID, query string) ([]todo.Todo, error)
}

type NoteProvider interface {
	Search(ctx context.Context, accountID, query string) ([]notes.Note, error)
}

// Providers holds the collaborators. Every field is optional: a nil provider
// degrades its layer to an explanatory note instead of failing the whole pack,
// because partial context is still useful and the response says what is missing.
type Providers struct {
	Skills    SkillProvider
	Knowledge KnowledgeProvider
	Memory    MemoryProvider
	Todos     TodoProvider
	Notes     NoteProvider
}
