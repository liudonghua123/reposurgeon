package main

import (
	"testing"
)

func assertEqual(t *testing.T, a string, b string) {
	t.Helper()
	if a != b {
		t.Fatalf("assertEqual: expected %q == %q", a, b)
	}
}

func TestNameSequenceLength(t *testing.T) {
	// first cut down the sequence so that it will have a much smaller ring
	seq := NewNameSequence()
	seq.color = seq.color[0:3]
	seq.item = seq.item[0:3]
	// then test that the name sequence loops after 3*3=9 names, and gains a suffix when it starts looping
	input := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	expected := []string{
		"AmberAngel", "AmethystAxe", "ArgentAngel",
		"AmberBear", "AmethystAngel", "ArgentBear",
		"AmberAxe", "AmethystBear", "ArgentAxe",
		"AmberAngel1"}
	names := make([]string, 0, 10)
	for _, s := range input {
		names = append(names, seq.obscureString(s))
	}
	for i := range expected {
		assertEqual(t, names[i], expected[i])
	}
}

func TestOptimizeRange(t *testing.T) {
	type optimizeTestEntry struct {
		before string
		after  string
	}
	tests := []optimizeTestEntry{
		{"1-1", "1"},
		{"1-2,4-5", "1-2,4-5"},
		{"1-2,3-4", "1-4"},
		{"1-2,2-3", "1-3"},
		{"1-1,2-2,3-3,5-5", "1-3,5"},
		{"1-1,2-2,3-3,5-5,7-7,8-8", "1-3,5,7-8"},
		{"1,2,3,5-5,7,8", "1-3,5,7-8"},
	}
	optimizeRange := func(in string) string {
		span := parseMergeinfoRange(in)
		span.Optimize()
		return span.dump("-")
	}
	for _, item := range tests {
		assertEqual(t, optimizeRange(item.before), item.after)
	}
}
