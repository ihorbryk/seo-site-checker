package diff

import (
	"strings"

	"github.com/aymanbagabas/go-udiff"
)

// RowKind says what a line of a rendered body Diff is.
type RowKind string

const (
	RowEqual  RowKind = "eq"
	RowDelete RowKind = "del"
	RowInsert RowKind = "add"
	RowGap    RowKind = "gap" // a collapsed run of unchanged lines
)

// Row is one line of a unified body Diff. OldNo and NewNo are 1-based line
// numbers, 0 where the line does not exist on that side. A gap row carries
// the number of hidden lines in Skipped.
type Row struct {
	Kind         RowKind
	OldNo, NewNo int
	Text         string
	Skipped      int
}

// SplitRow is one row of a side-by-side body Diff. A nil side is blank.
type SplitRow struct {
	Left, Right *Row
	Gap         int // hidden unchanged lines when this row is a gap
}

// ContextLines is how many unchanged lines surround a change.
const ContextLines = 3

// LineDiff line-diffs two texts with go-udiff and returns the unified rows,
// collapsing unchanged runs longer than 2*context into a gap row. It returns
// no rows when the texts are equal.
func LineDiff(before, after string, context int) []Row {
	edits := udiff.Lines(before, after)
	if len(edits) == 0 {
		return nil
	}
	var rows []Row
	oldNo, newNo, pos := 1, 1, 0
	equal := func(text string) {
		for _, l := range splitLines(text) {
			rows = append(rows, Row{Kind: RowEqual, OldNo: oldNo, NewNo: newNo, Text: l})
			oldNo++
			newNo++
		}
	}
	for _, e := range edits {
		equal(before[pos:e.Start])
		for _, l := range splitLines(before[e.Start:e.End]) {
			rows = append(rows, Row{Kind: RowDelete, OldNo: oldNo, Text: l})
			oldNo++
		}
		for _, l := range splitLines(e.New) {
			rows = append(rows, Row{Kind: RowInsert, NewNo: newNo, Text: l})
			newNo++
		}
		pos = e.End
	}
	equal(before[pos:])
	return collapse(rows, context)
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// collapse replaces unchanged runs longer than 2*context with context lines
// on each side of a change and a gap row in between. Runs at the start or end
// of the text keep context lines on the change side only.
func collapse(rows []Row, context int) []Row {
	var out []Row
	for i := 0; i < len(rows); {
		if rows[i].Kind != RowEqual {
			out = append(out, rows[i])
			i++
			continue
		}
		j := i
		for j < len(rows) && rows[j].Kind == RowEqual {
			j++
		}
		head, tail := context, context
		if i == 0 {
			head = 0
		}
		if j == len(rows) {
			tail = 0
		}
		if j-i <= head+tail+1 {
			out = append(out, rows[i:j]...)
		} else {
			out = append(out, rows[i:i+head]...)
			out = append(out, Row{Kind: RowGap, Skipped: j - i - head - tail})
			out = append(out, rows[j-tail:j]...)
		}
		i = j
	}
	return out
}

// SplitRows pairs the deletions and insertions of each change block into
// side-by-side rows.
func SplitRows(rows []Row) []SplitRow {
	var out []SplitRow
	for i := 0; i < len(rows); {
		r := rows[i]
		switch r.Kind {
		case RowEqual:
			out = append(out, SplitRow{Left: &rows[i], Right: &rows[i]})
			i++
		case RowGap:
			out = append(out, SplitRow{Gap: r.Skipped})
			i++
		default:
			var dels, adds []*Row
			for ; i < len(rows) && (rows[i].Kind == RowDelete || rows[i].Kind == RowInsert); i++ {
				if rows[i].Kind == RowDelete {
					dels = append(dels, &rows[i])
				} else {
					adds = append(adds, &rows[i])
				}
			}
			for k := 0; k < max(len(dels), len(adds)); k++ {
				var sr SplitRow
				if k < len(dels) {
					sr.Left = dels[k]
				}
				if k < len(adds) {
					sr.Right = adds[k]
				}
				out = append(out, sr)
			}
		}
	}
	return out
}
