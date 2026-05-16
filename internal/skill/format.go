package skill

import "strings"

// FormatAvailable renders the loaded skill index in the compact markdown shape
// used by tool descriptions and error messages.
func FormatAvailable(r *Registry) string {
	if r == nil || r.Len() == 0 {
		return "No skills are currently available."
	}
	var b strings.Builder
	b.WriteString("## Available Skills")
	for _, sk := range r.All() {
		b.WriteString("\n- **")
		b.WriteString(sk.Name)
		b.WriteString("**: ")
		b.WriteString(sk.Description)
	}
	return b.String()
}

// FormatAvailableVerbose renders the loaded skill index for the system prompt.
// The XML-ish structure mirrors OpenCode and gives models clear fields to match
// a task against before using the skill tool.
func FormatAvailableVerbose(r *Registry) string {
	if r == nil || r.Len() == 0 {
		return "No skills are currently available."
	}
	var b strings.Builder
	b.WriteString("<available_skills>")
	for _, sk := range r.All() {
		b.WriteString("\n  <skill>")
		b.WriteString("\n    <name>")
		b.WriteString(sk.Name)
		b.WriteString("</name>")
		b.WriteString("\n    <description>")
		b.WriteString(sk.Description)
		b.WriteString("</description>")
		if strings.TrimSpace(sk.WhenToUse) != "" {
			b.WriteString("\n    <when_to_use>")
			b.WriteString(sk.WhenToUse)
			b.WriteString("</when_to_use>")
		}
		if sk.Path != "" {
			b.WriteString("\n    <location>")
			b.WriteString(sk.Path)
			b.WriteString("</location>")
		}
		b.WriteString("\n  </skill>")
	}
	b.WriteString("\n</available_skills>")
	return b.String()
}
