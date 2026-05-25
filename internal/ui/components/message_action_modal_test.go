package components_test

import (
	"strings"
	"testing"

	"github.com/cfbender/hygge/internal/ui/components"
	"github.com/cfbender/hygge/internal/ui/styles"
)

// newMsgModal builds a MessageActionModal with sensible defaults.
func newMsgModal() components.MessageActionModal {
	return components.MessageActionModal{
		Theme:       styles.DefaultTheme(),
		Width:       100,
		Height:      30,
		SessionID:   "sess-abc",
		MessageID:   "msg-001",
		MessageText: "hello world",
		Cursor:      0,
	}
}

func pressMAK(m components.MessageActionModal, key string) (components.MessageActionModal, components.MessageActionModalMsg) {
	k := components.MessageActionKey{Name: key}
	if len(key) == 1 {
		k.Runes = []rune(key)
	}
	return m.HandleKey(k)
}

// --- render ------------------------------------------------------------------

func TestMessageActionModal_ViewContainsHeaderAndActions(t *testing.T) {
	t.Parallel()
	m := newMsgModal()
	v := m.View()
	for _, want := range []string{"message", "copy", "fork"} {
		if !strings.Contains(v, want) {
			t.Errorf("modal view missing %q:\n%s", want, v)
		}
	}
}

func TestMessageActionModal_ViewShowsPreview(t *testing.T) {
	t.Parallel()
	m := newMsgModal()
	v := m.View()
	if !strings.Contains(v, "hello world") {
		t.Errorf("modal view should show message preview; got:\n%s", v)
	}
}

func TestMessageActionModal_ViewOmitsPreviewWhenEmpty(t *testing.T) {
	t.Parallel()
	m := newMsgModal()
	m.MessageText = ""
	v := m.View()
	// \u201c is the opening curly quote used in previewText
	if strings.Contains(v, "\u201c") {
		t.Errorf("modal should not show preview with empty MessageText; got:\n%s", v)
	}
}

// --- navigation --------------------------------------------------------------

func TestMessageActionModal_DownMovesToFork(t *testing.T) {
	t.Parallel()
	m := newMsgModal()
	m.Cursor = 0
	m2, msg := pressMAK(m, "down")
	if msg != nil {
		t.Fatalf("down should not emit action; got %T", msg)
	}
	if m2.Cursor != 1 {
		t.Errorf("down: cursor=%d want 1", m2.Cursor)
	}
}

func TestMessageActionModal_JMovesToFork(t *testing.T) {
	t.Parallel()
	m := newMsgModal()
	m.Cursor = 0
	m2, _ := pressMAK(m, "j")
	if m2.Cursor != 1 {
		t.Errorf("j: cursor=%d want 1", m2.Cursor)
	}
}

func TestMessageActionModal_UpAtTopNoOp(t *testing.T) {
	t.Parallel()
	m := newMsgModal()
	m.Cursor = 0
	m2, _ := pressMAK(m, "up")
	if m2.Cursor != 0 {
		t.Errorf("up at top: cursor=%d want 0", m2.Cursor)
	}
}

func TestMessageActionModal_DownAtBottomNoOp(t *testing.T) {
	t.Parallel()
	m := newMsgModal()
	m.Cursor = 1
	m2, _ := pressMAK(m, "down")
	if m2.Cursor != 1 {
		t.Errorf("down at bottom: cursor=%d want 1", m2.Cursor)
	}
}

// --- actions -----------------------------------------------------------------

func TestMessageActionModal_EscEmitsClose(t *testing.T) {
	t.Parallel()
	_, msg := pressMAK(newMsgModal(), "esc")
	if _, ok := msg.(components.CloseMessageActionModal); !ok {
		t.Fatalf("esc should emit CloseMessageActionModal; got %T", msg)
	}
}

func TestMessageActionModal_EnterOnCopyEmitsCopy(t *testing.T) {
	t.Parallel()
	m := newMsgModal()
	m.Cursor = 0
	_, msg := pressMAK(m, "enter")
	act, ok := msg.(components.CopyMessageAction)
	if !ok {
		t.Fatalf("enter on copy should emit CopyMessageAction; got %T", msg)
	}
	if act.Text != "hello world" {
		t.Errorf("CopyMessageAction.Text = %q want %q", act.Text, "hello world")
	}
}

func TestMessageActionModal_EnterOnForkEmitsFork(t *testing.T) {
	t.Parallel()
	m := newMsgModal()
	m.Cursor = 1
	_, msg := pressMAK(m, "enter")
	act, ok := msg.(components.ForkMessageAction)
	if !ok {
		t.Fatalf("enter on fork should emit ForkMessageAction; got %T", msg)
	}
	if act.SessionID != "sess-abc" {
		t.Errorf("ForkMessageAction.SessionID = %q want %q", act.SessionID, "sess-abc")
	}
	if act.MessageID != "msg-001" {
		t.Errorf("ForkMessageAction.MessageID = %q want %q", act.MessageID, "msg-001")
	}
}

func TestMessageActionModal_CKeyEmitsCopy(t *testing.T) {
	t.Parallel()
	m := newMsgModal()
	m.Cursor = 1 // cursor on fork to prove 'c' overrides cursor
	_, msg := pressMAK(m, "c")
	act, ok := msg.(components.CopyMessageAction)
	if !ok {
		t.Fatalf("'c' should emit CopyMessageAction; got %T", msg)
	}
	if act.Text != "hello world" {
		t.Errorf("CopyMessageAction.Text = %q want %q", act.Text, "hello world")
	}
}

func TestMessageActionModal_FKeyEmitsFork(t *testing.T) {
	t.Parallel()
	m := newMsgModal()
	m.Cursor = 0 // cursor on copy to prove 'f' overrides cursor
	_, msg := pressMAK(m, "f")
	act, ok := msg.(components.ForkMessageAction)
	if !ok {
		t.Fatalf("'f' should emit ForkMessageAction; got %T", msg)
	}
	if act.MessageID != "msg-001" {
		t.Errorf("ForkMessageAction.MessageID = %q want %q", act.MessageID, "msg-001")
	}
}

func TestMessageActionModal_1KeyEmitsCopy(t *testing.T) {
	t.Parallel()
	_, msg := pressMAK(newMsgModal(), "1")
	if _, ok := msg.(components.CopyMessageAction); !ok {
		t.Fatalf("'1' should emit CopyMessageAction; got %T", msg)
	}
}

func TestMessageActionModal_2KeyEmitsFork(t *testing.T) {
	t.Parallel()
	_, msg := pressMAK(newMsgModal(), "2")
	if _, ok := msg.(components.ForkMessageAction); !ok {
		t.Fatalf("'2' should emit ForkMessageAction; got %T", msg)
	}
}
