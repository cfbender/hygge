package ui

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/ui/components"
	"github.com/cfbender/hygge/internal/ui/styles"
)

// benchDrawApp builds an App with a populated transcript at the given size.
func benchDrawApp(b *testing.B, w, h, msgCount int) *App {
	b.Helper()
	bs := bus.New()
	now := func() time.Time { return time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC) }
	app, err := New(AppOptions{
		Bus:           bs,
		Theme:         styles.DefaultTheme(),
		ProjectDir:    "~/proj",
		ModelProvider: "anthropic",
		ModelName:     "claude-sonnet-4-5",
		Now:           now,
	})
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	b.Cleanup(func() {
		_ = app.Close()
		bs.Close()
	})
	for i := range msgCount {
		role := components.RoleUser
		if i%2 == 1 {
			role = components.RoleAssistant
		}
		app.messages = append(app.messages, uiMessage{
			Role:      role,
			Raw:       fmt.Sprintf("message %d: enough text that the bubble wraps at least once in a 200-column terminal's chat column, exercising the wrap path", i),
			Timestamp: now(),
		})
	}
	app.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return app
}

// BenchmarkDraw measures a steady-state frame: the message cache is warm, so
// this is the per-frame cost paid during scrolling and animation ticks.
func BenchmarkDraw(b *testing.B) {
	app := benchDrawApp(b, 200, 60, 100)
	area := uv.Rect(0, 0, 200, 60)
	buf := uv.NewScreenBuffer(200, 60)
	app.Draw(buf, area) // warm the message cache
	b.ReportAllocs()

	for b.Loop() {
		app.Draw(buf, area)
	}
}

// BenchmarkFillBackgroundGaps isolates the background backfill pass so its
// share of a whole BenchmarkDraw frame can be compared directly.
func BenchmarkFillBackgroundGaps(b *testing.B) {
	app := benchDrawApp(b, 200, 60, 100)
	area := uv.Rect(0, 0, 200, 60)
	buf := uv.NewScreenBuffer(200, 60)
	app.Draw(buf, area)
	b.ReportAllocs()

	for b.Loop() {
		app.fillBackgroundGaps(buf, area)
	}
}
