package cli

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/cfbender/hygge/internal/session"
)

// newSessionsCmd builds the `hygge sessions` subcommand group.
func newSessionsCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "sessions",
		Short: "List, inspect, rename, and delete sessions",
	}
	root.AddCommand(
		newSessionsListCmd(),
		newSessionsShowCmd(),
		newSessionsRenameCmd(),
		newSessionsDeleteCmd(),
	)
	return root
}

// newSessionsListCmd builds `hygge sessions list`.
func newSessionsListCmd() *cobra.Command {
	var (
		here             bool
		includeDeleted   bool
		includeSubagents bool
		limit            int
		query            string
		kind             string
		parentID         string
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
				Query:          query,
				ParentID:       parentID,
			}
			if here {
				opts.ProjectDir = rt.Pwd
			}
			if kind != "" {
				opts.Kind = session.Kind(kind)
			} else if !includeSubagents {
				// Default: hide subagent sessions from top-level listing.
				opts.Kind = session.KindPrimary
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
			writeln(tw, "ID\tCREATED\tMODEL\tTITLE\tLAST USER\tLAST AGENT\tPROJECT\tKIND")
			home := homeDirFromRuntime(rt)
			for _, s := range sessions {
				title := sessionListTitle(s)
				lastUser := sessionListPreview(s.LastUserMessage)
				lastAgent := sessionListPreview(s.LastAgentMessage)
				marker := ""
				if !s.DeletedAt.IsZero() {
					marker = " (deleted)"
				}
				printf(tw, "%s%s\t%s\t%s/%s\t%s\t%s\t%s\t%s\t%s\n",
					shortID(s.ID),
					marker,
					s.CreatedAt.Format("2006-01-02 15:04"),
					s.Model.Provider,
					s.Model.Name,
					title,
					lastUser,
					lastAgent,
					abbreviatePath(s.ProjectDir, home),
					string(s.Kind),
				)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&here, "here", false, "only sessions whose project_dir matches the current pwd")
	cmd.Flags().BoolVar(&includeDeleted, "include-deleted", false, "include soft-deleted sessions")
	cmd.Flags().BoolVar(&includeSubagents, "include-subagents", false, "include subagent sessions (hidden by default)")
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum number of sessions to list")
	cmd.Flags().StringVar(&query, "query", "", "case-insensitive substring filter on slug, project_dir, first message")
	cmd.Flags().StringVar(&kind, "kind", "", "filter by kind: primary | subagent (default: primary unless --include-subagents)")
	cmd.Flags().StringVar(&parentID, "parent", "", "filter to children of this session id")
	return cmd
}

func sessionListTitle(s *session.Session) string {
	if s.Slug != "" {
		return sessionListPreview(s.Slug)
	}
	if s.FirstMessagePreview != "" {
		return sessionListPreview(s.FirstMessagePreview)
	}
	return "-"
}

func sessionListPreview(text string) string {
	if text == "" {
		return "-"
	}
	text = strings.Join(strings.Fields(text), " ")
	const max = 48
	if len(text) <= max {
		return text
	}
	return text[:max-3] + "..."
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
			printf(out(cmd), "kind:         %s\n", string(sess.Kind))
			if sess.Slug != "" {
				printf(out(cmd), "slug:         %s\n", sess.Slug)
			}
			if sess.FirstMessagePreview != "" {
				preview := sess.FirstMessagePreview
				if len(preview) > 60 {
					preview = preview[:57] + "..."
				}
				printf(out(cmd), "first_msg:    %s\n", preview)
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

// newSessionsRenameCmd builds `hygge sessions rename <id-prefix> <slug>`.
func newSessionsRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <id-prefix> <slug>",
		Short: "Set a slug on a session",
		Args:  cobra.ExactArgs(2),
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
			slug := args[1]
			if err := rt.Store.RenameSession(ctx, id, slug); err != nil {
				return fmt.Errorf("cli: rename session: %w", err)
			}
			printf(out(cmd), "renamed %s → %q\n", shortID(id), slug)
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
