package logger

import "github.com/Wei-Shaw/sub2api/internal/config"

// OptionsFromRuntimeConfig extends normal process logs without changing callers
// that intentionally use the legacy log-only adapter (including bootstrap).
func OptionsFromRuntimeConfig(cfg config.LogConfig, runtime config.RuntimeConfig) InitOptions {
	opts := OptionsFromConfig(cfg)
	opts.Runtime = RuntimeIdentity{NodeID: runtime.NodeID, Role: runtime.Role}
	return opts
}

func OptionsFromConfig(cfg config.LogConfig) InitOptions {
	return InitOptions{
		Level:           cfg.Level,
		Format:          cfg.Format,
		ServiceName:     cfg.ServiceName,
		Environment:     cfg.Environment,
		Caller:          cfg.Caller,
		StacktraceLevel: cfg.StacktraceLevel,
		Output: OutputOptions{
			ToStdout: cfg.Output.ToStdout,
			ToFile:   cfg.Output.ToFile,
			FilePath: cfg.Output.FilePath,
		},
		Rotation: RotationOptions{
			MaxSizeMB:  cfg.Rotation.MaxSizeMB,
			MaxBackups: cfg.Rotation.MaxBackups,
			MaxAgeDays: cfg.Rotation.MaxAgeDays,
			Compress:   cfg.Rotation.Compress,
			LocalTime:  cfg.Rotation.LocalTime,
		},
		Sampling: SamplingOptions{
			Enabled:    cfg.Sampling.Enabled,
			Initial:    cfg.Sampling.Initial,
			Thereafter: cfg.Sampling.Thereafter,
		},
	}
}
