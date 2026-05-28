package cli

import (
	"image/color"
	"io"
	"strings"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"
)

// CLI colors use ANSI palette indexes so command output follows the user's
// terminal theme instead of forcing fixed RGB values.
func cliHeaderColor() color.Color        { return lipgloss.Color("4") }
func cliAccentColor() color.Color        { return lipgloss.Color("5") }
func cliInfoColor() color.Color          { return lipgloss.Color("6") }
func cliMutedColor() color.Color         { return lipgloss.Color("8") }
func cliValueColor() color.Color         { return lipgloss.Color("7") }
func cliSuccessColor() color.Color       { return lipgloss.Color("2") }
func cliWarnColor() color.Color          { return lipgloss.Color("3") }
func cliSelectedColor() color.Color      { return lipgloss.Color("15") }
func cliSelectedMutedColor() color.Color { return lipgloss.Color("7") }

func cliFangColorScheme(lipgloss.LightDarkFunc) fang.ColorScheme {
	return fang.ColorScheme{
		Base:           cliValueColor(),
		Title:          cliHeaderColor(),
		Description:    cliValueColor(),
		Codeblock:      nil,
		Program:        cliAccentColor(),
		DimmedArgument: cliMutedColor(),
		Comment:        cliMutedColor(),
		Flag:           cliWarnColor(),
		FlagDefault:    cliMutedColor(),
		Command:        cliInfoColor(),
		QuotedString:   cliSuccessColor(),
		Argument:       cliValueColor(),
		Help:           cliValueColor(),
		Dash:           cliMutedColor(),
		ErrorHeader:    [2]color.Color{cliSelectedColor(), cliWarnColor()},
		ErrorDetails:   cliWarnColor(),
	}
}

type cliStyles struct {
	Title   lipgloss.Style
	Header  lipgloss.Style
	Label   lipgloss.Style
	Value   lipgloss.Style
	Accent  lipgloss.Style
	Info    lipgloss.Style
	Muted   lipgloss.Style
	Success lipgloss.Style
	Warn    lipgloss.Style
}

func newCLIStylesFor(w io.Writer) cliStyles {
	plain := lipgloss.NewStyle()
	if !isColorWriter(w) {
		return cliStyles{
			Title:   plain,
			Header:  plain,
			Label:   plain,
			Value:   plain,
			Accent:  plain,
			Info:    plain,
			Muted:   plain,
			Success: plain,
			Warn:    plain,
		}
	}
	return cliStyles{
		Title:   lipgloss.NewStyle().Bold(true).Underline(true).Foreground(cliHeaderColor()),
		Header:  lipgloss.NewStyle().Bold(true).Foreground(cliHeaderColor()),
		Label:   lipgloss.NewStyle().Bold(true).Foreground(cliHeaderColor()),
		Value:   lipgloss.NewStyle().Foreground(cliValueColor()),
		Accent:  lipgloss.NewStyle().Foreground(cliAccentColor()),
		Info:    lipgloss.NewStyle().Foreground(cliInfoColor()),
		Muted:   lipgloss.NewStyle().Foreground(cliMutedColor()),
		Success: lipgloss.NewStyle().Foreground(cliSuccessColor()),
		Warn:    lipgloss.NewStyle().Foreground(cliWarnColor()),
	}
}

func cliPadRight(s string, width int) string {
	padding := width - lipgloss.Width(s)
	if padding <= 0 {
		return s
	}
	return s + strings.Repeat(" ", padding)
}
