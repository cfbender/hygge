package ui

import (
	"testing"

	"charm.land/lipgloss/v2"
)

func TestFogFrameCacheReusesFrames(t *testing.T) {
	var c fogFrameCache
	accent := lipgloss.Color("#b06aff")

	first := c.line(15, 5, accent, 0)
	if first == "" {
		t.Fatal("expected a non-empty spinner line")
	}
	if len(c.lines) != fogCacheFrames {
		t.Fatalf("expected %d precomputed lines, got %d", fogCacheFrames, len(c.lines))
	}

	// Same key must not regenerate: mutate a frame and confirm the cached
	// value is served back.
	c.lines[0] = "sentinel"
	if got := c.line(15, 5, accent, 0); got != "sentinel" {
		t.Fatalf("cache regenerated for an unchanged key: got %q", got)
	}

	// A different tint regenerates the loop.
	if got := c.line(15, 5, lipgloss.Color("#22aa66"), 0); got == "sentinel" {
		t.Fatal("cache did not regenerate after tint change")
	}
}

func TestFogFrameCachePingPongIndexInBounds(t *testing.T) {
	var c fogFrameCache
	accent := lipgloss.Color("#b06aff")

	// Sweep several playback periods; every t must resolve to a cached frame
	// without panicking, including t exactly on frame boundaries.
	for i := range 1000 {
		tm := float64(i) * 0.0625
		if got := c.line(15, 5, accent, tm); got == "" {
			t.Fatalf("empty frame at t=%v", tm)
		}
	}
}

func TestFogFrameCacheDegenerateSize(t *testing.T) {
	var c fogFrameCache
	if got := c.line(0, 0, lipgloss.Color("#b06aff"), 1.0); got != "" {
		t.Fatalf("expected empty line for zero size, got %q", got)
	}
}

func TestDensestFogLinePicksMostGlyphs(t *testing.T) {
	frame := ".\n.-+O\n.."
	if got := densestFogLine(frame); got != ".-+O" {
		t.Fatalf("expected densest line %q, got %q", ".-+O", got)
	}
}
