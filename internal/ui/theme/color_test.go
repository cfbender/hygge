package theme

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	colorful "github.com/lucasb-eyer/go-colorful"

	"charm.land/lipgloss/v2"
)

func TestSaturationBoost_ANSIBase(t *testing.T) {
	t.Parallel()
	// ANSI 0-7 must map to their bright equivalents 8-15.
	cases := []struct {
		in   int
		want int
	}{
		{0, 8}, {1, 9}, {2, 10}, {3, 11},
		{4, 12}, {5, 13}, {6, 14}, {7, 15},
	}
	for _, tc := range cases {
		got := SaturationBoost(ansi.BasicColor(tc.in))
		bc, ok := got.(ansi.BasicColor)
		if !ok {
			t.Errorf("SaturationBoost(BasicColor(%d)) = %T, want ansi.BasicColor", tc.in, got)
			continue
		}
		if int(bc) != tc.want {
			t.Errorf("SaturationBoost(BasicColor(%d)) = %d, want %d", tc.in, int(bc), tc.want)
		}
	}
}

func TestSaturationBoost_ANSIBright_Unchanged(t *testing.T) {
	t.Parallel()
	// Bright ANSI 8-15 should be returned as-is.
	for _, n := range []int{8, 9, 12, 13, 15} {
		in := ansi.BasicColor(n)
		got := SaturationBoost(in)
		bc, ok := got.(ansi.BasicColor)
		if !ok || int(bc) != n {
			t.Errorf("SaturationBoost(BasicColor(%d)) = %v, want BasicColor(%d) (unchanged)", n, got, n)
		}
	}
}

func TestSaturationBoost_ANSI256_Unchanged(t *testing.T) {
	t.Parallel()
	// 256-color indices (IndexedColor) should be returned as-is.
	for _, n := range []int{17, 53, 236} {
		in := ansi.IndexedColor(n)
		got := SaturationBoost(in)
		ic, ok := got.(ansi.IndexedColor)
		if !ok || int(ic) != n {
			t.Errorf("SaturationBoost(IndexedColor(%d)) = %v, want IndexedColor(%d) (unchanged)", n, got, n)
		}
	}
}

func TestSaturationBoost_Hex_BumpsSaturation(t *testing.T) {
	t.Parallel()
	// A desaturated grey-blue; saturation should increase.
	// Use lipgloss.Color to produce the color.RGBA as lipgloss does.
	in := lipgloss.Color("#8995A8")
	out := SaturationBoost(in)

	// Returned value must be renderable as hex with higher saturation.
	if out == nil {
		t.Fatal("SaturationBoost(hex) returned nil")
	}
	cf, ok := colorful.MakeColor(out)
	if !ok {
		t.Fatalf("SaturationBoost(hex) result is not a valid color: %T %v", out, out)
	}
	inCf, _ := colorful.MakeColor(in)
	_, inS, _ := inCf.Hsl()
	_, outS, _ := cf.Hsl()
	if outS <= inS {
		t.Errorf("saturation not boosted: input S=%.3f, output S=%.3f", inS, outS)
	}
}

func TestSaturationBoost_Nil_ReturnsNil(t *testing.T) {
	t.Parallel()
	if got := SaturationBoost(nil); got != nil {
		t.Errorf("SaturationBoost(nil) = %v, want nil", got)
	}
}

func TestSaturationBoost_HexAlreadyMaxSaturation(t *testing.T) {
	t.Parallel()
	// Pure red is S=1.0; boosting must clamp and not panic.
	in := lipgloss.Color("#ff0000")
	out := SaturationBoost(in)
	if out == nil {
		t.Fatal("SaturationBoost(max-sat hex) returned nil")
	}
	// Output must be renderable.
	_, ok := colorful.MakeColor(out)
	if !ok {
		t.Errorf("output is not a valid color: %T %v", out, out)
	}
}
