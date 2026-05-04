package main

import (
	"github.com/anex-sh/anex/cmd/virtual-kubelet/internal/provider"
	"github.com/anex-sh/anex/internal/provider/anex"
)

func registerVastAI(s *provider.Store) {
	/* #nosec */
	s.Register("vastai", func(cfg provider.InitConfig) (provider.Provider, error) { //nolint:errcheck
		return anex.NewGlamiProvider(
			cfg.ConfigPath,
			cfg.OperatingSystem,
			cfg.InternalIP,
			cfg.DaemonPort,
		)
	})
}
