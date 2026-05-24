package ui

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// OpenURLWithOS opens the given URL with the OS default browser.
//
// On macOS it uses `open`; on Linux it uses `xdg-open`; on Windows it uses
// `explorer`. The function returns an error if the command is not found or
// exits non-zero. Only http:// and https:// URLs are accepted.
//
// This is the production implementation injected into AppOptions.OpenURL.
// Tests inject a no-op stub via AppOptions.OpenURL to avoid spawning a browser.
func OpenURLWithOS(url string) error {
	if url == "" {
		return fmt.Errorf("ui: OpenURLWithOS: empty URL")
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("ui: OpenURLWithOS: unsupported URL scheme")
	}
	var cmd string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd = "explorer"
	default:
		// Linux, BSD, etc.
		cmd = "xdg-open"
	}
	return exec.Command(cmd, url).Start() //nolint:gosec // URL is sourced from regex-validated URLHitZone
}
