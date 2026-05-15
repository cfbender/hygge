// Package styles defines the comprehensive style system for Hygge's terminal UI.
//
// Styles are built from a semantic color palette (quickStyleOpts) that maps
// abstract roles (primary, accent, error, etc.) to concrete hex colors. This
// allows themes to be created by swapping the palette while keeping the style
// structure stable.
package styles

import (
	"image/color"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
)

const (
	CheckIcon  = "✓"
	ErrorIcon  = "×"
	DotIcon    = "●"
	ArrowIcon  = "→"
	SpinnerDot = "⋯"

	BorderThin  = "│"
	BorderThick = "▌"

	TodoCompletedIcon  = "✓"
	TodoPendingIcon    = "•"
	TodoInProgressIcon = "→"
)

// Styles is the resolved set of all UI styles for the current theme.
type Styles struct {
	// Background is the base terminal background for the theme.
	Background color.Color

	// BubbleBg is the fill color behind chat bubble content.
	BubbleBg color.Color

	// SidebarBg is the fill color behind the sidebar panel.
	SidebarBg color.Color

	// InputBg is the fill color behind the text input area.
	InputBg color.Color

	// Working indicator gradient colors for animated spinners.
	WorkingGradFromColor color.Color
	WorkingGradToColor   color.Color
	WorkingLabelColor    color.Color

	// Logo gradient colors.
	Logo struct {
		GradFromColor color.Color
		GradToColor   color.Color
		AccentColor   color.Color
		VersionColor  color.Color
	}

	// Header styles.
	Header struct {
		Accent    lipgloss.Style
		Separator lipgloss.Style
		Muted     lipgloss.Style
		Wrapper   lipgloss.Style
	}

	// Editor/input styles.
	Editor struct {
		Textarea            textarea.Styles
		PromptFocused       lipgloss.Style
		PromptBlurred       lipgloss.Style
		AttachmentIcon      lipgloss.Style
		AttachmentName      lipgloss.Style
		AttachmentDeleting  lipgloss.Style
	}

	TextInput textinput.Styles

	// Messages styles.
	Messages struct {
		UserBorder      lipgloss.Style
		AssistantBorder lipgloss.Style
		NoContent       lipgloss.Style
		Thinking        lipgloss.Style
		ThinkingHint    lipgloss.Style
		ErrorTag        lipgloss.Style
		ErrorTitle      lipgloss.Style
		ErrorDetails    lipgloss.Style
		InfoModel       lipgloss.Style
		InfoProvider    lipgloss.Style
		InfoDuration    lipgloss.Style
		Canceled        lipgloss.Style
	}

	// Tool call styles.
	Tool struct {
		IconPending   lipgloss.Style
		IconSuccess   lipgloss.Style
		IconError     lipgloss.Style
		IconCancelled lipgloss.Style
		Name          lipgloss.Style
		ParamKey      lipgloss.Style
		ParamValue    lipgloss.Style
		ContentLine   lipgloss.Style
		ContentTrunc  lipgloss.Style
		Body          lipgloss.Style
		ErrorTag      lipgloss.Style
		ErrorMessage  lipgloss.Style
		ActionCreate  lipgloss.Style
		ActionDestroy lipgloss.Style
	}

	// Sidebar styles.
	Sidebar struct {
		Border       lipgloss.Style
		Section      lipgloss.Style
		Value        lipgloss.Style
		Accent       lipgloss.Style
		Muted        lipgloss.Style
		Background   lipgloss.Style
		SessionTitle lipgloss.Style
		Path         lipgloss.Style
		Additions    lipgloss.Style
		Deletions    lipgloss.Style
		TruncHint    lipgloss.Style
	}

	// ModelInfo styles (used in sidebar and header).
	ModelInfo struct {
		Icon      lipgloss.Style
		Name      lipgloss.Style
		Provider  lipgloss.Style
		Reasoning lipgloss.Style
		Tokens    lipgloss.Style
		Cost      lipgloss.Style
	}

	// Resource styles (MCP, plugins, etc.).
	Resource struct {
		Heading    lipgloss.Style
		Name       lipgloss.Style
		Status     lipgloss.Style
		OnlineIcon lipgloss.Style
		ErrorIcon  lipgloss.Style
		BusyIcon   lipgloss.Style
		Count      lipgloss.Style
		More       lipgloss.Style
	}

	// Dialog/modal styles.
	Dialog struct {
		Title        lipgloss.Style
		TitleAccent  lipgloss.Style
		View         lipgloss.Style
		Primary      lipgloss.Style
		Secondary    lipgloss.Style
		NormalItem   lipgloss.Style
		SelectedItem lipgloss.Style
		HelpKey      lipgloss.Style
		HelpDesc     lipgloss.Style
		HelpSep      lipgloss.Style
		ContentPanel lipgloss.Style
		Spinner      lipgloss.Style

		// Gradient colors for dialog title decorations.
		TitleGradFrom color.Color
		TitleGradTo   color.Color
	}

	// Pills styles (queue/todo indicators).
	Pills struct {
		Base      lipgloss.Style
		Focused   lipgloss.Style
		Blurred   lipgloss.Style
		Label     lipgloss.Style
		Progress  lipgloss.Style
		Muted     lipgloss.Style
		HelpKey   lipgloss.Style
		HelpText  lipgloss.Style
		Area      lipgloss.Style
		QueueGradFrom color.Color
		QueueGradTo   color.Color
	}

	// Breadcrumb styles.
	Breadcrumb struct {
		Segment   lipgloss.Style
		Separator lipgloss.Style
		Active    lipgloss.Style
	}

	// Section styles (used for dividers, section headers).
	Section struct {
		Title lipgloss.Style
		Line  lipgloss.Style
	}

	// Completion popup styles.
	Completions struct {
		Normal  lipgloss.Style
		Focused lipgloss.Style
		Match   lipgloss.Style
	}

	// Status message styles.
	Status struct {
		Success lipgloss.Style
		Error   lipgloss.Style
		Warning lipgloss.Style
		Info    lipgloss.Style
	}
}
