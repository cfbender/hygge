package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/cfbender/hygge/internal/skill"
)

// inspectStyles holds Lip Gloss styles shared by the inspection/list
// commands: skills, subagents, commands, and context.  Kept in a single
// struct so the palette is consistent across all four command groups.
type inspectStyles struct {
	Header lipgloss.Style // bold column headers
	Label  lipgloss.Style // left-hand "field:" labels in show commands
	Value  lipgloss.Style // right-hand values in show commands
	Muted  lipgloss.Style // empty-state notices and secondary info
	Path   lipgloss.Style // file system paths
	OK     lipgloss.Style // "ok" status badge
	Warn   lipgloss.Style // "skipped"/"error" status badge
}

// newInspectStylesFor returns full ANSI styles when w is a TTY and
// NO_COLOR is not set; otherwise returns plain no-op styles so that
// piped / redirected output and scripts are never polluted with escape
// sequences.
//
// Tabwriter tables must always use the plain variant (pass a non-*os.File
// writer, e.g. the tabwriter itself) because ANSI bytes inside tab cells
// break column alignment.  Only "show" commands that write one field per
// line should pass the real command writer.
func newInspectStylesFor(w io.Writer) inspectStyles {
	styles := newCLIStylesFor(w)
	label := styles.Label
	if isColorWriter(w) {
		label = label.Width(13)
	}
	return inspectStyles{
		Header: styles.Header,
		Label:  label,
		Value:  styles.Value,
		Muted:  styles.Muted,
		Path:   styles.Accent,
		OK:     styles.Success,
		Warn:   styles.Warn,
	}
}

// isColorWriter reports whether w is a terminal that supports ANSI colour.
// Returns false when w is not an *os.File, when the file descriptor is not
// a terminal, or when NO_COLOR is set (https://no-color.org).
func isColorWriter(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// newSkillsCmd builds the `hygge skills` subcommand group.
func newSkillsCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "skills",
		Short: "List, inspect, and diagnose loaded skills",
	}
	root.AddCommand(newSkillsListCmd(), newSkillsShowCmd(), newSkillsDoctorCmd())
	return root
}

// newSkillsListCmd builds `hygge skills list`.
func newSkillsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every loaded skill with its source path",
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

			// empty-state: styled when TTY, plain otherwise.
			sty := newInspectStylesFor(out(cmd))
			all := rt.Skills.All()
			if len(all) == 0 {
				writeln(out(cmd), sty.Muted.Render("(no skills loaded)"))
				return nil
			}
			// Tabwriter: always plain — ANSI bytes inside tab cells
			// corrupt column alignment regardless of terminal type.
			tw := tabwriter.NewWriter(out(cmd), 0, 0, 2, ' ', 0)
			printf(tw, "NAME\tSOURCE\tPATH\tDESCRIPTION\n")
			home := homeDirFromRuntime(rt)
			for _, sk := range all {
				printf(tw, "%s\t%s\t%s\t%s\n",
					sk.Name,
					sk.Source.String(),
					abbreviatePath(sk.Path, home),
					truncateInline(sk.Description, 60),
				)
			}
			return tw.Flush()
		},
	}
}

// newSkillsShowCmd builds `hygge skills show <name>`.
func newSkillsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Print the full body of a single skill",
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

			name := args[0]
			sk, ok := rt.Skills.Get(name)
			if !ok {
				return die(cmd, "no skill named %q (use `hygge skills list` to see what is loaded)", name)
			}
			sty := newInspectStylesFor(out(cmd))
			printf(out(cmd), "%s %s\n", sty.Label.Render("name:"), sty.Value.Render(sk.Name))
			printf(out(cmd), "%s %s\n", sty.Label.Render("source:"), sty.Value.Render(sk.Source.String()))
			printf(out(cmd), "%s %s\n", sty.Label.Render("path:"), sty.Path.Render(sk.Path))
			printf(out(cmd), "%s %s\n", sty.Label.Render("description:"), sk.Description)
			printf(out(cmd), "%s %s\n", sty.Label.Render("when_to_use:"), sk.WhenToUse)
			if len(sk.Extras) > 0 {
				printf(out(cmd), "%s\n", sty.Label.Render("extras:"))
				keys := make([]string, 0, len(sk.Extras))
				for k := range sk.Extras {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					printf(out(cmd), "  %s = %q\n", sty.Value.Render(k), sk.Extras[k])
				}
			}
			printf(out(cmd), "\n%s\n%s\n", sty.Muted.Render("---"), sk.Body)
			return nil
		},
	}
}

// newSkillsDoctorCmd builds `hygge skills doctor`. It walks the primary
// skill-discovery directories and reports any files that failed to load and why.
func newSkillsDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose files in skill directories that failed to load",
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

			home := homeDirFromRuntime(rt)
			xdgConfig := xdgConfigFromRuntime(rt)
			pwd := rt.Pwd

			type dirSpec struct {
				path   string
				source string
			}
			dirs := []dirSpec{
				{filepath.Join(home, ".claude", "skills"), "user/.claude"},
				{filepath.Join(home, ".agents", "skills"), "user/.agents"},
				{filepath.Join(xdgConfig, "hygge", "skills"), "user/hygge"},
			}
			// Project layers — we don't replicate the walk-up here; we
			// just check the immediate paths under pwd. Doctor is
			// best-effort guidance.
			if pwd != "" {
				dirs = append(dirs,
					dirSpec{filepath.Join(pwd, ".claude", "skills"), "project/.claude"},
					dirSpec{filepath.Join(pwd, ".agents", "skills"), "project/.agents"},
					dirSpec{filepath.Join(pwd, ".hygge", "skills"), "project/hygge"},
				)
			}

			// Non-table styled output (empty-state / footer): TTY-aware.
			sty := newInspectStylesFor(out(cmd))
			problems := 0
			// Tabwriter: always plain to preserve column alignment.
			tw := tabwriter.NewWriter(out(cmd), 0, 0, 2, ' ', 0)
			printf(tw, "STATUS\tSOURCE\tPATH\tDETAIL\n")
			for _, d := range dirs {
				entries, err := os.ReadDir(d.path)
				if err != nil {
					if !os.IsNotExist(err) {
						printf(tw, "error\t%s\t%s\tcannot read dir: %v\n",
							d.source, abbreviatePath(d.path, home), err)
						problems++
					}
					continue
				}
				for _, e := range entries {
					if e.IsDir() {
						continue
					}
					name := e.Name()
					if !strings.HasSuffix(name, ".md") {
						continue
					}
					full := filepath.Join(d.path, name)
					sk, perr := skill.ParseFile(full)
					if perr != nil {
						msg := perr.Error()
						printf(tw, "skipped\t%s\t%s\t%s\n",
							d.source, abbreviatePath(full, home), truncateInline(msg, 80))
						problems++
						continue
					}
					printf(tw, "ok\t%s\t%s\t%s\n",
						d.source, abbreviatePath(full, home), truncateInline(sk.Description, 60))
				}
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			if problems == 0 {
				writeln(out(cmd), "\n"+sty.OK.Render("no problems detected"))
			} else {
				printf(out(cmd), "\n%s\n", sty.Warn.Render(fmt.Sprintf("%d issue(s) detected", problems)))
			}
			return nil
		},
	}
}

// xdgConfigFromRuntime mirrors the resolution order in bootstrap so
// the doctor command points at the same paths the loader consulted.
func xdgConfigFromRuntime(rt *appRuntime) string {
	if testOverrides != nil && testOverrides.XDGConfigHome != "" {
		return testOverrides.XDGConfigHome
	}
	if v, ok := os.LookupEnv("XDG_CONFIG_HOME"); ok && v != "" {
		return v
	}
	return filepath.Join(homeDirFromRuntime(rt), ".config")
}

// truncateInline collapses a string to a single line and clips it to
// limit runes with an ellipsis.  Used by table-style output where
// multi-line descriptions would break the layout.
func truncateInline(s string, limit int) string {
	one := strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", "")
	if limit <= 0 {
		return one
	}
	runes := []rune(one)
	if len(runes) <= limit {
		return one
	}
	if limit < 4 {
		return string(runes[:limit])
	}
	return string(runes[:limit-1]) + "…"
}
