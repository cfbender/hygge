package cli

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// newLogsCmd builds the `hygge logs` subcommand.
//
// It reads $XDG_STATE_HOME/hygge/hygge.log (the file written by
// setupTUILog) and prints matching lines to stdout.
//
// Flags:
//
//	-n / --lines N  Print only the last N lines (0 = none; default: all).
//	--level LEVEL   Filter to lines at or above LEVEL
//	                (debug < info < warn < error).
//
// Level filtering uses the `level=LEVEL` field emitted by
// slog.NewTextHandler.  Lines that do not contain the `level=` field
// (e.g. multi-line continuation payloads) are kept when no level filter
// is active, and dropped when one is.
func newLogsCmd() *cobra.Command {
	var (
		lines    int
		levelArg string
	)
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Print the Hygge log file",
		Long: `Print the contents of the Hygge log file.

Use -n / --lines to limit output to the last N lines; explicit -n 0 prints nothing.
Use --level to filter by minimum severity (debug, info, warn, error).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if lines < 0 {
				return die(cmd, "hygge logs: -n must be >= 0, got %d", lines)
			}

			// Resolve log level threshold.
			var minLevel slog.Level
			filterByLevel := false
			if levelArg != "" {
				var ok bool
				minLevel, ok = parseSlogLevel(levelArg)
				if !ok {
					return die(cmd, "hygge logs: unknown level %q (valid: debug, info, warn, error)", levelArg)
				}
				filterByLevel = true
			}

			// Resolve the log file path using the same XDG resolution
			// as bootstrap, consulting testOverrides when set.
			logPath := resolveLogPath()

			file, err := os.Open(logPath) //nolint:gosec // logPath is derived from our state dir
			if err != nil {
				if os.IsNotExist(err) {
					// No log file yet — print nothing; this is not an error.
					return nil
				}
				return fmt.Errorf("hygge logs: open %s: %w", logPath, err)
			}
			defer func() { _ = file.Close() }()

			limitLines := cmd.Flags().Changed("lines")
			var kept []string
			var ring []string
			var ringCount int
			if limitLines && lines > 0 {
				ring = make([]string, lines)
			}

			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				line := scanner.Text()
				if filterByLevel && !lineMatchesLevel(line, minLevel) {
					continue
				}
				if !limitLines {
					kept = append(kept, line)
				} else if lines > 0 {
					ring[ringCount%lines] = line
					ringCount++
				}
			}
			if err := scanner.Err(); err != nil {
				return fmt.Errorf("hygge logs: scan %s: %w", logPath, err)
			}

			if limitLines && lines > 0 {
				if ringCount <= lines {
					kept = ring[:ringCount]
				} else {
					start := ringCount % lines
					kept = make([]string, 0, lines)
					kept = append(kept, ring[start:]...)
					kept = append(kept, ring[:start]...)
				}
			}

			w := out(cmd)
			for _, l := range kept {
				writeln(w, l)
			}
			return nil
		},
	}

	cmd.Flags().IntVarP(&lines, "lines", "n", 0, "print only the last N lines (0 = none)")
	cmd.Flags().StringVar(&levelArg, "level", "", "minimum log level to show: debug, info, warn, error")
	return cmd
}

// resolveLogPath returns the path to hygge.log, replicating the XDG
// state resolution used in bootstrap/setupTUILog.  testOverrides is
// consulted first so CLI tests always use hermetic paths.
func resolveLogPath() string {
	xdgState := ""
	if testOverrides != nil && testOverrides.XDGStateHome != "" {
		xdgState = testOverrides.XDGStateHome
	}
	if xdgState == "" {
		if v, ok := os.LookupEnv("XDG_STATE_HOME"); ok && v != "" {
			xdgState = v
		}
	}
	if xdgState == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			xdgState = filepath.Join(home, ".local", "state")
		}
	}
	return filepath.Join(xdgState, "hygge", "hygge.log")
}

// parseSlogLevel maps a user-supplied string to a slog.Level.  The
// mapping mirrors the level names written by slog.NewTextHandler.
// Returns (level, true) on success, (0, false) on unknown input.
func parseSlogLevel(s string) (slog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	default:
		return 0, false
	}
}

// lineMatchesLevel reports whether a slog text-format log line is at
// or above minLevel.  The slog text handler writes the level as
// `level=DEBUG`, `level=INFO`, `level=WARN`, `level=ERROR` — we scan
// for the `level=` key and compare the value.
//
// Lines without a `level=` field (e.g. bare continuation text) are
// treated as below any threshold and are excluded when filtering is
// active.
func lineMatchesLevel(line string, minLevel slog.Level) bool {
	const prefix = "level="
	_, after, ok := strings.Cut(line, prefix)
	if !ok {
		return false
	}
	rest := after
	// The value runs until the next space (unquoted) or end-of-string.
	before, _, ok := strings.Cut(rest, " ")
	var token string
	if !ok {
		token = rest
	} else {
		token = before
	}
	lineLevel, ok := parseSlogLevel(token)
	if !ok {
		// Unknown level token — keep the line (conservative).
		return true
	}
	return lineLevel >= minLevel
}
