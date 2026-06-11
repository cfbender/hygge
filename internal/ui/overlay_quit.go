package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// quitOverlay is the quit confirmation dialog: a centered question with
// selectable yes/no buttons. It owns its selection state; styles come from
// the App's per-theme overlay style cache via the injected supplier.
type quitOverlay struct {
	// selectedNo tracks which button is selected. Openers reset it to true
	// so "no" is the safe default.
	selectedNo bool
	styles     func() *overlayStyles
}

func (o *quitOverlay) Update(msg tea.Msg) (tea.Cmd, bool) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil, false
	}
	switch k.String() {
	case "y", "Y", "ctrl+c":
		return tea.Quit, true
	case "n", "N", "esc":
		return nil, true
	case "left", "right", "tab", "h", "l":
		o.selectedNo = !o.selectedNo
		return nil, false
	case "enter", " ":
		if !o.selectedNo {
			return tea.Quit, true
		}
		return nil, true
	default:
		return nil, false
	}
}

func (o *quitOverlay) View(w, h int) string {
	question := "Are you sure you want to quit?"

	ov := o.styles()

	var yesBtn, noBtn string
	if o.selectedNo {
		yesBtn = ov.quitNormal.Render("yeah")
		noBtn = ov.quitSelected.Render("nah")
	} else {
		yesBtn = ov.quitSelected.Render("yeah")
		noBtn = ov.quitNormal.Render("nah")
	}
	btnSep := ov.quitBgPad.Render(" ")
	buttonRow := yesBtn + btnSep + noBtn

	// Build content manually to avoid JoinVertical centering artifacts.
	// Ensure every line has the box background.
	qText := question
	qW := lipgloss.Width(qText)
	bW := lipgloss.Width(buttonRow)
	innerW := max(bW, qW)

	// Center the button row within the inner width.
	btnPad := max((innerW-bW)/2, 0)
	bgPad := ov.quitBgPad
	centeredButtons := bgPad.Render(strings.Repeat(" ", btnPad)) + buttonRow + bgPad.Render(strings.Repeat(" ", innerW-bW-btnPad))

	// Center the question too.
	qPad := max((innerW-qW)/2, 0)
	centeredQ := bgPad.Render(strings.Repeat(" ", qPad)) + ov.quitQuestion.Render(qText) + bgPad.Render(strings.Repeat(" ", innerW-qW-qPad))

	blankLine := bgPad.Render(strings.Repeat(" ", innerW))

	content := centeredQ + "\n" + blankLine + "\n" + centeredButtons
	box := ov.quitBox.Render(content)

	boxW := lipgloss.Width(box)
	boxH := lipgloss.Height(box)

	padLeft := max((w-boxW)/2, 0)
	padTop := max((h-boxH)/2, 0)

	var lines []string
	for range padTop {
		lines = append(lines, "")
	}
	for line := range strings.SplitSeq(box, "\n") {
		lines = append(lines, strings.Repeat(" ", padLeft)+line)
	}
	return strings.Join(lines, "\n")
}
