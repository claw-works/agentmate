package memory

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// 调用方是 agent。一次只报第一个错误意味着写错两个字段就要串行试错两轮，而每轮之间
// agent 可能已经压缩过上下文。真实接入里第一轮 6 条写入全部 400，且错误不含合法值，
// 唯一的发现路径变成去 MCP 的 inputSchema 里翻。这两条性质因此要锁住。
func TestValidateEntryReportsEveryFieldAtOnce(t *testing.T) {
	err := validateEntryRequest(CreateEntryRequest{
		ScopeType:  "project",
		ScopeKey:   "kiboard",
		MemoryType: "", // 错 1
		Content:    "x",
		Status:     StatusActive,
		Evidence:   []EvidenceInput{{}}, // 错 2
	}, time.Now())
	if err == nil {
		t.Fatal("expected validation to fail")
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error must still unwrap to ErrInvalidInput for the 400 mapping: %v", err)
	}
	var inputErr *InputError
	if !errors.As(err, &inputErr) {
		t.Fatalf("error must carry structured fields: %v", err)
	}
	if len(inputErr.Fields) != 2 {
		t.Fatalf("want both field errors at once, got %d: %+v", len(inputErr.Fields), inputErr.Fields)
	}

	byField := map[string]FieldError{}
	for _, f := range inputErr.Fields {
		byField[f.Field] = f
	}
	memoryType, ok := byField["memory_type"]
	if !ok {
		t.Fatalf("missing memory_type error: %+v", inputErr.Fields)
	}
	// allowed 是这条修复的全部意义：不列合法值等于把调用方推去读源码。
	if len(memoryType.Allowed) != 3 {
		t.Fatalf("memory_type must enumerate its allowed values, got %+v", memoryType.Allowed)
	}
	for _, want := range []string{"semantic", "episodic", "procedural"} {
		found := false
		for _, got := range memoryType.Allowed {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("allowed missing %q: %+v", want, memoryType.Allowed)
		}
	}
	evidence, ok := byField["evidence[0]"]
	if !ok {
		t.Fatalf("missing evidence error: %+v", inputErr.Fields)
	}
	// 写入用 source_type/source_id，读出来叫 ref。同一概念两套词汇，真实接入里
	// agent 按 ref 的直觉写错了形状，所以错误必须点明这件事。
	if !strings.Contains(evidence.Message, "source_type") || !strings.Contains(evidence.Message, "ref") {
		t.Fatalf("evidence error must state the write shape and name the read-side vocabulary: %q", evidence.Message)
	}
}

func TestValidateEventReportsEveryFieldAtOnce(t *testing.T) {
	err := validateEventRequest(RecordEventRequest{
		ScopeType:      "bogus", // 错 1
		EventType:      "bogus", // 错 2
		IdempotencyKey: "",      // 错 3
	})
	var inputErr *InputError
	if !errors.As(err, &inputErr) {
		t.Fatalf("expected structured error, got %v", err)
	}
	if len(inputErr.Fields) != 3 {
		t.Fatalf("want three field errors, got %d: %+v", len(inputErr.Fields), inputErr.Fields)
	}
	for _, f := range inputErr.Fields {
		if f.Field == "scope_type" || f.Field == "event_type" {
			if len(f.Allowed) == 0 {
				t.Fatalf("%s must enumerate allowed values", f.Field)
			}
		}
	}
}

// sequence_no 是可选的。强制 agent 自己数序号，在 retry 或上下文压缩之后只会制造 409。
func TestEventSequenceNumberIsOptional(t *testing.T) {
	if err := validateEventRequest(RecordEventRequest{
		ScopeType:      DefaultScopeType,
		EventType:      "goal",
		IdempotencyKey: "k",
	}); err != nil {
		t.Fatalf("omitting sequence_no must be valid: %v", err)
	}
}

// InputSchema 必须从校验表导出，不能是另抄的一份常量：抄一份就会漂移，而"文档说的
// 合法值和服务端认的不一样"比没有文档更糟。
func TestInputSchemaMatchesValidators(t *testing.T) {
	schema := InputSchema()
	entries, ok := schema["entries"].(map[string]any)
	if !ok {
		t.Fatal("schema missing entries section")
	}
	declared, ok := entries["memory_types"].([]string)
	if !ok {
		t.Fatalf("memory_types must be a string list, got %T", entries["memory_types"])
	}
	if len(declared) != len(validMemoryTypes) {
		t.Fatalf("schema declares %d memory types, validator accepts %d", len(declared), len(validMemoryTypes))
	}
	for _, value := range declared {
		if !validMemoryTypes[value] {
			t.Fatalf("schema declares %q but the validator rejects it", value)
		}
	}
}
