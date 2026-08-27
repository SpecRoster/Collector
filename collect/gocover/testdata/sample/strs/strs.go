package strs

import (
	"strings"

	"example.com/sample/calc"
)

// Repeat upper-cases s and repeats it Add(n, 0) times — deliberately
// crossing into the calc package so the collector's -coverpkg behavior is
// observable in tests.
func Repeat(s string, n int) string {
	return strings.Repeat(strings.ToUpper(s), calc.Add(n, 0))
}
