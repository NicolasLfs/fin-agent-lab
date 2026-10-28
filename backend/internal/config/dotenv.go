package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
)

func LoadDotEnv() error {
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("find local environment file: %w", err)
	}

	for {
		path := filepath.Join(dir, ".env")
		info, err := os.Stat(path)
		if err == nil {
			if !info.Mode().IsRegular() {
				return errors.New("local .env path is not a regular file")
			}
			if err := godotenv.Load(path); err != nil {
				return fmt.Errorf("load local .env file: %w", err)
			}
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect local .env file: %w", err)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}
