package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnvFindsParentFileWithoutOverridingEnvironment(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "backend", "cmd")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	const fileKey = "FIN_AGENT_DOTENV_FILE_VALUE"
	original, wasSet := os.LookupEnv(fileKey)
	if err := os.Unsetenv(fileKey); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if wasSet {
			_ = os.Setenv(fileKey, original)
		} else {
			_ = os.Unsetenv(fileKey)
		}
	})
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(
		fileKey+"=from-file\nFIN_AGENT_DOTENV_PROCESS_VALUE=from-file\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}

	const processKey = "FIN_AGENT_DOTENV_PROCESS_VALUE"
	t.Setenv(processKey, "from-process")
	t.Chdir(nested)

	if err := LoadDotEnv(); err != nil {
		t.Fatalf("LoadDotEnv() error = %v", err)
	}
	if got := os.Getenv(fileKey); got != "from-file" {
		t.Errorf("%s = %q, want from-file", fileKey, got)
	}
	if got := os.Getenv(processKey); got != "from-process" {
		t.Errorf("%s = %q, want from-process", processKey, got)
	}
}

func TestLoadDotEnvAllowsMissingFile(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := LoadDotEnv(); err != nil {
		t.Fatalf("LoadDotEnv() without a .env file error = %v, want nil", err)
	}
}

func TestLoadDotEnvReportsInvalidFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("INVALID='unterminated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	if err := LoadDotEnv(); err == nil {
		t.Fatal("LoadDotEnv() error = nil, want invalid .env syntax error")
	}
}
