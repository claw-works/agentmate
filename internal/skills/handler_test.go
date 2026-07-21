package skills

import (
	"net/http/httptest"
	"strings"
	"testing"

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
			context.Request = httptest.NewRequest("GET", "/api/skills/catalog"+testCase.query, nil)
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

func TestValidateCatalogQueryLimit(t *testing.T) {
	if err := validateCatalogQuery(strings.Repeat("界", maxCatalogQueryRunes)); err != nil {
		t.Fatalf("valid query rejected: %v", err)
	}
	if err := validateCatalogQuery(strings.Repeat("界", maxCatalogQueryRunes+1)); err == nil {
		t.Fatal("expected oversized query error")
	}
}

func TestSetQualityNoStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	setQualityNoStore(context)
	if got := recorder.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control = %q, want private, no-store", got)
	}
}
