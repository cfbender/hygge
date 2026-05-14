package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/cfbender/hygge/internal/skill"
)

// skillTool implements the "skill" built-in: it returns the full body
// of a named skill so the model can act on its instructions.  Skills
// are loaded into memory at bootstrap time; the tool just looks them
// up.  No filesystem side-effects happen here, so the tool does NOT
// gate on permission.
type skillTool struct {
	registry *skill.Registry
}

// NewSkillTool builds a skillTool backed by reg.  reg may be nil; in
// that case the tool always returns an IsError result with a
// "no skills configured" message.
func NewSkillTool(reg *skill.Registry) Tool {
	return &skillTool{registry: reg}
}

func (t *skillTool) Name() string { return "skill" }

// Parallelizable returns true: the skill tool reads from an in-memory
// registry with no mutation, making it safe to run concurrently with
// other read-only tools.
func (t *skillTool) Parallelizable() bool { return true }

func (t *skillTool) Description() string {
	return "Load the full body of a named skill. Use this when one of the listed available skills applies to the current task."
}

func (t *skillTool) InputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"name"},
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "The name of the skill to load.",
			},
		},
	}
}

type skillArgs struct {
	Name string `json:"name"`
}

func (t *skillTool) Execute(_ context.Context, raw json.RawMessage, _ ExecContext) (Result, error) {
	var a skillArgs
	if err := decodeArgs(raw, &a); err != nil {
		return Result{}, err
	}
	a.Name = strings.TrimSpace(a.Name)
	if a.Name == "" {
		return Result{}, newInvalidArgs("name is required", nil)
	}

	if t.registry == nil || t.registry.Len() == 0 {
		return Result{
			IsError: true,
			Content: "no skills configured. Place skill files under .agents/skills/, " +
				".hygge/skills/, ~/.agents/skills/, or ~/.config/hygge/skills/.",
			Metadata: map[string]any{
				"error": "no_skills_configured",
				"name":  a.Name,
			},
		}, nil
	}

	sk, ok := t.registry.Get(a.Name)
	if !ok {
		available := availableSkillNames(t.registry)
		msg := fmt.Sprintf("skill %q not found. Available skills: %s", a.Name, formatNames(available))
		return Result{
			IsError: true,
			Content: msg,
			Metadata: map[string]any{
				"error":     "skill_not_found",
				"name":      a.Name,
				"available": available,
			},
		}, nil
	}

	// Prepend a one-line header pointing at the skill's directory so
	// the model can resolve relative paths inside the body (script
	// blocks, references, etc.).  The header is plain text so it does
	// not interfere with markdown rendering.
	content := sk.Body
	if sk.Dir != "" {
		content = fmt.Sprintf("Skill directory: %s\n\n%s", sk.Dir, sk.Body)
	}

	return Result{
		Content: content,
		Metadata: map[string]any{
			"name":   sk.Name,
			"path":   sk.Path,
			"dir":    sk.Dir,
			"source": sk.Source.String(),
		},
	}, nil
}

// availableSkillNames returns every loaded skill name, sorted.
func availableSkillNames(reg *skill.Registry) []string {
	all := reg.All()
	names := make([]string, 0, len(all))
	for _, sk := range all {
		names = append(names, sk.Name)
	}
	sort.Strings(names)
	return names
}

// formatNames renders a comma-separated list, or "(none)" when empty.
func formatNames(names []string) string {
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}
