package engine

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/juancruzfl/paperperps/server/internal/hyperliquid"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func eq(t *testing.T, name string, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Equal(d(want)) {
		t.Errorf("%s = %s, want %s", name, got, want)
	}
}

func TestApplyFill(t *testing.T) {
	tests := []struct {
		name                     string
		size, entry              string
		buy                      bool
		sz, px                   string
		wantSize, wantEntry, pnl string
	}{
		{"open long", "0", "0", true, "0.01", "80000", "0.01", "80000", "0"},
		{"add to long averages entry", "0.01", "80000", true, "0.03", "84000", "0.04", "83000", "0"},
		{"partial close long at profit", "0.04", "83000", false, "0.01", "85000", "0.03", "83000", "20"},
		{"close long at loss", "0.03", "83000", false, "0.03", "82000", "0", "0", "-30"},
		{"open short", "0", "0", false, "2", "100", "-2", "100", "0"},
		{"close short at profit", "-2", "100", true, "2", "90", "0", "0", "20"},
		{"flip long to short", "1", "100", false, "3", "110", "-2", "110", "10"},
		{"flip short to long", "-1", "100", true, "1.5", "110", "0.5", "110", "-10"},
		{"pnl rounds to 6 decimals", "0.00001", "82137.5", false, "0.00001", "82137.123456", "0", "0", "-0.000004"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := ApplyFill(d(tt.size), d(tt.entry), tt.buy, d(tt.sz), d(tt.px))
			eq(t, "size", r.NewSize, tt.wantSize)
			eq(t, "entry", r.NewEntryPx, tt.wantEntry)
			eq(t, "closedPnl", r.ClosedPnl, tt.pnl)
		})
	}
}

func TestFee(t *testing.T) {
	eq(t, "taker", Fee(d("0.01"), d("82000"), d("0.00045")), "0.369")
	eq(t, "maker", Fee(d("0.01"), d("82000"), d("0.00015")), "0.123")
	eq(t, "rounded", Fee(d("0.00001"), d("82137"), d("0.00045")), "0.00037") // 0.000369616...
}

func levels(pairs ...string) []hyperliquid.Level {
	var out []hyperliquid.Level
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, hyperliquid.Level{Px: d(pairs[i]), Sz: d(pairs[i+1])})
	}
	return out
}

func TestWalkBook(t *testing.T) {
	asks := levels("100", "1", "101", "2", "105", "10")

	filled, ntl := WalkBook(asks, true, d("2"), zero)
	eq(t, "filled", filled, "2")
	eq(t, "notional", ntl, "201") // 1@100 + 1@101

	filled, ntl = WalkBook(asks, true, d("20"), d("101"))
	eq(t, "limited filled", filled, "3")
	eq(t, "limited notional", ntl, "302")

	bids := levels("99", "1", "98", "1")
	filled, _ = WalkBook(bids, false, d("5"), d("98.5"))
	eq(t, "sell limit", filled, "1")

	filled, _ = WalkBook(nil, true, d("1"), zero)
	eq(t, "empty book", filled, "0")
}

func TestMargin(t *testing.T) {
	btc := CoinState{Size: d("0.01"), EntryPx: d("80000"), Leverage: 20, MarkPx: d("82000")}
	eq(t, "upnl", btc.UnrealizedPnl(), "20")
	eq(t, "position margin", btc.PositionMargin(), "41")

	// A sell of 0.03 against a 0.01 long: 0.01 closes (free), 0.02 opens a short.
	btc.Orders = []RestingOrder{{Buy: false, Remaining: d("0.03"), Px: d("85000")}}
	eq(t, "order margin", btc.OrderMargin(), "85") // 0.02 * 85000 / 20
	// Buys add to the long in full.
	btc.Orders = []RestingOrder{{Buy: true, Remaining: d("0.01"), Px: d("78000")}, {Buy: true, Remaining: d("0.01"), Px: d("76000")}}
	eq(t, "buy order margin", btc.OrderMargin(), "77") // 0.02 * 77000 / 20

	eth := CoinState{Size: d("-1"), EntryPx: d("2500"), Leverage: 10, MarkPx: d("2400")}
	s := Summarize(d("1000"), []CoinState{btc, eth})
	eq(t, "upnl", s.UnrealizedPnl, "120") // 20 + 100
	eq(t, "account value", s.AccountValue, "1120")
	eq(t, "margin used", s.MarginUsed, "281") // 41 + 240
	eq(t, "order margin", s.OrderMargin, "77")
	eq(t, "available", s.Available, "762")
}

func TestValidatePrice(t *testing.T) {
	ok := []struct {
		px    string
		szDec int
	}{{"82000", 5}, {"123456", 5}, {"2472.5", 4}, {"108.65", 2}, {"0.21234", 0}, {"0.001234", 0}, {"82000.0", 5}}
	for _, c := range ok {
		if err := ValidatePrice(d(c.px), c.szDec); err != nil {
			t.Errorf("ValidatePrice(%s, %d) = %v, want ok", c.px, c.szDec, err)
		}
	}
	bad := []struct {
		px    string
		szDec int
	}{{"0", 2}, {"-1", 2}, {"82000.5", 5}, {"2472.55", 4}, {"108.655", 2}, {"0.0012345", 0}, {"0.001234", 2}}
	for _, c := range bad {
		if err := ValidatePrice(d(c.px), c.szDec); err == nil {
			t.Errorf("ValidatePrice(%s, %d) = ok, want error", c.px, c.szDec)
		}
	}
}

func TestValidateSize(t *testing.T) {
	if err := ValidateSize(d("0.00001"), 5); err != nil {
		t.Error(err)
	}
	if err := ValidateSize(d("1.50"), 1); err != nil {
		t.Errorf("trailing zeros should not count: %v", err)
	}
	if ValidateSize(d("0.000001"), 5) == nil || ValidateSize(d("0"), 5) == nil || ValidateSize(d("1.5"), 0) == nil {
		t.Error("invalid sizes accepted")
	}
}
