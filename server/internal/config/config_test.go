package config

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.HLNetwork != NetworkMainnet {
		t.Errorf("HLNetwork = %q, want mainnet", c.HLNetwork)
	}
	if len(c.Markets) != 20 {
		t.Errorf("len(Markets) = %d, want 20", len(c.Markets))
	}
	if c.ResetCooldown != 24*time.Hour {
		t.Errorf("ResetCooldown = %v, want 24h", c.ResetCooldown)
	}
	if c.LeaderboardMinTrades != 5 || c.LeaderboardMinAge != 24*time.Hour {
		t.Errorf("leaderboard thresholds = %d/%v, want 5/24h", c.LeaderboardMinTrades, c.LeaderboardMinAge)
	}
	if c.StartingBalance.String() != "1000" {
		t.Errorf("StartingBalance = %s, want 1000", c.StartingBalance)
	}
	if c.LiquidationMode != LiquidationForfeit {
		t.Errorf("LiquidationMode = %q, want forfeit", c.LiquidationMode)
	}
	if c.HLInfoURL() != "https://api.hyperliquid.xyz/info" {
		t.Errorf("HLInfoURL = %q", c.HLInfoURL())
	}
}

func TestLoadTestnetAndMarkets(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("HL_NETWORK", "TESTNET")
	t.Setenv("MARKETS", " BTC, kPEPE ,,BTC ")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.HLWebsocketURL() != "wss://api.hyperliquid-testnet.xyz/ws" {
		t.Errorf("HLWebsocketURL = %q", c.HLWebsocketURL())
	}
	if want := []string{"BTC", "kPEPE"}; !slices.Equal(c.Markets, want) {
		t.Errorf("Markets = %v, want %v", c.Markets, want)
	}
}

func TestLoadReportsAllErrors(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("HL_NETWORK", "devnet")
	t.Setenv("RESET_COOLDOWN", "a day")
	t.Setenv("TAKER_FEE_RATE", "-1")

	_, err := Load()
	if err == nil {
		t.Fatal("Load succeeded, want error")
	}
	for _, want := range []string{"DATABASE_URL", "HL_NETWORK", "RESET_COOLDOWN", "TAKER_FEE_RATE"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}
