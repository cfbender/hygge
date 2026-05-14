package cli

import (
	"context"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/cfbender/hygge/internal/session"
)

// newSessionsCmd builds the `hygge sessions` subcommand group.
func newSessionsCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "sessions",
		Short: "List, inspect, and delete sessions",
	}
	root.AddCommand(newSessionsListCmd(), newSessionsShowCmd(), newSessionsDeleteCmd())
	return root
}

// newSessionsListCmd builds `hygge sessions list`.
func newSessionsListCmd() *cobra.Command {
	var (
		here           bool
		includeDeleted bool
		limit          int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent sessions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rt, err := bootstrap(context.Background(), bootstrapOptions{
				ConfigFile:      rootFlags.ConfigFile,
				ProfileName:     rootFlags.Profile,
				Pwd:             rootFlags.Pwd,
				ProviderFactory: stubProviderFactory,
			})
			if err != nil {
				return err
			}
			defer func() { _ = rt.Close() }()

			opts := session.ListOpts{
				IncludeDeleted: includeDeleted,
				Limit:          limit,
			}
			if here {
				opts.ProjectDir = rt.Pwd
			}
			sessions, err := rt.Store.ListSessions(context.Background(), opts)
			if err != nil {
				return fmt.Errorf("cli: list sessions: %w", err)
			}
			if len(sessions) == 0 {
				writeln(out(cmd), "no sessions")
				return nil
			}
			tw := tabwriter.NewWriter(out(cmd), 0, 0, 2, ' ', 0)
			writeln(tw, "ID\tCREATED\tMODEL\tSLUG\tPROJECT")
			home := homeDirFromRuntime(rt)
			for _, s := range sessions {
				slug := s.Slug
				if slug == "" {
					slug = "-"
				}
				marker := ""
				if !s.DeletedAt.IsZero() {
					marker = " (deleted)"
				}
				printf(tw, "%s%s\t%s\t%s/%s\t%s\t%s\n",
					shortID(s.ID),
					marker,
					s.CreatedAt.Format("2006-01-02 15:04"),
					s.Model.Provider,
					s.Model.Name,
					slug,
					abbreviatePath(s.ProjectDir, home),
				)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&here, "here", false, "only sessions whose project_dir matches the current pwd")
	cmd.Flags().BoolVar(&includeDeleted, "include-deleted", false, "include soft-deleted sessions")
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum number of sessions to list")
	return cmd
}

// newSessionsShowCmd builds `hygge sessions show <id-prefix>`.
func newSessionsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <id-prefix>",
		Short: "Show a session's metadata and message count",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := bootstrap(context.Background(), bootstrapOptions{
				ConfigFile:      rootFlags.ConfigFile,
				ProfileName:     rootFlags.Profile,
				Pwd:             rootFlags.Pwd,
				ProviderFactory: stubProviderFactory,
			})
			if err != nil {
				return err
			}
			defer func() { _ = rt.Close() }()

			ctx := context.Background()
			id, err := findSessionByPrefix(ctx, rt, args[0], true)
			if err != nil {
				return die(cmd, "%s", err)
			}
			sess, err := rt.Store.GetSession(ctx, id)
			if err != nil {
				return fmt.Errorf("cli: get session: %w", err)
			}
			msgs, err := rt.Store.MessagesForSession(ctx, id)
			if err != nil {
				return fmt.Errorf("cli: messages for session: %w", err)
			}

			home := homeDirFromRuntime(rt)
			printf(out(cmd), "id:           %s\n", sess.ID)
			printf(out(cmd), "created:      %s\n", sess.CreatedAt.Format("2006-01-02 15:04:05"))
			printf(out(cmd), "updated:      %s\n", sess.UpdatedAt.Format("2006-01-02 15:04:05"))
			printf(out(cmd), "model:        %s/%s\n", sess.Model.Provider, sess.Model.Name)
			printf(out(cmd), "project_dir:  %s\n", abbreviatePath(sess.ProjectDir, home))
			if sess.Slug != "" {
				printf(out(cmd), "slug:         %s\n", sess.Slug)
			}
			if sess.ParentID != "" {
				printf(out(cmd), "parent:       %s (forked at %s)\n", shortID(sess.ParentID), shortID(sess.ForkMessageID))
			}
			if !sess.DeletedAt.IsZero() {
				printf(out(cmd), "deleted_at:   %s\n", sess.DeletedAt.Format("2006-01-02 15:04:05"))
			}
			printf(out(cmd), "messages:     %d\n", len(msgs))
			printf(out(cmd), "input_tokens: %d\n", sess.Totals.InputTokens)
			printf(out(cmd), "output_tokens:%d\n", sess.Totals.OutputTokens)
			printf(out(cmd), "cost_usd:     $%.4f\n", sess.Totals.CostUSD)
			return nil
		},
	}
}

// newSessionsDeleteCmd builds `hygge sessions delete <id-prefix>`.
func newSessionsDeleteCmd() *cobra.Command {
	var force bool
	var noConfirm bool
	cmd := &cobra.Command{
		Use:   "delete <id-prefix>",
		Short: "Soft-delete a session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := bootstrap(context.Background(), bootstrapOptions{
				ConfigFile:      rootFlags.ConfigFile,
				ProfileName:     rootFlags.Profile,
				Pwd:             rootFlags.Pwd,
				ProviderFactory: stubProviderFactory,
			})
			if err != nil {
				return err
			}
			defer func() { _ = rt.Close() }()

			ctx := context.Background()
			id, err := findSessionByPrefix(ctx, rt, args[0], false)
			if err != nil {
				return die(cmd, "%s", err)
			}
			if !force && !noConfirm {
				return die(cmd, "refusing to delete without -f or --no-confirm")
			}
			if err := rt.Store.SoftDeleteSession(ctx, id); err != nil {
				return fmt.Errorf("cli: soft-delete: %w", err)
			}
			printf(out(cmd), "deleted %s\n", shortID(id))
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "skip the confirmation prompt")
	cmd.Flags().BoolVar(&noConfirm, "no-confirm", false, "skip the confirmation prompt (alias of -f for unattended use)")
	return cmd
}
