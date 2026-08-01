package memory

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// checkpoint 可以经通用的 events 路径写入，而那条路径的 payload 是自由格式。
// 不在写入时校验，类型写错的 checkpoint 会被收下、只在读取时炸——失败点离犯错的
// 地方隔了一次会话，而且炸的时候正是最需要它的时候（断点恢复）。
func TestCheckpointPayloadValidatedOnWrite(t *testing.T) {
	err := validateEventRequest(RecordEventRequest{
		ScopeType:      "project",
		ScopeKey:       "kiboard",
		EventType:      checkpointEventType,
		IdempotencyKey: "k",
		Payload: map[string]any{
			"goal":       "修 GPIO9",
			"next":       "改高阻", // 应为 []string
			"blocked_on": "等硬件", // 不是 checkpoint 的字段
		},
	})
	var inputErr *InputError
	if !errors.As(err, &inputErr) {
		t.Fatalf("a malformed checkpoint payload must be rejected on write, got %v", err)
	}
	fields := map[string]string{}
	for _, f := range inputErr.Fields {
		fields[f.Field] = f.Message
	}
	if msg, ok := fields["payload.next"]; !ok || !strings.Contains(msg, "array of strings") {
		t.Fatalf("next must be reported as a type error: %+v", inputErr.Fields)
	}
	// 未知键必须报错而不是忽略：写入方以为记下了阻塞原因，实际什么都没留下。
	if msg, ok := fields["payload.blocked_on"]; !ok || !strings.Contains(msg, "unknown checkpoint field") {
		t.Fatalf("unknown checkpoint field must be rejected: %+v", inputErr.Fields)
	}
}

func TestCheckpointPayloadAcceptsCorrectShape(t *testing.T) {
	if err := validateEventRequest(RecordEventRequest{
		ScopeType: "project", ScopeKey: "kiboard",
		EventType: checkpointEventType, IdempotencyKey: "k",
		Payload: map[string]any{
			"goal": "修 GPIO9", "label": "v1", "notes": "n",
			"done": []any{"定位"}, "next": []any{"改高阻"}, "open": []any{"等货"},
		},
	}); err != nil {
		t.Fatalf("correct checkpoint payload rejected: %v", err)
	}
}

// 已经存在的坏数据（老服务端写入的）必须能降级读出，而不是让整个 TASK 层不可用。
// 真实接入里 next 写成字符串导致 goal 也读不出来，一个断点恢复用的东西因此在最需要
// 它的时候完全失效。
func TestCheckpointReadDegradesInsteadOfFailing(t *testing.T) {
	event := &Event{
		ID:        "e1",
		SessionID: "s1",
		Payload:   json.RawMessage(`{"goal":"旧快照","next":"单个字符串","blocked_on":"x"}`),
	}
	checkpoint, err := checkpointFromEvent(event)
	if err != nil {
		t.Fatalf("a malformed checkpoint must degrade, not error: %v", err)
	}
	// 关键性质：能认的字段照样读出来。
	if checkpoint.Goal != "旧快照" {
		t.Fatalf("goal lost to an unrelated field's type error: %+v", checkpoint)
	}
	if len(checkpoint.Next) != 1 || checkpoint.Next[0] != "单个字符串" {
		t.Fatalf("single string should be read as a one-item list: %+v", checkpoint.Next)
	}
	if !strings.Contains(checkpoint.Warning, "next") {
		t.Fatalf("degradation must be reported: %q", checkpoint.Warning)
	}
	if !strings.Contains(checkpoint.Warning, "blocked_on") {
		t.Fatalf("ignored unknown field must be reported: %q", checkpoint.Warning)
	}
}

func TestCheckpointReadHandlesNonObjectPayload(t *testing.T) {
	checkpoint, err := checkpointFromEvent(&Event{ID: "e2", Payload: json.RawMessage(`"just a string"`)})
	if err != nil {
		t.Fatalf("non-object payload must not error: %v", err)
	}
	// 即使一个字段都救不回来，也要让调用方知道"这里有一个 checkpoint，只是读不出内容"。
	if checkpoint.EventID != "e2" || checkpoint.Warning == "" {
		t.Fatalf("want an identified but empty checkpoint with a warning: %+v", checkpoint)
	}
}

// 空 payload 的事件回读时必须明说正文为空，否则调用方对着 {} 无从判断是"本来没内容"
// 还是"内容在写入时丢了"。
func TestEmptyPayloadWarningOnReadBack(t *testing.T) {
	if w := emptyPayloadWarning(Event{Payload: json.RawMessage(`{}`)}); w == "" {
		t.Fatal("an empty payload must be flagged on read-back")
	}
	if w := emptyPayloadWarning(Event{Payload: json.RawMessage(`{"text":"x"}`)}); w != "" {
		t.Fatalf("a populated payload must not be flagged: %q", w)
	}
}

// checkpoint 的字段表被读取侧、写入校验和 /api/schema 三处共用，不能各说一套。
func TestSchemaDeclaresCheckpointPayload(t *testing.T) {
	events, ok := InputSchema()["events"].(map[string]any)
	if !ok {
		t.Fatal("schema missing events section")
	}
	declared, ok := events["checkpoint_payload"].(map[string]any)
	if !ok {
		t.Fatal("schema must declare the checkpoint payload structure")
	}
	fields, ok := declared["fields"].(map[string]string)
	if !ok {
		t.Fatalf("checkpoint fields must be declared, got %T", declared["fields"])
	}
	for _, required := range []string{"goal", "done", "next", "open", "label", "notes"} {
		if _, present := fields[required]; !present {
			t.Fatalf("schema omits checkpoint field %q", required)
		}
	}
	if fields["next"] != "[]string" || fields["goal"] != "string" {
		t.Fatalf("declared types disagree with the validator: %+v", fields)
	}
}
