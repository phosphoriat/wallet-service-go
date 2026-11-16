package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"regexp"
	"strings"

	"wallet-service/internal/utils"
)

type App struct {
	ConfigPath string // APP_CONFIG, по умолчанию config.json
	LogDir     string // LOG_DIR, по умолчанию logs
	LogLevel   string // LOG_LEVEL: debug | info | warn | error
}

type Server struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	AuthToken string `json:"auth_token,omitempty"`
}

type Gate struct {
	Name       string `json:"name"`
	Mnemonic   string `json:"mnemonic"`
	Passphrase string `json:"passphrase,omitempty"`
	Chain      string `json:"chain,omitempty"`
}

type Config struct {
	App    App    `json:"-"`
	Server Server `json:"config"`
	Gates  []Gate `json:"gates"`
}

var envNameRe = regexp.MustCompile(`[^A-Z0-9]+`)

func Load() (*Config, error) {
	if err := utils.LoadDotEnv(".env"); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("load .env: %w", err)
	}

	app := App{
		ConfigPath: utils.Env("APP_CONFIG", "config.json"),
		LogDir:     utils.Env("LOG_DIR", "logs"),
		LogLevel:   utils.Env("LOG_LEVEL", "info"),
	}

	raw, err := os.ReadFile(app.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", app.ConfigPath, err)
	}

	cfg := &Config{App: app}
	if err := json.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", app.ConfigPath, err)
	}
	if err := cfg.normalize(); err != nil {
		return nil, fmt.Errorf("%s: %w", app.ConfigPath, err)
	}
	return cfg, nil
}

func (c *Config) normalize() error {
	if c.Server.Host == "" {
		c.Server.Host = "127.0.0.1"
	}
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("config.port must be in 1..65535, got %d", c.Server.Port)
	}
	if v := os.Getenv("WALLET_AUTH_TOKEN"); v != "" {
		c.Server.AuthToken = v
	}
	if len(c.Gates) == 0 {
		return errors.New("at least one gate is required")
	}

	seen := make(map[string]bool, len(c.Gates))
	for i := range c.Gates {
		g := &c.Gates[i]
		g.Name = strings.TrimSpace(g.Name)
		if g.Name == "" {
			return fmt.Errorf("gates[%d].name is empty", i)
		}
		if seen[g.Name] {
			return fmt.Errorf("duplicate gate %q", g.Name)
		}
		seen[g.Name] = true

		if v := os.Getenv(g.MnemonicEnv()); v != "" {
			g.Mnemonic = v
		}
		g.Mnemonic = strings.Join(strings.Fields(g.Mnemonic), " ")
		if g.Mnemonic == "" || strings.HasPrefix(g.Mnemonic, "<") {
			return fmt.Errorf("gate %q has no mnemonic (set it in config or %s)", g.Name, g.MnemonicEnv())
		}
		if g.Chain == "" {
			g.Chain = g.Name
		}
	}
	return nil
}

func (g Gate) MnemonicEnv() string {
	return "WALLET_MNEMONIC_" + strings.Trim(envNameRe.ReplaceAllString(strings.ToUpper(g.Name), "_"), "_")
}

func (s Server) Addr() string { return fmt.Sprintf("%s:%d", s.Host, s.Port) }

func (a App) SlogLevel() slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(a.LogLevel)); err != nil {
		return slog.LevelInfo
	}
	return l
}
