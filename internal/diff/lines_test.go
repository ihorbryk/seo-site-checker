package diff

import (
	"fmt"
	"strings"
	"testing"
)

func numbered(n int, change map[int]string) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		if c, ok := change[i]; ok {
			b.WriteString(c + "\n")
		} else {
			fmt.Fprintf(&b, "line %d\n", i)
		}
	}
	return b.String()
}

func renderRows(rows []Row) string {
	var parts []string
	for _, r := range rows {
		if r.Kind == RowGap {
			parts = append(parts, fmt.Sprintf("gap%d", r.Skipped))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s:%d/%d:%s", r.Kind, r.OldNo, r.NewNo, r.Text))
	}
	return strings.Join(parts, " | ")
}

func TestLineDiffEqualTextsHaveNoRows(t *testing.T) {
	if rows := LineDiff("a\nb\n", "a\nb\n", 3); rows != nil {
		t.Errorf("rows = %v", rows)
	}
}

func TestLineDiffMarksDeletesAndInsertsWithLineNumbers(t *testing.T) {
	got := renderRows(LineDiff("a\nb\nc\n", "a\nB\nc\n", 3))
	want := "eq:1/1:a | del:2/0:b | add:0/2:B | eq:3/3:c"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestLineDiffCollapsesLongUnchangedRuns(t *testing.T) {
	got := renderRows(LineDiff(numbered(30, nil), numbered(30, map[int]string{2: "X", 29: "Y"}), 3))
	if !strings.Contains(got, "gap") || strings.Contains(got, "line 15") {
		t.Errorf("middle not collapsed: %s", got)
	}
	if !strings.Contains(got, "eq:5/5:line 5") || strings.Contains(got, "line 6") {
		t.Errorf("context wrong: %s", got)
	}
}

func TestSplitRowsPairsChangeBlocks(t *testing.T) {
	rows := SplitRows(LineDiff("a\nb\nc\n", "a\nB1\nB2\nc\n", 3))
	if len(rows) != 4 {
		t.Fatalf("rows = %d", len(rows))
	}
	if rows[1].Left == nil || rows[1].Left.Text != "b" || rows[1].Right.Text != "B1" {
		t.Errorf("row 1 = %+v", rows[1])
	}
	if rows[2].Left != nil || rows[2].Right.Text != "B2" {
		t.Errorf("row 2 = %+v", rows[2])
	}
}
