//go:build darwin

package main

import "errors"

func applyProcessLabel(string) (string, error) {
	return "", errors.New("PROCESS_LABEL_APPLY_FAILED")
}
