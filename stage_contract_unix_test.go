//go:build !windows

package main

import "testing"

func TestStageDispatchesDeclaredDarwinARM64Target(t *testing.T) {
	verifyStageDispatchesDeclaredDarwinARM64Target(t)
}
