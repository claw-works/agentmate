package memory

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// 静默丢弃是最坏的一类失败：写入方拿到 201，内容却从未被接收，两侧都发现不了。
// 真实接入里 agent 把事件正文写进了 content（events 上没有这个字段），服务端回 201、
// 响应里 payload 是 {}，于是它认为"服务端不回显内容"——服务端确实回显了，只是内容
// 从未存在过。这条契约必须锁住。
func TestBindStrictRejectsUnknownFieldAndNamesTheDestination(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, testCase := range []struct {
		name       string
		body       string
		wantField  string
		wantInHint string
	}{
		{
			name:       "事件正文放在 content",
			body:       `{"scope_type":"project","event_type":"decision","content":"GPIO9 反接"}`,
			wantField:  `unknown field "content"`,
			wantInHint: "payload",
		},
		{
			name:       "evidence 用读侧词汇 kind",
			body:       `{"scope_type":"project","kind":"url"}`,
			wantField:  `unknown field "kind"`,
			wantInHint: "source_type",
		},
		{
			name:       "纯粹拼错的字段也要拒绝",
			body:       `{"scope_type":"project","scoep_key":"kiboard"}`,
			wantField:  `unknown field "scoep_key"`,
			wantInHint: "/api/schema",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(testCase.body))

			var req RecordEventRequest
			err := bindStrict(c, &req)
			if err == nil {
				t.Fatal("unknown field must be rejected, not silently dropped")
			}
			if !strings.Contains(err.Error(), testCase.wantField) {
				t.Fatalf("error must name the offending field: %v", err)
			}
			// 只说"字段不认识"不够——调用方需要知道内容该放哪，否则它只能继续猜。
			if !strings.Contains(err.Error(), testCase.wantInHint) {
				t.Fatalf("error must point somewhere actionable (%q): %v", testCase.wantInHint, err)
			}
		})
	}
}

// 严格解码不能顺手把合法请求也拒了。
func TestBindStrictAcceptsWellFormedRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"scope_type":"project","scope_key":"kiboard","event_type":"decision","payload":{"text":"ok"},"idempotency_key":"k"}`))

	var req RecordEventRequest
	if err := bindStrict(c, &req); err != nil {
		t.Fatalf("well-formed request rejected: %v", err)
	}
	if req.Payload["text"] != "ok" {
		t.Fatalf("payload lost in decoding: %+v", req.Payload)
	}
}
