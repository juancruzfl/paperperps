package sharecard

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"testing"
)

func TestFormatUSD(t *testing.T) {
	for _, c := range []struct {
		v      float64
		signed bool
		want   string
	}{
		{1234.567, true, "+$1,234.57"},
		{-1234567.891, true, "-$1,234,567.89"},
		{12.5, false, "$12.50"},
		{0.001, true, "$0.00"},
		{-0.004, true, "$0.00"}, // never -$0.00
		{999, true, "+$999.00"},
	} {
		if got := FormatUSD(c.v, c.signed); got != c.want {
			t.Errorf("FormatUSD(%v, %v) = %q, want %q", c.v, c.signed, got, c.want)
		}
	}
}

func TestRenderProfile(t *testing.T) {
	win := 0.62
	for name, p := range map[string]Profile{
		"gain": {Handle: "brave-otter-412", Pnl: 1234.56, WinRate: &win, Trades: 1520, Rank: 3, Link: "paperperps.lol/u/brave-otter-412"},
		"loss": {Handle: "a-very-long-handle-xyz-1", Pnl: -98765.43, Trades: 7, Link: "paperperps.lol/u/a-very-long-handle-xyz-1"},
	} {
		t.Run(name, func(t *testing.T) {
			data, err := RenderProfile(p)
			if err != nil {
				t.Fatal(err)
			}
			img, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if img.Bounds() != image.Rect(0, 0, W, H) {
				t.Fatalf("size = %v", img.Bounds())
			}
			// The paper-trading pill ends at the right margin, in the warning color.
			r, g, b, _ := img.At(CARDRight-6, 79).RGBA()
			if r>>8 != 0xf5 || g>>8 != 0xb9 || b>>8 != 0x42 {
				t.Errorf("pill pixel = %02x%02x%02x, want f5b942", r>>8, g>>8, b>>8)
			}
			if dir := os.Getenv("SHARECARD_OUT"); dir != "" {
				os.WriteFile(dir+"/card-"+name+".png", data, 0o644)
			}
		})
	}
}

func BenchmarkRenderProfile(b *testing.B) {
	win := 0.5
	p := Profile{Handle: "brave-otter-412", Pnl: 1234.56, WinRate: &win, Trades: 20, Rank: 1, Link: "paperperps.lol/u/brave-otter-412"}
	for b.Loop() {
		if _, err := RenderProfile(p); err != nil {
			b.Fatal(err)
		}
	}
}
