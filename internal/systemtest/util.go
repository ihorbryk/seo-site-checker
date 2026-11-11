package systemtest

import (
	"strconv"
	"testing"
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func atoi(t testing.TB, s string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
