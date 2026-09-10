package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

type ServerConfig struct {
	Port int `koanf:"port"`
}

var defaultConfig = ServerConfig{
	Port: 6380,
}

func LoadConfig(path string, logger *slog.Logger) (ServerConfig, error) {
	k := koanf.New(".")
	if err := k.Load(confmap.Provider(map[string]any{
		"port": defaultConfig.Port,
	}, "."), nil); err != nil {
		return ServerConfig{}, fmt.Errorf("load default config: %w", err)
	}

	path, err := configFilePath(path)
	if err != nil {
		logger.Error("unable to resolve config path. using default config", "error", err)
		return readConfig(k)
	}

	if path == "" {
		logger.Info("no config found. using default config")
		return readConfig(k)
	}

	logger.Info("reading config from file", "path", path)

	if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
		if os.IsNotExist(err) {
			logger.Info("no config file found. using default config")
			return readConfig(k)
		}
		return ServerConfig{}, fmt.Errorf("load config file %q: %w", path, err)
	}

	return readConfig(k)
}

// configFilePath resolves path in three layers
// If path is passed from cli as function param, then the absolute path is returned
// If current folder contains a vectorized.yaml, use it
// Else look for config.yaml in $HOME/.vectorized/config.yaml
func configFilePath(path string) (string, error) {
	if path != "" {
		return filepath.Abs(path)
	}

	if path := "vectorized.yaml"; fileExists(path) {
		return filepath.Abs(path)
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".vectorized", "config.yaml"), nil
	}
	return "", errors.New("unable to resolve any config path")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func readConfig(k *koanf.Koanf) (ServerConfig, error) {
	var cfg ServerConfig
	if err := k.Unmarshal("", &cfg); err != nil {
		return ServerConfig{}, fmt.Errorf("decode config: %w", err)
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return ServerConfig{}, fmt.Errorf("port must be between 1 and 65535, got %d", cfg.Port)
	}
	return cfg, nil
}
