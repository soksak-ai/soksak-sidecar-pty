//go:build windows

package main

import "fmt"

func startSessionProcess(_ string, _ string, _ []string, cols, rows uint16) (sessionProcess, error) {
	return nil, fmt.Errorf("ConPTY is not implemented for a %dx%d session", cols, rows)
}
