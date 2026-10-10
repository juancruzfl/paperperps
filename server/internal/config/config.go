// Package config loads server configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// DefaultMarkets is the top 20 perps by mainnet 24h volume that are also listed on
// testnet, as of 2026-10-09. See docs/plan.md §4.1.
const DefaultMarkets = "BTC,ETH,HYPE,NEAR,SOL,PUMP,VVV,WLD,LIT,SUI,ZRO,DOGE,MET,PONS,ONDO,ADA,TAO,FARTCOIN,XPL,XMR"

const (
	NetworkTestnet = "testnet"
	NetworkMainnet = "mainnet"

	// LiquidationForfeit forfeits remaining margin on liquidation, like HL's backstop.
	LiquidationForfeit = "forfeit"
	// LiquidationMarket returns remaining margin, like HL's market liquidation.
	LiquidationMarket = "market"
)

type Config struct {
	Port        string
	DatabaseURL string
	// WebDir holds the built frontend. Empty or missing means API only.
	WebDir string
	// Version identifies the running build (the git commit on Railway).
	Version string

	HLNetwork string
	Markets   []string
	// HLInfoURLOverride and HLWSURLOverride point the server at another Hyperliquid-
	// compatible API, e.g. the fake one the browser tests use. Empty: use HLNetwork's.
	HLInfoURLOverride string
	HLWSURLOverride   string

	StartingBalance decimal.Decimal
	TakerFeeRate    decimal.Decimal
	MakerFeeRate    decimal.Decimal
	LiquidationMode string
	ResetCooldown   time.Duration

	LeaderboardMinTrades int
	LeaderboardMinAge    time.Duration
}

// Load reads the configuration from the environment and validates it.
func Load() (Config, error) {
	var errs []error
	c := Config{
		Port:              env("PORT", "8080"),
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		WebDir:            env("WEB_DIR", ""),
		Version:           env("RAILWAY_GIT_COMMIT_SHA", "dev"),
		HLNetwork:         strings.ToLower(env("HL_NETWORK", NetworkMainnet)),
		Markets:           parseList(env("MARKETS", DefaultMarkets)),
		HLInfoURLOverride: env("HL_INFO_URL", ""),
		HLWSURLOverride:   env("HL_WS_URL", ""),
		LiquidationMode:   strings.ToLower(env("LIQUIDATION_MODE", LiquidationForfeit)),
	}

	c.StartingBalance = parseDecimal("STARTING_BALANCE", "1000", &errs)
	c.TakerFeeRate = parseDecimal("TAKER_FEE_RATE", "0.00045", &errs)
	c.MakerFeeRate = parseDecimal("MAKER_FEE_RATE", "0.00015", &errs)
	c.ResetCooldown = parseDuration("RESET_COOLDOWN", "24h", &errs)
	c.LeaderboardMinAge = parseDuration("LEADERBOARD_MIN_AGE", "24h", &errs)
	c.LeaderboardMinTrades = parseInt("LEADERBOARD_MIN_TRADES", "5", &errs)

	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}
	if c.HLNetwork != NetworkTestnet && c.HLNetwork != NetworkMainnet {
		errs = append(errs, fmt.Errorf("HL_NETWORK must be %q or %q, got %q", NetworkTestnet, NetworkMainnet, c.HLNetwork))
	}
	if len(c.Markets) == 0 {
		errs = append(errs, errors.New("MARKETS must list at least one coin"))
	}
	if c.LiquidationMode != LiquidationForfeit && c.LiquidationMode != LiquidationMarket {
		errs = append(errs, fmt.Errorf("LIQUIDATION_MODE must be %q or %q, got %q", LiquidationForfeit, LiquidationMarket, c.LiquidationMode))
	}
	return c, errors.Join(errs...)
}

// HLInfoURL is the Hyperliquid REST info endpoint for the configured network.
func (c Config) HLInfoURL() string {
	if c.HLInfoURLOverride != "" {
		return c.HLInfoURLOverride
	}
	if c.HLNetwork == NetworkMainnet {
		return "https://api.hyperliquid.xyz/info"
	}
	return "https://api.hyperliquid-testnet.xyz/info"
}

// HLWebsocketURL is the Hyperliquid websocket endpoint for the configured network.
func (c Config) HLWebsocketURL() string {
	if c.HLWSURLOverride != "" {
		return c.HLWSURLOverride
	}
	if c.HLNetwork == NetworkMainnet {
		return "wss://api.hyperliquid.xyz/ws"
	}
	return "wss://api.hyperliquid-testnet.xyz/ws"
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return fallback
}

func parseList(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		// Case matters: Hyperliquid lists coins like kPEPE.
		coin := strings.TrimSpace(part)
		if coin != "" && !seen[coin] {
			seen[coin] = true
			out = append(out, coin)
		}
	}
	return out
}

func parseDecimal(key, fallback string, errs *[]error) decimal.Decimal {
	d, err := decimal.NewFromString(env(key, fallback))
	if err != nil || d.IsNegative() {
		*errs = append(*errs, fmt.Errorf("%s must be a non-negative decimal", key))
	}
	return d
}

func parseDuration(key, fallback string, errs *[]error) time.Duration {
	d, err := time.ParseDuration(env(key, fallback))
	if err != nil || d < 0 {
		*errs = append(*errs, fmt.Errorf("%s must be a non-negative duration like 24h", key))
	}
	return d
}

func parseInt(key, fallback string, errs *[]error) int {
	n, err := strconv.Atoi(env(key, fallback))
	if err != nil || n < 0 {
		*errs = append(*errs, fmt.Errorf("%s must be a non-negative integer", key))
	}
	return n
}
