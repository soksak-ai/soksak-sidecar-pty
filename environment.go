package main

import (
	"os"
	"runtime"
	"sort"
	"strings"

	ptycontract "github.com/soksak-ai/soksak-contract-pty"
)

// The environment a session runs in is the caller's, and this daemon's own when the caller sends
// none.
//
// Reading os.Environ here is reading what was handed over, not deriving something: this process is
// started with an environment its starter chose, and a caller that is a document in a web view has
// no environment of its own to send. A daemon that refused instead would leave every session with
// no PATH, which fails as a shell that cannot find anything rather than as a missing argument.
//
// A caller that does send one replaces it whole rather than adding to it. Merging would make the
// result depend on what happened to be in this process, which is the thing an explicit environment
// exists to pin down. Session variables are the one addition: they ride on top of whichever
// environment applies, already limited by the contract to the SOKSAK_ namespace.
//
// The defaults below are the terminal facts a tty needs, applied only where nothing named them.
func sessionEnvironment(env ptycontract.Environment, drop []string) []string {
	entries := env.Replace
	if len(entries) == 0 {
		entries = inherited()
	}
	return applyEnvironment(entries, env.Variables, drop)
}

// inherited is this process's environment as pairs.
func inherited() [][2]string {
	own := os.Environ()
	entries := make([][2]string, 0, len(own))
	for _, entry := range own {
		name, value, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		entries = append(entries, [2]string{name, value})
	}
	return entries
}

func applyEnvironment(entries [][2]string, variables map[string]string, drop []string) []string {
	caseInsensitive := runtime.GOOS == "windows"
	normalize := func(name string) string {
		if caseInsensitive {
			return strings.ToUpper(name)
		}
		return name
	}

	dropped := make(map[string]struct{}, len(drop))
	for _, name := range drop {
		dropped[normalize(name)] = struct{}{}
	}
	// A session variable replaces an inherited value under the same name rather than duplicating
	// it: two entries for one name is a shell deciding which wins.
	overridden := make(map[string]struct{}, len(variables))
	for name := range variables {
		overridden[normalize(name)] = struct{}{}
	}

	given := make(map[string]struct{}, len(entries)+len(variables))
	result := make([]string, 0, len(entries)+len(variables)+len(ttyDefaults()))
	for _, entry := range entries {
		key := normalize(entry[0])
		if _, remove := dropped[key]; remove {
			continue
		}
		if _, replaced := overridden[key]; replaced {
			continue
		}
		given[key] = struct{}{}
		result = append(result, entry[0]+"="+entry[1])
	}

	names := make([]string, 0, len(variables))
	for name := range variables {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		given[normalize(name)] = struct{}{}
		result = append(result, name+"="+variables[name])
	}

	defaults := ttyDefaults()
	names = names[:0]
	for name := range defaults {
		if _, already := given[normalize(name)]; already {
			continue
		}
		if _, remove := dropped[normalize(name)]; remove {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		result = append(result, name+"="+defaults[name])
	}
	return result
}

// ttyDefaults are what a program discovers about the terminal it was started under.
//
// They are defaults rather than overrides: a caller that names TERM has a reason, and a daemon that
// overwrote it would decide what a terminal is, which is the one thing this process does not do.
func ttyDefaults() map[string]string {
	defaults := map[string]string{
		"TERM":      "xterm-256color",
		"COLORTERM": "truecolor",
	}
	if runtime.GOOS != "windows" {
		defaults["LANG"] = "en_US.UTF-8"
		defaults["LC_CTYPE"] = "en_US.UTF-8"
	}
	return defaults
}
