package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/cfbender/hygge/internal/session"
)

// newResumeCmd builds `hygge resume [prefix]`.
//
// With a prefix argument, resume the most recent session whose id begins
// with that prefix.  Without one, resume the most recent session
// anywhere.  In both cases the matched session id is printed and the
// TUI is launched against it.
func newResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume [id-prefix]",
		Short: "Resume the most recent matching session",
		Args:  cobra.MaximumNArgs(1),
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

			var sid string
			if len(args) == 1 {
				sid, err = findSessionByPrefix(ctx, rt, args[0], false)
				if err != nil {
					return die(cmd, "%s", err)
				}
			} else {
				sessions, err := rt.Store.ListSessions(ctx, session.ListOpts{Limit: 1})
				if err != nil {
					return fmt.Errorf("cli: list sessions: %w", err)
				}
				if len(sessions) == 0 {
					return die(cmd, "no sessions to resume")
				}
				sid = sessions[0].ID
			}
			printf(out(cmd), "resuming %s\n", shortID(sid))

			return runTUI(ctx, cmd, rt, sid)
		},
	}
}
