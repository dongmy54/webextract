package main

import (
	"strings"
	"testing"
)

func TestURLToFilePath(t *testing.T) {
	used := map[string]bool{}

	check := func(name, in, want string) {
		t.Run(name, func(t *testing.T) {
			got, err := URLToFilePath(in, "output", used)
			if err != nil {
				t.Fatalf("错误: %v", err)
			}
			if got != want {
				t.Errorf("得到 %q，期望 %q", got, want)
			}
		})
	}

	check("根路径", "https://x.com/", "index.md")
	check("单段", "https://x.com/install", "install.md")
	check("多段", "https://x.com/config/mysql", "config/mysql.md")
	check("非法冒号替换", "https://x.com/a:b", "a_b.md")
	// 同 path 不同 query 用 query 的短 hash 区分。
	check("query 加 hash 后缀", "https://x.com/p?id=1", "p_"+shortHash("id=1")+".md")

	// 冲突：used 已占用 a.md 时，追加 _2。
	usedConflict := map[string]bool{"a.md": true}
	if got, err := URLToFilePath("https://x.com/a", "output", usedConflict); err != nil {
		t.Fatalf("错误: %v", err)
	} else if got != "a_2.md" {
		t.Errorf("冲突解决：得到 %q，期望 a_2.md", got)
	}

	// 超长段截断：仍以 .md 结尾，且总长可控。
	long := "https://x.com/" + strings.Repeat("a", 300)
	got, err := URLToFilePath(long, "output", map[string]bool{})
	if err != nil {
		t.Fatalf("错误: %v", err)
	}
	if !strings.HasSuffix(got, ".md") {
		t.Errorf("超长截断后缀错误: %q", got)
	}
	// 截断格式：前 100 + "_" + 8 位 hash + ".md"
	base := strings.TrimSuffix(got, ".md")
	if !strings.HasPrefix(base, strings.Repeat("a", 100)+"_") {
		t.Errorf("超长截断前缀错误: %q", base)
	}
}

func TestSanitizeSegment(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"normal", "normal"},
		{"a/b", "a_b"},
		{"a:b*c?", "a_b_c_"},
		{".", "_"},
		{"..", "_"},
		{"", "_"},
	}
	for _, tc := range cases {
		if got := sanitizeSegment(tc.in); got != tc.want {
			t.Errorf("sanitizeSegment(%q)=%q，期望 %q", tc.in, got, tc.want)
		}
	}
}
