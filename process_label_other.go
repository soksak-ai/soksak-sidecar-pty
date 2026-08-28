//go:build !darwin

package main

import (
	"fmt"

	controlwire "github.com/soksak-ai/soksak-contract-control"
)

func applyProcessLabel(label string) (string, error) {
	if label == controlwire.DefaultProcessLabel {
		return label, nil
	}
	return "", fmt.Errorf("PROCESS_LABEL_UNSUPPORTED: %s", label)
}
