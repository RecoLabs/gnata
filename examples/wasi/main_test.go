package main

import (
	"slices"
	"testing"
)

func TestRun(t *testing.T) {
	lines, err := run()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := []string{
		`user.plan = "enterprise" -> true`,
		`$sum(orders.amount) -> 1150`,
		`$upper(user.name) -> ADA`,
		`{"id": user.id, "orders": $count(orders)} -> {"id":"u1","orders":2}`,
	}
	if !slices.Equal(lines, want) {
		t.Fatalf("run() = %q, want %q", lines, want)
	}
	main()
}
