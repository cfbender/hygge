package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/cfbender/hygge/internal/session"
)

// newResumeCmd builds `hygge resume [id-prefix]`.
//
// With a prefix argument, resume the most recent session whose id begins
// with that prefix (scoped to cwd by default; --any disables scoping).
//
// Without a prefix argument:
//   - 0 sessions in cwd  → error
//   - 1 session in cwd   → resume it directly
//   - >1 sessions in cwd → open the sessions picker
//
// --any disables the cwd filter and restores pre-T2.4 global scope.
func newResumeCmd() *cobra.Command {
	var anyFlag bool

	cmd := &cobra.Command{
		Use:   "resume [id-prefix]",
		Short: "Resume a session for the current project",
		Long: `Resume a session.

Without an argument: auto-picks when exactly one session exists in the
current directory; opens an interactive picker when multiple sessions
exist; errors when none exist.

With an id-prefix argument: resumes the most recent session whose id
starts with that prefix.

--any  Disable the current-directory scope and search all sessions
       (restores the pre-T2.4 global behaviour).`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			rt, err := bootstrap(ctx, bootstrapOptions{
				ConfigFile:        rootFlags.ConfigFile,
				ProfileName:       rootFlags.Profile,
				Pwd:               rootFlags.Pwd,
				ReasoningOverride: reasoningFlag,
			})
			if err != nil {
				return err
			}
			defer func() { _ = rt.Close() }()

			// Resolve the project dir for scoping.
			projectDir := rt.Pwd
			if anyFlag {
				projectDir = ""
			}

			if len(args) == 1 {
				// Prefix match — scoped to cwd unless --any.
				sid, err := findSessionByPrefixScoped(ctx, rt, args[0], projectDir)
				if err != nil {
					return die(cmd, "%s", err)
				}
				printf(out(cmd), "resuming %s\n", shortID(sid))
				return runTUI(ctx, cmd, rt, sid, false)
			}

			// No prefix — list sessions for the project (or globally).
			listOpts := session.ListOpts{
				Kind:       session.KindPrimary,
				ProjectDir: projectDir,
				Limit:      200,
			}
			sessions, err := rt.Store.ListSessions(ctx, listOpts)
			if err != nil {
				return fmt.Errorf("cli: list sessions: %w", err)
			}

			switch {
			case len(sessions) == 0:
				if anyFlag {
					return die(cmd, "no sessions to resume")
				}
				return die(cmd, "no sessions to resume in %s (use --any to search all projects)", rt.Pwd)

			case len(sessions) == 1:
				sid := sessions[0].ID
				printf(out(cmd), "resuming %s\n", shortID(sid))
				return runTUI(ctx, cmd, rt, sid, false)

			default:
				// Multiple sessions — open the picker.
				return runTUI(ctx, cmd, rt, "", true)
			}
		},
	}

	cmd.Flags().BoolVar(&anyFlag, "any", false, "search all projects, not just the current directory")
	return cmd
}

// findSessionByPrefixScoped looks up a session whose id begins with prefix
// within the given projectDir scope.  When projectDir is empty (""),
// all sessions are searched (global scope).
func findSessionByPrefixScoped(ctx context.Context, rt *appRuntime, prefix, projectDir string) (string, error) {
	if prefix == "" {
		return "", fmt.Errorf("cli: empty session prefix")
	}
	opts := session.ListOpts{
		ProjectDir: projectDir,
		Limit:      200,
	}
	sessions, err := rt.Store.ListSessions(ctx, opts)
	if err != nil {
		return "", fmt.Errorf("cli: list sessions: %w", err)
	}
	lower := lowerTrim(prefix)
	for _, s := range sessions {
		if hasLowerPrefix(s.ID, lower) {
			return s.ID, nil
		}
	}
	return "", fmt.Errorf("no session matches %q", prefix)
}
