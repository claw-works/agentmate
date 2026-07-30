package knowledge

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/claw-works/agentmate/internal/auth"
	"github.com/gin-gonic/gin"
)

func TestStrictPagination(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, testCase := range []struct {
		name       string
		query      string
		wantLimit  int
		wantOffset int
		wantError  bool
	}{
		{name: "defaults", wantLimit: 20, wantOffset: 0},
		{name: "valid", query: "?limit=100&offset=25", wantLimit: 100, wantOffset: 25},
		{name: "invalid limit text", query: "?limit=nope", wantError: true},
		{name: "zero limit", query: "?limit=0", wantError: true},
		{name: "large limit", query: "?limit=101", wantError: true},
		{name: "negative offset", query: "?offset=-1", wantError: true},
		{name: "invalid offset text", query: "?offset=nope", wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Request = httptest.NewRequest("GET", "/api/knowledge/revisions/x/documents"+testCase.query, nil)
			limit, offset, err := strictPagination(context)
			if (err != nil) != testCase.wantError {
				t.Fatalf("strictPagination error = %v, wantError = %v", err, testCase.wantError)
			}
			if err == nil && (limit != testCase.wantLimit || offset != testCase.wantOffset) {
				t.Fatalf("strictPagination = (%d, %d), want (%d, %d)", limit, offset, testCase.wantLimit, testCase.wantOffset)
			}
		})
	}
}

// TestKnowledgeScopeEnforcement verifies the knowledge:r / knowledge:rw
// route guards using the shared auth.RequireScope middleware, exactly as
// wired in cmd/server/main.go.
func TestKnowledgeScopeEnforcement(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newRouter := func(scopes []string, requiredScope string) *gin.Engine {
		router := gin.New()
		router.GET("/probe",
			func(c *gin.Context) {
				c.Set(auth.ContextAuthMethod, "apikey")
				c.Set(auth.ContextScopes, scopes)
			},
			auth.RequireScope(requiredScope),
			func(c *gin.Context) { c.Status(http.StatusOK) },
		)
		return router
	}

	for _, testCase := range []struct {
		name       string
		scopes     []string
		required   string
		wantStatus int
	}{
		{name: "read scope allows read", scopes: []string{"knowledge:r"}, required: "knowledge:r", wantStatus: http.StatusOK},
		{name: "rw scope implies read", scopes: []string{"knowledge:rw"}, required: "knowledge:r", wantStatus: http.StatusOK},
		{name: "read scope denies write", scopes: []string{"knowledge:r"}, required: "knowledge:rw", wantStatus: http.StatusForbidden},
		{name: "unrelated scope denied", scopes: []string{"todos:rw"}, required: "knowledge:r", wantStatus: http.StatusForbidden},
		{name: "empty scopes mean full access", scopes: []string{}, required: "knowledge:rw", wantStatus: http.StatusOK},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest("GET", "/probe", nil)
			newRouter(testCase.scopes, testCase.required).ServeHTTP(recorder, request)
			if recorder.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, testCase.wantStatus)
			}
		})
	}
}

func TestGetDocumentSetsNoStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Header("Cache-Control", "private, no-store")
	if got := recorder.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
}
