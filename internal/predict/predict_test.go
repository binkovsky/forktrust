package predict

import (
	"strings"
	"testing"
)

func TestFormatWarning_Empty(t *testing.T) {
	if s := FormatWarning(nil, 5); s != "" {
		t.Errorf("expected empty for no overlaps, got %q", s)
	}
	if s := FormatWarning([]Overlap{}, 5); s != "" {
		t.Errorf("expected empty for empty slice, got %q", s)
	}
}

func TestFormatWarning_SingleFile(t *testing.T) {
	out := FormatWarning([]Overlap{
		{File: "src/api.ts", Slugs: []string{"feat-x"}},
	}, 5)
	if !strings.Contains(out, "1 file ") {
		t.Errorf("expected '1 file' (singular), got %q", out)
	}
	if !strings.Contains(out, "1 worktree ") {
		t.Errorf("expected '1 worktree' (singular), got %q", out)
	}
	if !strings.Contains(out, "src/api.ts") {
		t.Errorf("missing file path: %q", out)
	}
	if !strings.Contains(out, "feat-x") {
		t.Errorf("missing slug: %q", out)
	}
}

func TestFormatWarning_PluralAndMultipleSlugs(t *testing.T) {
	out := FormatWarning([]Overlap{
		{File: "a.ts", Slugs: []string{"s1", "s2"}},
		{File: "b.ts", Slugs: []string{"s2"}},
		{File: "c.ts", Slugs: []string{"s3"}},
	}, 5)
	if !strings.Contains(out, "3 files") {
		t.Errorf("expected '3 files' plural: %q", out)
	}
	if !strings.Contains(out, "3 worktrees") {
		t.Errorf("expected '3 worktrees' (s1, s2, s3 unique): %q", out)
	}
	for _, want := range []string{"a.ts", "b.ts", "c.ts", "s1", "s2", "s3"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestFormatWarning_TruncatesAtMax(t *testing.T) {
	var overlaps []Overlap
	for i := 0; i < 12; i++ {
		overlaps = append(overlaps, Overlap{File: "f" + string(rune('A'+i)) + ".ts", Slugs: []string{"slug-x"}})
	}
	out := FormatWarning(overlaps, 3)
	// Should show 3 files then "... N more"
	for _, want := range []string{"fA.ts", "fB.ts", "fC.ts", "more"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "fD.ts") {
		t.Errorf("did not truncate; fD.ts should be hidden: %s", out)
	}
}

func TestFormatWarning_MaxDefault(t *testing.T) {
	// max <= 0 should fall back to 5
	var overlaps []Overlap
	for i := 0; i < 7; i++ {
		overlaps = append(overlaps, Overlap{File: "z" + string(rune('0'+i)) + ".ts", Slugs: []string{"s"}})
	}
	out := FormatWarning(overlaps, 0)
	if !strings.Contains(out, "more") {
		t.Errorf("expected truncation marker for max=0 default-5: %s", out)
	}
	if !strings.Contains(out, "z4.ts") {
		t.Errorf("expected 5th file to be shown: %s", out)
	}
	if strings.Contains(out, "z5.ts") {
		t.Errorf("expected 6th file to be hidden: %s", out)
	}
}

func TestFormatCount(t *testing.T) {
	cases := []struct {
		n    int
		noun string
		want string
	}{
		{0, "file", "0 file"},
		{1, "file", "1 file"},
		{2, "files", "2 files"},
		{12, "worktree", "12 worktree"},
	}
	for _, c := range cases {
		if got := formatCount(c.n, c.noun); got != c.want {
			t.Errorf("formatCount(%d, %q) = %q, want %q", c.n, c.noun, got, c.want)
		}
	}
}

func TestIntoa(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{{0, "0"}, {1, "1"}, {9, "9"}, {10, "10"}, {99, "99"}, {100, "100"}, {12345, "12345"}}
	for _, c := range cases {
		if got := intoa(c.in); got != c.want {
			t.Errorf("intoa(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
