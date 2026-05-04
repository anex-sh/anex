// Copyright © 2017 The virtual-kubelet authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package root

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/anex-sh/anex/cmd/virtual-kubelet/internal/provider"
	"github.com/anex-sh/anex/internal/manager"
	"github.com/anex-sh/anex/internal/provider/anex"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/virtual-kubelet/virtual-kubelet/errdefs"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"github.com/virtual-kubelet/virtual-kubelet/node"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apiserver/pkg/server/dynamiccertificates"
)

// NewCommand creates a new top-level command.
// This command is used to start the virtual-kubelet daemon
func NewCommand(ctx context.Context, name string, s *provider.Store, c Opts) *cobra.Command {
	cmd := &cobra.Command{
		Use:   name,
		Short: name + " provides a virtual kubelet interface for your kubernetes cluster.",
		Long: name + ` implements the Kubelet interface with a pluggable
backend implementation allowing users to create kubernetes nodes without running the kubelet.
This allows users to schedule kubernetes workloads on nodes that aren't running Kubernetes.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRootCommand(ctx, s, c)
		},
	}

	installFlags(cmd.Flags(), &c)
	return cmd
}

func runRootCommand(ctx context.Context, s *provider.Store, c Opts) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	config, err := anex.LoadConfig(c.ProviderConfigPath)
	if err != nil {
		return err
	}

	if ok := provider.ValidOperatingSystems[c.OperatingSystem]; !ok {
		return errdefs.InvalidInputf("operating system %q is not supported", c.OperatingSystem)
	}

	if c.PodSyncWorkers == 0 {
		return errdefs.InvalidInput("pod sync workers must be greater than 0")
	}

	var taint *corev1.Taint
	if !c.DisableTaint {
		var err error
		taint, err = getTaint(c)
		if err != nil {
			return err
		}
	}

	// Ensure API client.
	clientSet, err := nodeutil.ClientsetFromEnv(c.KubeConfigPath)
	if err != nil {
		return err
	}

	var providerWG *sync.WaitGroup

	// Set-up the node provider.
	mux := http.NewServeMux()
	newProvider := func(cfg nodeutil.ProviderConfig) (nodeutil.Provider, node.NodeProvider, error) {
		rm, err := manager.NewResourceManager(cfg.Pods, cfg.Secrets, cfg.ConfigMaps, cfg.Services)
		if err != nil {
			return nil, nil, errors.Wrap(err, "could not create resource manager")
		}
		// TODO: Fix missing env var VKUBELET_POD_IP
		initConfig := provider.InitConfig{
			ConfigPath:        c.ProviderConfigPath,
			NodeName:          config.VirtualNode.NodeName,
			OperatingSystem:   c.OperatingSystem,
			ResourceManager:   rm,
			DaemonPort:        c.ListenPort,
			InternalIP:        os.Getenv("VKUBELET_POD_IP"),
			KubeClusterDomain: c.KubeClusterDomain,
		}
		pInit := s.Get(c.Provider)
		if pInit == nil {
			return nil, nil, errors.Errorf("provider %q not found", c.Provider)
		}

		p, err := pInit(initConfig)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "error initializing provider %s", c.Provider)
		}

		ps := p.(interface {
			ProvisioningWG() *sync.WaitGroup
		})
		providerWG = ps.ProvisioningWG()

		p.ConfigureNode(ctx, cfg.Node)

		cfg.Node.Status.NodeInfo.KubeletVersion = c.Version
		return p, nil, nil
	}

	apiConfig, err := getAPIConfig(c)
	if err != nil {
		return err
	}

	cm, err := nodeutil.NewNode(config.VirtualNode.NodeName, newProvider,
		nodeutil.WithClient(clientSet),
		setAuth(config.VirtualNode.NodeName, apiConfig),
		func(cfg *nodeutil.NodeConfig) error {
			cfg.KubeconfigPath = c.KubeConfigPath
			cfg.Handler = mux
			cfg.InformerResyncPeriod = c.InformerResyncPeriod

			if taint != nil {
				cfg.NodeSpec.Spec.Taints = append(cfg.NodeSpec.Spec.Taints, *taint)
			}
			cfg.NodeSpec.Status.NodeInfo.Architecture = runtime.GOARCH
			cfg.NodeSpec.Status.NodeInfo.OperatingSystem = c.OperatingSystem

			cfg.HTTPListenAddr = apiConfig.Addr
			cfg.StreamCreationTimeout = apiConfig.StreamCreationTimeout
			cfg.StreamIdleTimeout = apiConfig.StreamIdleTimeout
			cfg.DebugHTTP = true

			cfg.NumWorkers = c.PodSyncWorkers

			// Always enable TLS for kube-apiserver <-> kubelet communication.
			// Prefer explicit keypair from provider config, then APISERVER_* env, else fallback to self-signed.
			var withTLS nodeutil.NodeOpt
			if config.TLS.CertPath != "" && config.TLS.KeyPath != "" {
				log.G(ctx).Info("Enabling TLS from provider config keypair")
				withTLS = nodeutil.WithTLSConfig(
					nodeutil.WithKeyPairFromPath(config.TLS.CertPath, config.TLS.KeyPath),
					maybeCA(config.TLS.CACertPath),
				)
			} else if apiConfig.CertPath != "" && apiConfig.KeyPath != "" {
				log.G(ctx).Info("Enabling TLS from APISERVER_* environment keypair")
				withTLS = nodeutil.WithTLSConfig(
					nodeutil.WithKeyPairFromPath(apiConfig.CertPath, apiConfig.KeyPath),
					maybeCA(apiConfig.CACertPath),
				)
			} else {
				// Self-signed fallback for managed control planes (e.g., EKS) where no CA is available.
				log.G(ctx).Warn("No TLS keypair provided; generating self-signed certificate. kubectl logs/exec/attach require --insecure-skip-tls-verify-backend")
				// Build SANs: localhost, loopbacks, pod IP (if set), and node name as DNS.
				var ips []net.IP
				var names []string
				names = append(names, "localhost", config.VirtualNode.NodeName)
				ips = append(ips, net.ParseIP("127.0.0.1"), net.ParseIP("::1"))
				if ip := os.Getenv("VKUBELET_POD_IP"); ip != "" {
					if parsed := net.ParseIP(ip); parsed != nil {
						ips = append(ips, parsed)
					}
				}
				cert, genErr := generateSelfSignedCert(names, ips, 24*time.Hour)
				if genErr != nil {
					return errors.Wrap(genErr, "generate self-signed cert")
				}
				withTLS = nodeutil.WithTLSConfig(func(tc *tls.Config) error {
					tc.Certificates = []tls.Certificate{cert}
					return nil
				})
			}
			return withTLS(cfg)
		},
		nodeutil.AttachProviderRoutes(mux),
	)
	if err != nil {
		return err
	}

	if err := setupTracing(ctx, c); err != nil {
		return err
	}

	ctx = log.WithLogger(ctx, log.G(ctx).WithFields(log.Fields{
		"provider":         c.Provider,
		"operatingSystem":  c.OperatingSystem,
		"node":             config.VirtualNode.NodeName,
		"watchedNamespace": c.KubeNamespace,
	}))

	go cm.Run(ctx) //nolint:errcheck

	defer func() {
		log.G(ctx).Debug("Waiting for controllers to be done")
		cancel()
		<-cm.Done()

		// Wait for Provisioning goroutines to finish
		providerCleanup := make(chan struct{})
		go func() {
			providerWG.Wait()
			close(providerCleanup)
		}()

		select {
		case <-providerCleanup:
			log.G(ctx).Info("All provisioning goroutines finished")
		case <-time.After(5 * time.Second):
			log.G(ctx).Error("Timeout waiting for provisioning goroutines")
			// TODO: likely unrecoverable error; trigger alert
		}
	}()

	log.G(ctx).Info("Waiting for controller to be ready")
	if err := cm.WaitReady(ctx, c.StartupTimeout); err != nil {
		return err
	}

	log.G(ctx).Info("Ready")

	select {
	case <-ctx.Done():
	case <-cm.Done():
		return cm.Err()
	}
	return nil
}

func setAuth(node string, apiCfg *apiServerConfig) nodeutil.NodeOpt {
	if apiCfg.CACertPath == "" {
		return func(cfg *nodeutil.NodeConfig) error {
			cfg.Handler = api.InstrumentHandler(nodeutil.WithAuth(nodeutil.NoAuth(), cfg.Handler))
			return nil
		}
	}

	return func(cfg *nodeutil.NodeConfig) error {
		auth, err := nodeutil.WebhookAuth(cfg.Client, node, func(cfg *nodeutil.WebhookAuthConfig) error {
			var err error
			cfg.AuthnConfig.ClientCertificateCAContentProvider, err = dynamiccertificates.NewDynamicCAContentFromFile("ca-cert-bundle", apiCfg.CACertPath)
			return err
		})
		if err != nil {
			return err
		}
		cfg.Handler = api.InstrumentHandler(nodeutil.WithAuth(auth, cfg.Handler))
		return nil
	}
}

func maybeCA(p string) func(*tls.Config) error {
	if p == "" {
		return func(*tls.Config) error { return nil }
	}
	return nodeutil.WithCAFromPath(p)
}
