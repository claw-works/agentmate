package contextpack

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// 标签派生要按 rune 截断：按字节切会把多字节字符切成半个，显示成乱码。
func TestMemoryLabelHandlesCJKAndMarkdown(t *testing.T) {
	long := strings.Repeat("电气分析", 40) // 160 runes, 480 bytes
	for _, c := range []struct{ name, mtype, title, summary, content, want string }{
		{"取正文首行并去掉标题标记", "semantic", "", "", "# GPIO9 反接的电气分析\n\n详见文档。", "semantic · GPIO9 反接的电气分析"},
		{"有 title 时优先 title", "procedural", "矩阵扫描高阻改法", "", "正文", "procedural · 矩阵扫描高阻改法"},
		{"退到 summary", "episodic", "", "反接导致反向电流", "", "episodic · 反接导致反向电流"},
		{"三者皆空只报类型", "semantic", "", "", "", "semantic"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := memoryLabel(c.mtype, c.title, c.summary, c.content)
			if got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("label is not valid UTF-8: %q", got)
			}
		})
	}

	truncated := memoryLabel("semantic", "", "", long)
	if !utf8.ValidString(truncated) {
		t.Fatalf("truncation broke a multibyte character: %q", truncated)
	}
	if !strings.HasSuffix(truncated, "…") {
		t.Fatalf("long label must be marked as truncated: %q", truncated)
	}
	// 80 runes + 前缀 + 省略号，远小于按字节截断会产生的长度
	if runes := utf8.RuneCountInString(truncated); runes > 100 {
		t.Fatalf("label too long: %d runes", runes)
	}
}
