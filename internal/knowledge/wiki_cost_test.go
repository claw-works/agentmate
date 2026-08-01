package knowledge

import (
	"encoding/json"
	"strings"
	"testing"
)

// cost_micros: 0 有两种含义：没配单价，或这次编译真的没调用模型。
// 混在一起会让成本表上出现一个看起来权威的错数字（0 被读成"免费"）。
func TestBuildCostPricedDistinguishesUnpricedFromZero(t *testing.T) {
	for _, c := range []struct {
		name       string
		build      BuildRevision
		wantPriced bool
	}{
		{"花了 token 却零成本 = 没配单价", BuildRevision{InputTokens: 1000, OutputTokens: 500, CostMicros: 0}, false},
		{"花了 token 且有成本 = 已配单价", BuildRevision{InputTokens: 1000, CostMicros: 42}, true},
		{"没花 token 所以零成本是真的", BuildRevision{}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			encoded, err := json.Marshal(c.build)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			want := `"cost_priced":false`
			if c.wantPriced {
				want = `"cost_priced":true`
			}
			if !strings.Contains(string(encoded), want) {
				t.Fatalf("want %s in %s", want, encoded)
			}
		})
	}
}

func TestBuildMarshalKeepsExistingFields(t *testing.T) {
	encoded, err := json.Marshal(BuildRevision{ID: "b1", CostMicros: 7, InputTokens: 1})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, field := range []string{`"id":"b1"`, `"cost_micros":7`, `"is_active":false`} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("lost field %s: %s", field, encoded)
		}
	}
}
