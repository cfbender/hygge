package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/ui/components"
)

// TestUserMsgClick_OpensMessageActionModal verifies that clicking on the
// screen area occupied by a finalized user-message bubble opens the
// message-action overlay.
func TestUserMsgClick_OpensMessageActionModal(t *testing.T) {
	t.Parallel()
	app, st, _ := newTestAppWithStore(t, []session.NewMessage{
		{
			Role:  session.RoleUser,
			Parts: []session.Part{{Kind: session.PartText, Text: "hello from click test"}},
		},
	})
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	// Hydrate messages from store so MessageID is populated.
	app.hydrateMessagesFromStore(app.opts.SessionID)
	_ = st // used for seeding

	// Render to populate userMsgHitZones.
	_ = app.View()

	if len(app.userMsgHitZones) == 0 {
		t.Fatal("expected userMsgHitZones to be populated after View()")
	}

	zone := &app.userMsgHitZones[0]

	// Translate zone line to screen Y. Same formula as url_hitzones_test.go
	// (leading blank line → offset 1; subtract scroll offset).
	screenY := headerHeight + zone.StartLine + 1 - app.msgViewport.YOffset()
	// X in the middle of the left column so it is within the bubble area.
	screenX := app.layout.leftW / 2

	app.Update(tea.MouseClickMsg{X: screenX, Y: screenY, Button: tea.MouseLeft})

	if !app.overlays.Has(overlayMessageAction) {
		t.Fatalf("overlayMessageAction should be open after clicking a user message")
	}
	if app.messageActionModal.MessageID != zone.MessageID {
		t.Errorf("modal.MessageID = %q want %q", app.messageActionModal.MessageID, zone.MessageID)
	}
	if app.messageActionModal.MessageText != "hello from click test" {
		t.Errorf("modal.MessageText = %q want %q", app.messageActionModal.MessageText, "hello from click test")
	}
}

// TestMessageActionModal_EscClosesOverlay verifies that pressing Esc in the
// message-action overlay removes it from the overlay stack.
func TestMessageActionModal_EscClosesOverlay(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	// Manually open the overlay as if a user clicked a message.
	app.messageActionModal = components.MessageActionModal{
		Theme:       app.opts.Theme,
		SessionID:   "sess",
		MessageID:   "msg-001",
		MessageText: "test",
	}
	app.openOverlay(overlayMessageAction)

	if !app.overlays.Has(overlayMessageAction) {
		t.Fatal("precondition: overlay should be open")
	}

	app.Update(tea.KeyPressMsg{Code: tea.KeyEscape, Text: ""})

	if app.overlays.Has(overlayMessageAction) {
		t.Fatal("Esc should close overlayMessageAction")
	}
}

// TestMessageActionModal_CopyAction verifies that pressing 'c' in the overlay
// emits a copyToClipboard command (non-nil cmd).
func TestMessageActionModal_CopyAction(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.messageActionModal = components.MessageActionModal{
		Theme:       app.opts.Theme,
		SessionID:   "sess",
		MessageID:   "msg-001",
		MessageText: "copy this text",
		Cursor:      0,
	}
	app.openOverlay(overlayMessageAction)

	_, cmd := app.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	if cmd == nil {
		t.Fatal("expected clipboard cmd from 'c' in message-action modal; got nil")
	}
	// Overlay should be closed after the action.
	if app.overlays.Has(overlayMessageAction) {
		t.Fatal("overlayMessageAction should be closed after copy action")
	}
}

// TestMessageActionModal_ForkAction verifies that pressing 'f' in the overlay
// closes the overlay (the fork cmd is async and returns a tea.Cmd).
func TestMessageActionModal_ForkAction(t *testing.T) {
	t.Parallel()
	app, st, _ := newTestAppWithStore(t, []session.NewMessage{
		{
			Role:  session.RoleUser,
			Parts: []session.Part{{Kind: session.PartText, Text: "fork test"}},
		},
	})
	_ = st

	// Hydrate so we have a real message ID.
	app.hydrateMessagesFromStore(app.opts.SessionID)
	_ = app.View()

	var msgID string
	for _, m := range app.messages {
		if m.Role == components.RoleUser && m.MessageID != "" {
			msgID = m.MessageID
			break
		}
	}
	if msgID == "" {
		t.Fatal("no user message with MessageID found after hydration")
	}

	app.messageActionModal = components.MessageActionModal{
		Theme:       app.opts.Theme,
		SessionID:   app.opts.SessionID,
		MessageID:   msgID,
		MessageText: "fork test",
		Cursor:      1,
	}
	app.openOverlay(overlayMessageAction)

	_, cmd := app.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	// Overlay should be closed after the action, regardless of whether the fork succeeds.
	if app.overlays.Has(overlayMessageAction) {
		t.Fatal("overlayMessageAction should be closed after fork action")
	}
	// A fork cmd should have been returned (it runs the fork async).
	if cmd == nil {
		t.Fatal("expected fork cmd from 'f' in message-action modal; got nil")
	}
}

// TestUserMsgHitZones_NotPopulatedForStreamingMsg verifies that streaming
// user messages (no MessageID) do NOT produce a hit zone.
func TestUserMsgHitZones_NotPopulatedForStreamingMsg(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.opts.SessionID = "streaming-test"
	app.messages = []uiMessage{
		{
			Role:        components.RoleUser,
			Raw:         "streaming message",
			IsStreaming: true,
			MessageID:   "", // no MessageID — must not register a hit zone
		},
	}
	_ = app.View()
	if len(app.userMsgHitZones) != 0 {
		t.Errorf("streaming user message should not create a hit zone; got %d zones", len(app.userMsgHitZones))
	}
}

// TestUserMsgHitZones_PopulatedForFinalizedMsg verifies that finalized user
// messages with a MessageID DO produce a hit zone.
func TestUserMsgHitZones_PopulatedForFinalizedMsg(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.opts.SessionID = "finalized-test"
	app.messages = []uiMessage{
		{
			Role:      components.RoleUser,
			Raw:       "finalized message",
			MessageID: "msg-xyz",
		},
	}
	_ = app.View()
	if len(app.userMsgHitZones) == 0 {
		t.Fatal("finalized user message with MessageID should create a hit zone")
	}
	z := app.userMsgHitZones[0]
	if z.MessageID != "msg-xyz" {
		t.Errorf("zone.MessageID = %q want %q", z.MessageID, "msg-xyz")
	}
	if z.MessageText != "finalized message" {
		t.Errorf("zone.MessageText = %q want %q", z.MessageText, "finalized message")
	}
}
