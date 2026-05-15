package cli

import (
	"regexp"

	tea "charm.land/bubbletea/v2"
)

// oscResponsePattern matches the inner content of OSC 10/11/12/... terminal
// capability responses that some terminals surface as raw key-press text.
//
// Format: <num>;rgb:<hex>[/<hex>/<hex>]
// Examples matched:
//   - "11;rgb:1818/0808/1010"  (OSC 11 background, 4-digit hex)
//   - "10;rgb:ffff/ffff/ffff"  (OSC 10 foreground, full white)
//   - "11;rgb:18/08/10"        (short 2-digit hex form)
//
// The pattern requires an integer prefix before ";rgb:" so it does not catch
// user text that merely contains "rgb:" somewhere.
//
// See docs/agents/ui-v2-gotchas.md and the note in run.go about bubbletea
// v2.0.6's OSC parser; remove this filter if upstream fixes parsing.
var oscResponsePattern = regexp.MustCompile(`^\d+;rgb:[0-9a-fA-F]+(?:/[0-9a-fA-F]+){2}$`)

// dropOSCResponses is a bubbletea v2 filter function (see tea.WithFilter).
// It drops KeyPressMsg events whose Text field matches the pattern of a
// terminal OSC background/foreground color response that leaked through
// bubbletea v2.0.6's input parser.  All other messages pass through unchanged.
func dropOSCResponses(_ tea.Model, msg tea.Msg) tea.Msg {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		if oscResponsePattern.MatchString(k.Text) {
			return nil
		}
	}
	return msg
}
