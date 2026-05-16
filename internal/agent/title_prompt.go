package agent

import (
	"strings"
	"unicode/utf8"

	"github.com/cfbender/hygge/internal/session"
)

const titleSystemInstruction = `You format Hygge coding session titles.

Return exactly one line and nothing else.

Rules:
- Prefer 2-5 words.
- Summarize the actual topic, not the user's wording.
- Remove filler such as "please", "generate", "high level", "in this project".
- Never copy a user message verbatim.
- If the current title still fits the recent conversation, return KEEP.
- Use natural title text, not a filename, command, quoted prompt, or sentence.

Examples:
User: generate a high level overview of / commands in this project
Title: Commands overview

User: fix the click to expand on bash tool blocks
Title: Tool block expansion

User: work through TODOS.md
Title: TODO implementation`

type titleMessage struct {
	role    string
	content string
}

func titlePrompt(currentTitle string, messages []titleMessage) string {
	if len(messages) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(titleSystemInstruction)
	b.WriteString("\n\nCurrent title: ")
	if strings.TrimSpace(currentTitle) == "" {
		b.WriteString("(none)")
	} else {
		b.WriteString(currentTitle)
	}
	b.WriteString("\n\nRecent conversation:\n")
	for _, msg := range recentTitleMessages(messages, 12) {
		content := strings.TrimSpace(msg.content)
		if content == "" {
			continue
		}
		b.WriteString(msg.role)
		b.WriteString(": ")
		b.WriteString(limitString(content, 700))
		b.WriteByte('\n')
	}
	return b.String()
}

func titleRepairPrompt(currentTitle string, messages []titleMessage, rejected string) string {
	base := titlePrompt(currentTitle, messages)
	if base == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\nPrevious candidate title was rejected because it copied user wording instead of summarizing it.\n")
	b.WriteString("Rejected title: ")
	b.WriteString(limitString(cleanModelTitle(rejected), 160))
	b.WriteString("\nReturn a different 2-5 word topic title. Do not return KEEP. Do not copy any user message verbatim.\n")
	return b.String()
}

func titleMessagesFromSession(msgs []*session.Message) []titleMessage {
	out := make([]titleMessage, 0, len(msgs))
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		role := ""
		switch msg.Role {
		case session.RoleUser:
			role = "User"
		case session.RoleAssistant:
			role = "Assistant"
		default:
			continue
		}
		text := textFromParts(msg.Parts)
		if strings.TrimSpace(text) == "" {
			continue
		}
		out = append(out, titleMessage{role: role, content: text})
	}
	return out
}

func textFromParts(parts []session.Part) string {
	var b strings.Builder
	for _, part := range parts {
		if part.Kind != session.PartText || strings.TrimSpace(part.Text) == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(part.Text)
	}
	return b.String()
}

func recentTitleMessages(messages []titleMessage, limit int) []titleMessage {
	if limit <= 0 || len(messages) <= limit {
		return messages
	}
	return messages[len(messages)-limit:]
}

func cleanModelTitle(title string) string {
	title = strings.TrimSpace(title)
	title = strings.Trim(title, " \t\n\r\"'`“”‘’")
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		return ""
	}
	return limitString(title, 80)
}

func titleCopiesUserMessage(title string, messages []titleMessage) bool {
	title = normalizeTitleComparison(title)
	if title == "" {
		return false
	}
	for _, msg := range messages {
		if msg.role != "User" {
			continue
		}
		if title == normalizeTitleComparison(msg.content) {
			return true
		}
	}
	return false
}

func normalizeTitleComparison(s string) string {
	return strings.ToLower(cleanModelTitle(s))
}

func limitString(s string, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	if maxRunes == 1 {
		return "…"
	}
	return string(runes[:maxRunes-1]) + "…"
}
