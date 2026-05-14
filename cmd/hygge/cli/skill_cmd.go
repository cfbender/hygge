package cli

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/cfbender/hygge/internal/skill"
)

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

			all := rt.Skills.All()
			if len(all) == 0 {
				writeln(out(cmd), "(no skills loaded)")
				return nil
			}
			tw := tabwriter.NewWriter(out(cmd), 0, 0, 2, ' ', 0)
			writeln(tw, "NAME\tSOURCE\tPATH\tDESCRIPTION")
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
			printf(out(cmd), "name:        %s\n", sk.Name)
			printf(out(cmd), "source:      %s\n", sk.Source.String())
			printf(out(cmd), "path:        %s\n", sk.Path)
			printf(out(cmd), "description: %s\n", sk.Description)
			printf(out(cmd), "when_to_use: %s\n", sk.WhenToUse)
			if len(sk.Extras) > 0 {
				printf(out(cmd), "extras:\n")
				keys := make([]string, 0, len(sk.Extras))
				for k := range sk.Extras {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					printf(out(cmd), "  %s = %q\n", k, sk.Extras[k])
				}
			}
			printf(out(cmd), "\n---\n%s\n", sk.Body)
			return nil
		},
	}
}

// newSkillsDoctorCmd builds `hygge skills doctor`.  It walks the four
// skill-discovery directories and reports any files that failed to
// load and why.
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
				{filepath.Join(home, ".agents", "skills"), "user/.agents"},
				{filepath.Join(xdgConfig, "hygge", "skills"), "user/hygge"},
			}
			// Project layers — we don't replicate the walk-up here; we
			// just check the immediate paths under pwd.  Doctor is
			// best-effort guidance.
			if pwd != "" {
				dirs = append(dirs,
					dirSpec{filepath.Join(pwd, ".agents", "skills"), "project/.agents"},
					dirSpec{filepath.Join(pwd, ".hygge", "skills"), "project/hygge"},
				)
			}

			problems := 0
			tw := tabwriter.NewWriter(out(cmd), 0, 0, 2, ' ', 0)
			writeln(tw, "STATUS\tSOURCE\tPATH\tDETAIL")
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
				writeln(out(cmd), "\nno problems detected")
			} else {
				printf(out(cmd), "\n%d issue(s) detected\n", problems)
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
