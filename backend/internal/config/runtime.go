package config

import (
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/serverless"
	"time"
)

const (
	RuntimeRoleFull    = "full"
	RuntimeRoleGateway = "gateway"
)

// RuntimeConfig separates the primary's global jobs from request replicas.
// Empty remains equivalent to full for programmatically constructed configs.
type RuntimeConfig struct {
	Role               string `mapstructure:"role"`
	ServerlessID       string `mapstructure:"serverless_id"`
	ServerlessEndpoint string `mapstructure:"serverless_endpoint"`
	ServerlessRegion   string `mapstructure:"serverless_region"`
	ServerlessSecret   string `mapstructure:"serverless_secret"`
}

func (c *Config) RunsBackgroundJobs() bool {
	return c == nil || c.Runtime.Role != RuntimeRoleGateway
}

func (c *Config) validateRuntime() error {
	if err := serverless.ValidateRuntime(serverless.Runtime{ID: c.Runtime.ServerlessID, Endpoint: c.Runtime.ServerlessEndpoint, Region: c.Runtime.ServerlessRegion, Secret: c.Runtime.ServerlessSecret, Gateway: c.Runtime.Role == RuntimeRoleGateway}); err != nil {
		return err
	}
	switch c.Runtime.Role {
	case "", RuntimeRoleFull, RuntimeRoleGateway:
	default:
		return fmt.Errorf("runtime.role must be full or gateway")
	}
	if c.Server.GracefulShutdownTimeout < 0 || c.Server.GracefulShutdownTimeout > 3600 {
		return fmt.Errorf("server.graceful_shutdown_timeout must be between 0 and 3600 seconds")
	}
	if c.Server.ShutdownDrainDelay < 0 || c.Server.ShutdownDrainDelay > 300 {
		return fmt.Errorf("server.shutdown_drain_delay must be between 0 and 300 seconds")
	}
	return nil
}

func (c ServerConfig) ShutdownTimeout() time.Duration {
	if c.GracefulShutdownTimeout <= 0 {
		return 5 * time.Second
	}
	return time.Duration(c.GracefulShutdownTimeout) * time.Second
}
