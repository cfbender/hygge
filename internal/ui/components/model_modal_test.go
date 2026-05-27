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

func TestModelModalFavoritesDoNotExceedConfiguredHeight(t *testing.T) {
	t.Parallel()
	models := make([]ModelOption, 0, 20)
	models = append(models, testModelOption("anthropic", "claude-opus"))
	for i := 0; i < 19; i++ {
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
