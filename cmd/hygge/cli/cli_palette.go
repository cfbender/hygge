package cli

import (
	"image/color"

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
