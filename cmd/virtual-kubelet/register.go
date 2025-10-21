package main

import (
	"gitlab.devklarka.cz/ai/gpu-provider/cmd/virtual-kubelet/internal/provider"
	"gitlab.devklarka.cz/ai/gpu-provider/internal/provider/glami"
)

func registerVastAI(s *provider.Store) {
	/* #nosec */
	s.Register("vastai", func(cfg provider.InitConfig) (provider.Provider, error) { //nolint:errcheck
		return glami.NewGlamiProvider(
			cfg.ConfigPath,
			cfg.OperatingSystem,
			cfg.InternalIP,
			cfg.DaemonPort,
		)
	})
}
