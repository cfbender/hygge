package styles

import (
	"image/color"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// quickStyleOpts is the palette of semantic colors used to build a theme.
type quickStyleOpts struct {
	primary   color.Color
	secondary color.Color
	accent    color.Color

	fgBase       color.Color
	fgSubtle     color.Color
	fgMoreSubtle color.Color
	fgMostSubtle color.Color

	onPrimary color.Color

	bgBase         color.Color
	bgLeastVisible color.Color
	bgLessVisible  color.Color
	bgMostVisible  color.Color

	separator color.Color

	destructive       color.Color
	error             color.Color
	warning           color.Color
	warningSubtle     color.Color
	busy              color.Color
	info              color.Color
	infoMoreSubtle    color.Color
	infoMostSubtle    color.Color
	success           color.Color
	successMoreSubtle color.Color
	successMostSubtle color.Color
}

// quickStyle builds a complete Styles from a semantic color palette.
func quickStyle(o quickStyleOpts) Styles {
	var (
		base   = lipgloss.NewStyle().Foreground(o.fgBase)
		muted  = lipgloss.NewStyle().Foreground(o.fgMoreSubtle)
		subtle = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
		s      Styles
	)

	s.Colors = paletteAtoms(o)

	s.Background = o.bgBase
	s.BubbleBg = o.bgLessVisible
	s.SidebarBg = o.bgLessVisible
	s.InputBg = o.bgBase
	s.UserAccent = o.accent

	// Working indicator gradient.
	s.WorkingGradFromColor = o.primary
	s.WorkingGradToColor = o.secondary
	s.WorkingLabelColor = o.fgBase

	// Logo.
	s.Logo.GradFromColor = o.secondary
	s.Logo.GradToColor = o.primary
	s.Logo.AccentColor = o.secondary
	s.Logo.VersionColor = o.primary

	// Text input.
	s.TextInput = textinput.Styles{
		Focused: textinput.StyleState{
			Text:        base,
			Placeholder: base.Foreground(o.fgMostSubtle),
			Prompt:      base.Foreground(o.accent),
			Suggestion:  base.Foreground(o.fgMostSubtle),
		},
		Blurred: textinput.StyleState{
			Text:        base.Foreground(o.fgMoreSubtle),
			Placeholder: base.Foreground(o.fgMostSubtle),
			Prompt:      base.Foreground(o.fgMoreSubtle),
			Suggestion:  base.Foreground(o.fgMostSubtle),
		},
		Cursor: textinput.CursorStyle{
			Color: o.secondary,
			Shape: tea.CursorBlock,
			Blink: true,
		},
	}

	// Editor / textarea.
	s.Editor.Textarea = textarea.Styles{
		Focused: textarea.StyleState{
			Base:             base,
			Text:             base,
			LineNumber:       base.Foreground(o.fgMostSubtle),
			CursorLine:       base,
			CursorLineNumber: base.Foreground(o.fgMostSubtle),
			Placeholder:      base.Foreground(o.fgMostSubtle),
			Prompt:           base.Foreground(o.accent),
		},
		Blurred: textarea.StyleState{
			Base:             base,
			Text:             base.Foreground(o.fgMoreSubtle),
			LineNumber:       base.Foreground(o.fgMoreSubtle),
			CursorLine:       base,
			CursorLineNumber: base.Foreground(o.fgMoreSubtle),
			Placeholder:      base.Foreground(o.fgMostSubtle),
			Prompt:           base.Foreground(o.fgMoreSubtle),
		},
		Cursor: textarea.CursorStyle{
			Color: o.secondary,
			Shape: tea.CursorBlock,
			Blink: true,
		},
	}
	s.Editor.PromptFocused = lipgloss.NewStyle().Foreground(o.successMostSubtle).SetString("::: ")
	s.Editor.PromptBlurred = s.Editor.PromptFocused.Foreground(o.fgMoreSubtle)
	s.Editor.AttachmentIcon = base.Foreground(o.bgLessVisible).Background(o.success).Padding(0, 1)
	s.Editor.AttachmentName = base.Padding(0, 1).MarginRight(1).Background(o.fgMoreSubtle).Foreground(o.fgBase)
	s.Editor.AttachmentDeleting = base.Padding(0, 1).Bold(true).Background(o.destructive).Foreground(o.fgBase)

	// Header.
	s.Header.Accent = base.Foreground(o.secondary)
	s.Header.Separator = subtle
	s.Header.Muted = muted
	s.Header.Wrapper = lipgloss.NewStyle().Foreground(o.fgBase).Background(o.bgLeastVisible)

	// Messages.
	focusedBorder := lipgloss.Border{Left: "▌"}
	s.Messages.NoContent = lipgloss.NewStyle().Foreground(o.fgBase)
	s.Messages.UserBorder = s.Messages.NoContent.PaddingLeft(1).
		BorderLeft(true).BorderForeground(o.primary).BorderStyle(focusedBorder)
	s.Messages.AssistantBorder = s.Messages.NoContent.PaddingLeft(2)
	s.Messages.Thinking = lipgloss.NewStyle().MaxHeight(10)
	s.Messages.ThinkingHint = muted
	s.Messages.ErrorTag = lipgloss.NewStyle().Padding(0, 1).
		Background(o.destructive).Foreground(o.onPrimary)
	s.Messages.ErrorTitle = lipgloss.NewStyle().Foreground(o.fgSubtle)
	s.Messages.ErrorDetails = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	s.Messages.InfoModel = muted
	s.Messages.InfoProvider = subtle
	s.Messages.InfoDuration = subtle
	s.Messages.Canceled = lipgloss.NewStyle().Foreground(o.fgBase).Italic(true)

	// Tool calls.
	s.Tool.IconPending = base.Foreground(o.successMostSubtle).SetString(DotIcon)
	s.Tool.IconSuccess = base.Foreground(o.success).SetString(CheckIcon)
	s.Tool.IconError = base.Foreground(o.error).SetString(ErrorIcon)
	s.Tool.IconCancelled = muted.SetString(DotIcon)
	s.Tool.Name = base.Foreground(o.info)
	s.Tool.ParamKey = subtle
	s.Tool.ParamValue = subtle
	s.Tool.ContentLine = muted.Background(o.bgLeastVisible)
	s.Tool.ContentTrunc = muted.Background(o.bgLeastVisible)
	s.Tool.Body = base.PaddingLeft(2)
	s.Tool.ErrorTag = base.Padding(0, 1).Background(o.destructive).Foreground(o.onPrimary)
	s.Tool.ErrorMessage = base.Foreground(o.fgSubtle)
	s.Tool.ActionCreate = lipgloss.NewStyle().Foreground(o.successMoreSubtle)
	s.Tool.ActionDestroy = lipgloss.NewStyle().Foreground(o.destructive)

	// Sidebar.
	s.Sidebar.Border = lipgloss.NewStyle().Foreground(o.separator)
	s.Sidebar.Section = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	s.Sidebar.Value = lipgloss.NewStyle().Foreground(o.fgBase)
	s.Sidebar.Accent = lipgloss.NewStyle().Foreground(o.accent)
	s.Sidebar.Muted = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	s.Sidebar.Background = lipgloss.NewStyle().Background(o.bgLeastVisible)
	s.Sidebar.SessionTitle = lipgloss.NewStyle().Foreground(o.fgMoreSubtle)
	s.Sidebar.Path = lipgloss.NewStyle().Foreground(o.fgMoreSubtle)
	s.Sidebar.Additions = lipgloss.NewStyle().Foreground(o.successMostSubtle)
	s.Sidebar.Deletions = lipgloss.NewStyle().Foreground(o.error)
	s.Sidebar.TruncHint = lipgloss.NewStyle().Foreground(o.fgMostSubtle)

	// Model info.
	s.ModelInfo.Icon = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	s.ModelInfo.Name = lipgloss.NewStyle().Foreground(o.fgBase)
	s.ModelInfo.Provider = lipgloss.NewStyle().Foreground(o.fgMoreSubtle)
	s.ModelInfo.Reasoning = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	s.ModelInfo.Tokens = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	s.ModelInfo.Cost = lipgloss.NewStyle().Foreground(o.fgMoreSubtle)

	// Resources (MCP, plugins).
	s.Resource.Heading = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	s.Resource.Name = lipgloss.NewStyle().Foreground(o.fgMoreSubtle)
	s.Resource.Status = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	s.Resource.OnlineIcon = lipgloss.NewStyle().Foreground(o.successMostSubtle).SetString("●")
	s.Resource.ErrorIcon = lipgloss.NewStyle().Foreground(o.destructive).SetString("●")
	s.Resource.BusyIcon = lipgloss.NewStyle().Foreground(o.busy).SetString("●")
	s.Resource.Count = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	s.Resource.More = lipgloss.NewStyle().Foreground(o.fgMostSubtle)

	// Dialogs / modals.
	s.Dialog.Title = base.Padding(0, 1).Foreground(o.primary)
	s.Dialog.TitleAccent = base.Foreground(o.success).Bold(true)
	s.Dialog.View = base.Border(lipgloss.RoundedBorder()).BorderForeground(o.primary)
	s.Dialog.Primary = base.Padding(0, 1).Foreground(o.primary)
	s.Dialog.Secondary = base.Padding(0, 1).Foreground(o.fgMostSubtle)
	s.Dialog.NormalItem = base.Padding(0, 1).Foreground(o.fgBase)
	s.Dialog.SelectedItem = base.Padding(0, 1).Background(o.primary).Foreground(o.onPrimary)
	s.Dialog.HelpKey = base.Foreground(o.fgMoreSubtle)
	s.Dialog.HelpDesc = base.Foreground(o.fgMostSubtle)
	s.Dialog.HelpSep = base.Foreground(o.separator)
	s.Dialog.ContentPanel = base.Background(o.bgLessVisible).Foreground(o.fgBase).Padding(1, 2)
	s.Dialog.Spinner = base.Foreground(o.secondary)
	s.Dialog.TitleGradFrom = o.primary
	s.Dialog.TitleGradTo = o.secondary

	// Pills.
	s.Pills.Base = base.Padding(0, 1)
	s.Pills.Focused = base.Padding(0, 1).BorderStyle(lipgloss.RoundedBorder()).BorderForeground(o.bgMostVisible)
	s.Pills.Blurred = base.Padding(0, 1).BorderStyle(lipgloss.HiddenBorder())
	s.Pills.Label = lipgloss.NewStyle().Foreground(o.fgBase)
	s.Pills.Progress = lipgloss.NewStyle().Foreground(o.fgMoreSubtle)
	s.Pills.Muted = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	s.Pills.HelpKey = lipgloss.NewStyle().Foreground(o.fgMoreSubtle)
	s.Pills.HelpText = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	s.Pills.Area = base
	s.Pills.QueueGradFrom = o.error
	s.Pills.QueueGradTo = o.secondary

	// Breadcrumb.
	s.Breadcrumb.Segment = muted
	s.Breadcrumb.Separator = subtle
	s.Breadcrumb.Active = base.Foreground(o.fgBase)

	// Section.
	s.Section.Title = subtle
	s.Section.Line = base.Foreground(o.separator)

	// Completions.
	s.Completions.Normal = base.Background(o.bgLessVisible).Foreground(o.fgBase)
	s.Completions.Focused = base.Background(o.primary).Foreground(o.onPrimary)
	s.Completions.Match = base.Underline(true)

	// Status.
	s.Status.Success = base.Foreground(o.success)
	s.Status.Error = base.Foreground(o.error)
	s.Status.Warning = base.Foreground(o.warning)
	s.Status.Info = base.Foreground(o.info)

	return s
}
