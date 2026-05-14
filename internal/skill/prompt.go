package skill

import "strings"

// BuildSystemPromptAdditions returns the text to APPEND to the base
// system prompt so the model knows what skills exist.  Returns an empty
// string when the registry is nil or has no skills.
//
// The output is a stable markdown block tests assert on verbatim:
//
//	## Available skills
//
//	Skills provide specialized instructions for specific tasks. Load a
//	skill by name via the `skill` tool when the task matches its description.
//
//	- <name>: <description>
//	  <when_to_use>
//	- ...
//
// The skills are listed sorted by name for determinism.
func BuildSystemPromptAdditions(r *Registry) string {
	if r == nil || r.Len() == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Available skills\n\n")
	b.WriteString("Skills provide specialized instructions for specific tasks. Load a ")
	b.WriteString("skill by name via the `skill` tool when the task matches its description.\n\n")
	for _, sk := range r.All() {
		b.WriteString("- ")
		b.WriteString(sk.Name)
		b.WriteString(": ")
		b.WriteString(sk.Description)
		b.WriteByte('\n')
		b.WriteString("  ")
		b.WriteString(sk.WhenToUse)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}
