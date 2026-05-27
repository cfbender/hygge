package components

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/catalog"
)

func testModelOption(provider, id string) ModelOption {
	return ModelOption{
		Provider: provider,
		Entry: catalog.Entry{
			Provider: provider,
			ID:       id,
			Name:     id,
		},
	}
}

func TestModelModalCtrlFToggleKeepsCursor(t *testing.T) {
	t.Parallel()
	modal := ModelModal{
		Cursor: 1,
		Models: []ModelOption{
			testModelOption("anthropic", "claude-opus"),
			testModelOption("openai", "gpt-4o"),
			testModelOption("openai", "gpt-4.1"),
		},
	}

	updated, msg := modal.HandleKey(ModelKey{Name: "ctrl+f"})
	if updated.Cursor != 1 {
		t.Fatalf("Cursor = %d after ctrl+f, want 1", updated.Cursor)
	}
	action, ok := msg.(ToggleFavoriteModelAction)
	if !ok {
		t.Fatalf("msg = %T, want ToggleFavoriteModelAction", msg)
	}
	if action.Provider != "openai" || action.Model != "gpt-4.1" {
		t.Fatalf("action = %s/%s, want openai/gpt-4.1", action.Provider, action.Model)
	}
}

func TestModelModalFavoritesDoNotExceedConfiguredHeight(t *testing.T) {
	t.Parallel()
	models := make([]ModelOption, 0, 20)
	models = append(models, testModelOption("anthropic", "claude-opus"))
	for i := range 19 {
		models = append(models, testModelOption("openai", fmt.Sprintf("gpt-%02d", i)))
	}
	modal := ModelModal{
		Width:     80,
		Height:    18,
		Models:    models,
		Favorites: []string{"anthropic/claude-opus"},
	}

	view := modal.View()
	if got := lipgloss.Height(view); got > modal.Height {
		t.Fatalf("modal height = %d, want <= %d\n%s", got, modal.Height, view)
	}
	if !strings.Contains(view, "Favorites") || !strings.Contains(view, "All models") {
		t.Fatalf("view should include both section headings:\n%s", view)
	}
}
