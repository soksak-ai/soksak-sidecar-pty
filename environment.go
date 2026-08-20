package main

import (
	"runtime"
	"sort"
	"strings"
)

// The environment a session runs in is the caller's, with this daemon's own removed.
//
// Nothing here reads os.Environ. This process outlives whatever launched it, so its own environment
// is a snapshot of one moment that has no claim on a session started an hour later — and a session
// that inherited it would differ from an identical one started by a different launcher.
//
// What the caller sends is the whole environment, plus the names it wants dropped. The defaults
// below are the terminal facts a tty needs and are applied only where the caller named nothing.
func sessionEnvironment(entries [][2]string, drop []string) []string {
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

	given := make(map[string]struct{}, len(entries))
	result := make([]string, 0, len(entries)+len(ttyDefaults()))
	for _, entry := range entries {
		if _, remove := dropped[normalize(entry[0])]; remove {
			continue
		}
		given[normalize(entry[0])] = struct{}{}
		result = append(result, entry[0]+"="+entry[1])
	}

	defaults := ttyDefaults()
	names := make([]string, 0, len(defaults))
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
