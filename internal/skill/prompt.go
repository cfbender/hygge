package skill

// BuildSystemPromptAdditions returns the text to APPEND to the base
// system prompt so the model knows what skills exist. Returns an empty
// string when the registry is nil or has no skills.
func BuildSystemPromptAdditions(r *Registry) string {
	if r == nil || r.Len() == 0 {
		return ""
	}
	return "## Available skills\n\n" +
		"Skills provide specialized instructions and workflows for specific tasks.\n" +
		"Use the skill tool to load a skill when a task matches its description.\n\n" +
		FormatAvailableVerbose(r)
}
