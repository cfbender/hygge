package cli

import (
	"strings"

	"github.com/charmbracelet/colorprofile"
)

func tuiColorProfile(environ []string) colorprofile.Profile {
	if envValue(environ, "TERM_PROGRAM") == "Apple_Terminal" && !envTrueColor(environ) {
		return colorprofile.ANSI256
	}
	return colorprofile.TrueColor
}

func envTrueColor(environ []string) bool {
	value := strings.ToLower(envValue(environ, "COLORTERM"))
	return value == "truecolor" || value == "24bit"
}

func envValue(environ []string, key string) string {
	prefix := key + "="
	for _, entry := range environ {
		if after, ok := strings.CutPrefix(entry, prefix); ok {
			return after
		}
	}
	return ""
}
