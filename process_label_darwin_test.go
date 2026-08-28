//go:build darwin

package main

import "testing"

func TestDarwinAppliesTheDeclaredProcessLabel(t *testing.T) {
	got, err := applyProcessLabel("soksakv3")
	if err != nil || got != "soksakv3" {
		t.Fatalf("process label = %q, %v", got, err)
	}
}
