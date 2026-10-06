package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadReadinessTimeout(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
		env  string
		want int
	}{
		{name: "omitted keeps default", want: 0},
		{name: "YAML", file: "3", want: 3},
		{name: "environment only", env: "2", want: 2},
		{name: "environment overrides YAML", file: "3", env: "4", want: 4},
		{name: "zero resets YAML to default", file: "3", env: "0", want: 0},
		{name: "upper bound", env: "60", want: 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			t.Setenv("SERVER_READINESS_TIMEOUT_SECONDS", tc.env)
			if tc.file != "" {
				path := filepath.Join(t.TempDir(), "config.yaml")
				require.NoError(t, os.WriteFile(path, []byte("server:\n  readiness_timeout_seconds: "+tc.file+"\n"), 0o600))
				t.Setenv("CONFIG_FILE", path)
			}
			cfg, err := Load()
			require.NoError(t, err)
			require.Equal(t, tc.want, cfg.Server.ReadinessTimeoutSeconds)
		})
	}
}

func TestLoadReadinessTimeoutRejectsInvalidBudgets(t *testing.T) {
	for _, value := range []string{"-1", "61"} {
		t.Run(value, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			t.Setenv("SERVER_READINESS_TIMEOUT_SECONDS", value)
			_, err := Load()
			require.ErrorContains(t, err, "server.readiness_timeout_seconds")
		})
	}
}
