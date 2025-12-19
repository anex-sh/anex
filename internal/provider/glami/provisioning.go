package glami

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"gitlab.devklarka.cz/ai/gpu-provider/virtualpod"
	"k8s.io/api/core/v1"
)

var (
	ErrMachineNotFound        = errors.New("machine not found")
	ErrMachineNotRunning      = errors.New("machine not running")
	ErrMachineFailed          = errors.New("machine failed")
	ErrCandidateMachineFailed = errors.New("candidate failed")
)

func newMachineSpecification(pod *v1.Pod) virtualpod.MachineSpecification {
	var out virtualpod.MachineSpecification

	const prefix = "gpu-provider.glami.cz/"
	annotations := pod.GetAnnotations()

	// Helper functions for parsing
	parseInt := func(value string) *int {
		if v, err := strconv.Atoi(value); err == nil {
			return &v
		}
		return nil
	}

	parseFloat := func(value string) *float64 {
		if v, err := strconv.ParseFloat(value, 64); err == nil {
			return &v
		}
		return nil
	}

	parseBool := func(value string) bool {
		v, _ := strconv.ParseBool(value)
		return v
	}

	parseList := func(value string) []string {
		parts := strings.Split(value, ",")
		var result []string
		for _, p := range parts {
			trimmed := strings.TrimSpace(p)
			if trimmed != "" {
				result = append(result, trimmed)
			}
		}
		return result
	}

	// Track exact values to handle conflicts with min/max
	exactFields := make(map[string]bool)

	// First pass: collect exact values
	for key := range annotations {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		setting := strings.TrimPrefix(key, prefix)

		// Check if this is an exact value (no -min or -max suffix)
		if !strings.HasSuffix(setting, "-min") && !strings.HasSuffix(setting, "-max") {
			exactFields[setting] = true
		}
	}

	// Second pass: parse all annotations
	for key, value := range annotations {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		setting := strings.TrimPrefix(key, prefix)

		switch {
		// Bool fields
		case setting == "verified-only":
			out.VerifiedOnly = parseBool(value)
		case setting == "datacenter-only":
			out.DatacenterOnly = parseBool(value)

		// List fields
		case setting == "region":
			regions := parseList(value)
			for _, r := range regions {
				switch r {
				case "europe":
					out.Regions = append(out.Regions, virtualpod.RegionEurope)
				case "north-america":
					out.Regions = append(out.Regions, virtualpod.RegionNorthAmerica)
				case "asia-pacific":
					out.Regions = append(out.Regions, virtualpod.RegionAsia)
				case "africa":
					out.Regions = append(out.Regions, virtualpod.RegionAfrica)
				case "south-america":
					out.Regions = append(out.Regions, virtualpod.RegionSouthAmerica)
				case "oceania":
					out.Regions = append(out.Regions, virtualpod.RegionOceania)
				}
			}
		case setting == "gpu-names":
			out.GPUNames = parseList(value)
		case setting == "compute-cap":
			out.ComputeCap = parseList(value)

		// GPU Count
		case setting == "gpu-count":
			out.GPUCount = parseInt(value)
		case setting == "gpu-count-min" && !exactFields["gpu-count"]:
			out.GPUCountMin = parseInt(value)
		case setting == "gpu-count-max" && !exactFields["gpu-count"]:
			out.GPUCountMax = parseInt(value)

		// VRAM (per GPU)
		case setting == "vram":
			out.VRAM = parseInt(value)
		case setting == "vram-min" && !exactFields["vram"]:
			out.VRAMMin = parseInt(value)
		case setting == "vram-max" && !exactFields["vram"]:
			out.VRAMMax = parseInt(value)

		// VRAM Total
		case setting == "vram-total":
			out.VRAMTotal = parseInt(value)
		case setting == "vram-total-min" && !exactFields["vram-total"]:
			out.VRAMTotalMin = parseInt(value)
		case setting == "vram-total-max" && !exactFields["vram-total"]:
			out.VRAMTotalMax = parseInt(value)

		// VRAM Bandwidth
		case setting == "vram-bandwidth":
			out.VRAMBandwidth = parseFloat(value)
		case setting == "vram-bandwidth-min" && !exactFields["vram-bandwidth"]:
			out.VRAMBandwidthMin = parseFloat(value)
		case setting == "vram-bandwidth-max" && !exactFields["vram-bandwidth"]:
			out.VRAMBandwidthMax = parseFloat(value)

		// TFLOPS
		case setting == "tflops":
			out.TFLOPS = parseFloat(value)
		case setting == "tflops-min" && !exactFields["tflops"]:
			out.TFLOPSMin = parseFloat(value)
		case setting == "tflops-max" && !exactFields["tflops"]:
			out.TFLOPSMax = parseFloat(value)

		// CUDA
		case setting == "cuda":
			out.CUDA = parseFloat(value)
		case setting == "cuda-min" && !exactFields["cuda"]:
			out.CUDAMin = parseFloat(value)
		case setting == "cuda-max" && !exactFields["cuda"]:
			out.CUDAMax = parseFloat(value)

		// CPU
		case setting == "cpu":
			out.CPU = parseInt(value)
		case setting == "cpu-min" && !exactFields["cpu"]:
			out.CPUMin = parseInt(value)
		case setting == "cpu-max" && !exactFields["cpu"]:
			out.CPUMax = parseInt(value)

		// RAM
		case setting == "ram":
			out.RAM = parseInt(value)
		case setting == "ram-min" && !exactFields["ram"]:
			out.RAMMin = parseInt(value)
		case setting == "ram-max" && !exactFields["ram"]:
			out.RAMMax = parseInt(value)

		// Price
		case setting == "price":
			out.Price = parseFloat(value)
		case setting == "price-min" && !exactFields["price"]:
			out.PriceMin = parseFloat(value)
		case setting == "price-max" && !exactFields["price"]:
			out.PriceMax = parseFloat(value)

		// VastAI DLPerf
		case setting == "vastai-dlperf":
			out.VastAIDLPerf = parseFloat(value)
		case setting == "vastai-dlperf-min" && !exactFields["vastai-dlperf"]:
			out.VastAIDLPerfMin = parseFloat(value)
		case setting == "vastai-dlperf-max" && !exactFields["vastai-dlperf"]:
			out.VastAIDLPerfMax = parseFloat(value)

		// Upload Speed
		case setting == "upload-speed":
			out.UploadSpeed = parseFloat(value)
		case setting == "upload-speed-min" && !exactFields["upload-speed"]:
			out.UploadSpeedMin = parseFloat(value)
		case setting == "upload-speed-max" && !exactFields["upload-speed"]:
			out.UploadSpeedMax = parseFloat(value)

		// Download Speed
		case setting == "download-speed":
			out.DownloadSpeed = parseFloat(value)
		case setting == "download-speed-min" && !exactFields["download-speed"]:
			out.DownloadSpeedMin = parseFloat(value)
		case setting == "download-speed-max" && !exactFields["download-speed"]:
			out.DownloadSpeedMax = parseFloat(value)
		}
	}

	return out
}

func (p *Provider) machineCleanup(ctx context.Context, vp *virtualpod.VirtualPod) {
	cleanupCtx := context.WithoutCancel(ctx)
	cleanupCtx, cancel := context.WithTimeout(cleanupCtx, 10*time.Second) // bound the cleanup
	defer cancel()

	if !vp.ProvisioningCompleted() {
		if vp.MachineRentID() != "" {
			destroyErr := p.client.DestroyMachine(cleanupCtx, vp.MachineRentID())
			if destroyErr != nil {
				log.G(ctx).Errorf("Error destroying instance: %v", destroyErr)
			}
			vp.RemoveMachine()
		}
	}
}

func (p *Provider) restartPod(ctx context.Context, machineID string, pullImage bool) error {
	return p.client.RestartMachine(ctx, machineID, pullImage)
}

func (p *Provider) initializeVirtualPod(ctx context.Context, vp *virtualpod.VirtualPod, restartOnly bool) {
	start := time.Now()
	defer vp.ProvisionCancel()
	defer p.provisioningWG.Done()
	defer p.machineCleanup(ctx, vp)

	logger := log.G(ctx)
	logger.Info("Starting pod initialization")

	var err error
	var machineID string
	var bo backoff.BackOff = backoff.NewConstantBackOff(1 * time.Second)
	bo = backoff.WithMaxRetries(bo, uint64(p.config.Provisioning.MaxRetries))
	bo = backoff.WithContext(bo, ctx)

	op := func() error {
		if restartOnly {
			machineID = vp.MachineRentID()
			logger.Infof("Restarting existing machine: %s", machineID)
			p.eventRecorder.Eventf(vp.Pod(), v1.EventTypeNormal, "Restarting", "Restarting machine %s", machineID)
			// TODO: Refactor provisioning state
			vp.SetProvisioningCompleted(false)
			err = p.restartPod(ctx, machineID, vp.ImagePullAlways())
			if err != nil {
				restartOnly = false
				return err
			}
		} else {
			logger.Info("Selecting and provisioning new machine")
			machineID, err = p.selectAndProvisionMachine(ctx, vp.Pod(), vp.AuthToken())
			// TODO: This is wrong, pod is not failed if provisioning still in progress
			if err != nil {
				logger.Errorf("Failed to select and provision machine: %v", err)
				return err
			}

			vp.SetMachine(&virtualpod.Machine{
				ID: machineID,
			})
			logger.Infof("Machine provisioned with ID: %s", machineID)
		}

		logger.Info("Waiting for machine to become ready")
		p.eventRecorder.Eventf(vp.Pod(), v1.EventTypeNormal, "MachineProvisioned", "Machine provisioned, waiting for startup")

		err = p.waitForMachineReady(ctx, vp)
		if err != nil {
			if errors.Is(err, ErrMachineFailed) || errors.Is(err, context.DeadlineExceeded) {
				logger.Warnf("Machine startup failed, banning machine: %s", vp.MachineStableID())
				p.eventRecorder.Eventf(vp.Pod(), v1.EventTypeWarning, "MachineStartupFailed", "Machine failed to start, will retry with different machine")
				p.mutex.Lock()
				p.machineBans[vp.MachineStableID()] = time.Now()
				_ = p.persistMachineBansToFile()
				p.mutex.Unlock()
				restartOnly = false
			}
			p.machineCleanup(ctx, vp)
			p.metrics.podsProvisioningTotal.WithLabelValues("false", "machine_startup_timeout").Inc()
			return ErrCandidateMachineFailed
		}

		logger.Info("Machine is running, initializing runtime environment")
		p.eventRecorder.Eventf(vp.Pod(), v1.EventTypeNormal, "MachineRunning", "Machine is running, initializing runtime environment")

		// TODO: Change this to a proper backoff by wrapper function and callback for logging - remove network retry logging
		// 		 Use normal http client with reasonable timeout
		client := retryablehttp.NewClient()
		client.HTTPClient.Timeout = 0
		client.RetryWaitMin = 1 * time.Second
		client.RetryWaitMax = 30 * time.Second
		client.RetryMax = math.MaxInt32
		client.Logger = nil

		agentCtx, agentStartUpCancel := context.WithTimeout(ctx, p.config.GetStartupTimeout())
		defer agentStartUpCancel()

		logger.Info("Waiting for agent to become ready")
		err = vp.WaitForAgentReady(agentCtx, client)
		if err != nil {
			logger.Errorf("Agent startup timeout: %v", err)
			p.eventRecorder.Eventf(vp.Pod(), v1.EventTypeWarning, "RuntimeInitFailed", "Failed to initialize container Agent")
			p.metrics.podsProvisioningTotal.WithLabelValues("false", "agent_startup_timeout").Inc()
			return ErrMachineFailed
		}

		logger.Info("Pushing environment variables to agent")
		err = vp.PushEnvVars(agentCtx, client)
		if err != nil {
			logger.Errorf("Failed to push env vars: %v", err)
			p.eventRecorder.Eventf(vp.Pod(), v1.EventTypeWarning, "RuntimeInitFailed", "Failed to configure runtime environment")
			p.metrics.podsProvisioningTotal.WithLabelValues("false", "env_var_push_fail").Inc()
			return ErrMachineFailed
		}

		logger.Info("Pushing config maps to agent")
		err = vp.PushConfigMaps(agentCtx, client)
		if err != nil {
			logger.Errorf("Failed to push config maps: %v", err)
			p.eventRecorder.Eventf(vp.Pod(), v1.EventTypeWarning, "RuntimeInitFailed", "Failed to configure runtime environment")
			p.metrics.podsProvisioningTotal.WithLabelValues("false", "config_map_push_fail").Inc()
			return ErrMachineFailed
		}

		if p.config.Gateway.Enable {
			logger.Info("Pushing wireproxy config to agent")
			err = vp.PushWireproxyConfig(agentCtx, client)
			if err != nil {
				logger.Errorf("Failed to push wireproxy config: %v", err)
				p.eventRecorder.Eventf(vp.Pod(), v1.EventTypeWarning, "RuntimeInitFailed", "Failed to configure runtime environment")
				p.metrics.podsProvisioningTotal.WithLabelValues("false", "wireproxy_config_push_fail").Inc()
				return ErrMachineFailed
			}
		}

		if p.config.Promtail.Enable {
			// TODO: Implement multiple clients
			lokiConfig := virtualpod.LokiPushGateway{
				URL:      p.config.Promtail.Clients[0].URL,
				Username: p.config.Promtail.Clients[0].BasicAuth.Username,
				Password: p.config.Promtail.Clients[0].BasicAuth.Password,
			}

			logger.Info("Pushing promtail config to agent")
			err = vp.PushPromtailConfig(agentCtx, client, lokiConfig)
			if err != nil {
				logger.Errorf("Failed to push promtail config: %v", err)
				p.eventRecorder.Eventf(vp.Pod(), v1.EventTypeWarning, "RuntimeInitFailed", "Failed to configure runtime environment")
				p.metrics.podsProvisioningTotal.WithLabelValues("false", "cmd_start_fail").Inc()
				return ErrMachineFailed
			}
		}

		logger.Info("Starting container command")
		err = vp.RunCommand(agentCtx, client)
		if err != nil {
			logger.Errorf("Failed to start command: %v", err)
			p.eventRecorder.Eventf(vp.Pod(), v1.EventTypeWarning, "ContainerStartFailed", "Failed to start pod's command")
			p.metrics.podsProvisioningTotal.WithLabelValues("false", "cmd_start_fail").Inc()
			return ErrMachineFailed
		}

		logger.Info("Starting pod lifecycle reconciliation")
		lifecycleCtx, lifecycleCancel := context.WithCancel(p.baseContext)
		vp.LifecycleCancel = lifecycleCancel
		go p.reconcilePodLifecycle(lifecycleCtx, vp)

		return nil
	}

	if errMainLoop := backoff.Retry(op, bo); errMainLoop == nil {
		vp.SetProvisioningCompleted(true)
		dur := time.Since(start).Seconds()
		logger.Infof("Pod initialization completed successfully in %.2f seconds", dur)
		p.eventRecorder.Eventf(vp.Pod(), v1.EventTypeNormal, "Started", "Container started successfully")
		p.metrics.podsProvisioningTotal.WithLabelValues("true", "ok").Inc()
		p.metrics.podsByPhase.WithLabelValues("Running").Inc()
		p.metrics.podsRunning.Inc()
		p.metrics.podsProvisioningDurationSecs.Add(dur)
	} else {
		logger.Errorf("Pod initialization failed after all retries: %v", errMainLoop)
		p.eventRecorder.Eventf(vp.Pod(), v1.EventTypeWarning, "ProvisioningFailed", "Failed to provision pod after retries")
		p.metrics.podsByPhase.WithLabelValues("Failed").Inc()
	}
}

func (p *Provider) selectAndProvisionMachine(ctx context.Context, pod *v1.Pod, authToken string) (machineID string, err error) {
	logger := log.G(ctx)
	logger.Info("Selecting and provisioning machine")

	bo := backoff.NewConstantBackOff(60 * time.Second)

	op := func() error {
		p.mutex.Lock()
		defer p.mutex.Unlock()
		time.Sleep(1 * time.Second)

		logger.Info("Searching for available machines matching requirements")
		p.eventRecorder.Eventf(pod, v1.EventTypeNormal, "SearchingMachines", "Searching for available machines matching requirements")

		machineSpec := newMachineSpecification(pod)
		var offers []virtualpod.Offer
		offers, err = p.client.GetRentalCandidates(ctx, machineSpec)
		if err != nil {
			logger.Errorf("Failed to search for available machines: %v", err)
			p.eventRecorder.Eventf(pod, v1.EventTypeWarning, "MachineSearchFailed", "Failed to search for available machines")
			p.metrics.podsProvisioningTotal.WithLabelValues("false", "rental_candidates_search_failed").Inc()
			return err
		}

		if len(offers) == 0 {
			logger.Warn("No machines matching requirements found, will retry")
			p.eventRecorder.Eventf(pod, v1.EventTypeWarning, "NoMachinesAvailable", "No machines matching requirements found, will retry")
		} else {
			logger.Infof("Found %d machine(s) matching requirements", len(offers))
			p.eventRecorder.Eventf(pod, v1.EventTypeNormal, "MachinesFound", "Found %d machine(s) matching requirements", len(offers))
		}

		// Filter out banned machines
		var candidatesFiltered []string
		banDuration := time.Duration(p.config.GetMachineBanDuration()) * time.Second
		bannedCount := 0
		for _, offer := range offers {
			if banTime, banned := p.machineBans[offer.MachineID]; !banned || (banDuration > 0 && time.Since(banTime) > banDuration) {
				candidatesFiltered = append(candidatesFiltered, offer.OfferID)
			} else {
				bannedCount++
			}
		}
		if bannedCount > 0 {
			logger.Infof("Filtered out %d banned machine(s), %d candidates remaining", bannedCount, len(candidatesFiltered))
		}

		machineID, err = p.client.ProvisionMachine(ctx, candidatesFiltered, pod, authToken, p.config.Gateway.Enable, p.config.Promtail.Enable)
		// TODO: Make some smoke tests on init; retry Unauthorized during runtime
		//if errors.Is(err, utils.ErrBadPayload) || errors.Is(err, utils.ErrUnauthorized) {
		//	p.metrics.podsProvisioningTotal.WithLabelValues("false", "provisioning_call_failed").Inc()
		//	return backoff.Permanent(err)
		//}

		return err
	}

	err = backoff.Retry(op, bo)
	return machineID, err
}

func (p *Provider) waitForMachineReady(ctx context.Context, vp *virtualpod.VirtualPod) error {
	logger := log.G(ctx)
	logger.Infof("Waiting for machine %s to become ready", vp.MachineRentID())

	retryCtx, cancel := context.WithTimeout(ctx, p.config.GetStartupTimeout())
	defer cancel()

	var bo backoff.BackOff = backoff.NewConstantBackOff(90 * time.Second)
	bo = backoff.WithContext(bo, retryCtx)

	op := func() error {
		if err := retryCtx.Err(); err != nil {
			return err
		}

		machine, err := p.client.GetMachine(retryCtx, vp.MachineRentID())
		if err != nil {
			return err
		}
		if machine == nil {
			return ErrMachineNotFound
		}

		vp.SetMachine(machine)

		switch machine.State {
		case virtualpod.MachineStateRunning:
			logger.Info("Machine is now in running state")
			return nil
		case virtualpod.MachineStateFailed:
			logger.Error("Machine entered failed state")
			return backoff.Permanent(ErrMachineFailed)
		default:
			return ErrMachineNotRunning
		}
	}

	return backoff.Retry(op, bo)
}
