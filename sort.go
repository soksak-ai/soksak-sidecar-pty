package main

import (
	"sort"

	controlwire "github.com/soksak-ai/soksak-contract-control"
)

// sortTable orders both lists by name.
//
// A map iterates in a different order every run, so an unsorted greeting differs between two runs of
// the same build. A reader diffing them would see change where there was none.
func sortTable(table *controlwire.Table) {
	sort.Slice(table.Commands, func(i, j int) bool { return table.Commands[i].Name < table.Commands[j].Name })
	sort.Slice(table.Unserved, func(i, j int) bool { return table.Unserved[i].Name < table.Unserved[j].Name })
}
