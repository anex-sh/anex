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
	"k8s.io/apimachinery/pkg/api/resource"
)

var (
	ErrMachineNotRunning      = errors.New("machine not running")
	ErrMachineFailed          = errors.New("machine failed")
	ErrCandidateMachineFailed = errors.New("candidate failed")
)

func newMachineSpecification(pod *v1.Pod) virtualpod.MachineSpecification {
	var out virtualpod.MachineSpecification

	annotations := pod.GetAnnotations()
	for key, value := range annotations {
		if strings.HasPrefix(key, "glami.cz/") {
			setting := strings.TrimPrefix(key, "glami.cz/")
			switch setting {
			case "region":
				regions := strings.Split(value, ",")
				for _, r := range regions {
					if r == "europe" {
						out.Regions = append(out.Regions, virtualpod.RegionEurope)
					} else if r == "north-america" {
						out.Regions = append(out.Regions, virtualpod.RegionNorthAmerica)
					} else if r == "asia-pacific" {
						out.Regions = append(out.Regions, virtualpod.RegionAsia)
					} else if r == "africa" {
						out.Regions = append(out.Regions, virtualpod.RegionAfrica)
					} else if r == "south-america" {
						out.Regions = append(out.Regions, virtualpod.RegionSouthAmerica)
					} else if r == "oceania" {
						out.Regions = append(out.Regions, virtualpod.RegionOceania)
					} else {
						continue
					}
				}
			case "min-gpu-memory":
				if q, err := resource.ParseQuantity(value); err == nil {
					out.MemoryPerGPUMB = int(q.Value() / (1024 * 1024)) // Convert to MB
				}
			case "tflops-min":
				if tflops, err := strconv.ParseFloat(value, 64); err == nil {
					out.TFLOPSMin = tflops
				}
			case "dlperf-min":
				if dlperf, err := strconv.ParseFloat(value, 64); err == nil {
					out.DLPerfMin = dlperf
				}
			case "cuda-max":
				if cuda, err := strconv.ParseFloat(value, 64); err == nil {
					out.CudaMax = cuda
				}
			case "cpu-cores-min":
				if cpuCores, err := strconv.ParseInt(value, 10, 64); err == nil {
					out.CPUCores = int(cpuCores)
				}
			case "cpu-ram-min":
				if cpuRam, err := strconv.ParseInt(value, 10, 64); err == nil {
					out.CPURamMB = int(cpuRam) * 1024
				}
			case "disk-space-gb":
				if diskSpace, err := strconv.ParseInt(value, 10, 64); err == nil {
					out.DiskSpace = int(diskSpace)
				}
			case "max-price":
				if price, err := strconv.ParseFloat(value, 64); err == nil {
					out.MaxPricePerHour = price
				}
			}
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
	logger.Infof("Initializing instance for pod: %s", vp.ID())

	var err error
	var machineID string
	var bo backoff.BackOff = backoff.NewConstantBackOff(1 * time.Second)
	bo = backoff.WithMaxRetries(bo, uint64(p.config.VirtualKubelet.Provisioning.MaxRetries))
	bo = backoff.WithContext(bo, ctx)

	// TODO: Enhance with Kubernetes events
	op := func() error {
		if restartOnly {
			machineID = vp.MachineRentID()
			err = p.restartPod(ctx, machineID, vp.ImagePullAlways())
		} else {
			machineID, err = p.selectAndProvisionMachine(ctx, vp.Pod(), vp.AuthToken())
			// TODO: This is wrong, pod is not failed if provisioning still in progress
			if err != nil {
				// vp.FailContainer(err)
				//p.notifyPodUpdate(vp.Pod())
				return err
			}

			vp.SetMachine(&virtualpod.Machine{
				ID: machineID,
			})
		}

		p.eventRecorder.Eventf(vp.Pod(), v1.EventTypeNormal, "Provisioning", "Provisioning machine ID: %s", vp.MachineRentID())

		err = p.waitForMachineReady(ctx, vp)
		if err != nil {
			if errors.Is(err, ErrMachineFailed) || errors.Is(err, context.DeadlineExceeded) {
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

		// TODO: Change this to a proper backoff by wrapper function and callback for logging - remove network retry logging
		// 		 Use normal http client with reasonable timeout
		client := retryablehttp.NewClient()
		client.HTTPClient.Timeout = 0
		client.RetryWaitMin = 1 * time.Second
		client.RetryWaitMax = 30 * time.Second
		client.RetryMax = math.MaxInt32

		agentCtx, agentStartUpCancel := context.WithTimeout(ctx, p.config.GetStartupTimeout())
		defer agentStartUpCancel()

		err = vp.WaitForAgentReady(agentCtx, client)
		if err != nil {
			logger.Error("Agent startup timeout")
			p.metrics.podsProvisioningTotal.WithLabelValues("false", "agent_startup_timeout").Inc()
			return ErrMachineFailed
		}

		err = vp.PushEnvVars(agentCtx, client)
		if err != nil {
			logger.Error("Failed to push env vars")
			p.metrics.podsProvisioningTotal.WithLabelValues("false", "env_var_push_fail").Inc()
			return ErrMachineFailed
		}

		err = vp.PushConfigMaps(agentCtx, client)
		if err != nil {
			logger.Error("Failed to push config maps")
			p.metrics.podsProvisioningTotal.WithLabelValues("false", "config_map_push_fail").Inc()
			return ErrMachineFailed
		}

		if p.config.Proxy.Enable {
			err = vp.PushWireproxyConfig(agentCtx, client)
			if err != nil {
				logger.Error("Failed to push wireproxy config")
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

			err = vp.PushPromtailConfig(agentCtx, client, lokiConfig)
			if err != nil {
				logger.Error("Failed to start command")
				p.metrics.podsProvisioningTotal.WithLabelValues("false", "cmd_start_fail").Inc()
				return ErrMachineFailed
			}
		}

		err = vp.RunCommand(agentCtx, client)
		if err != nil {
			logger.Error("Failed to start command")
			p.metrics.podsProvisioningTotal.WithLabelValues("false", "cmd_start_fail").Inc()
			return ErrMachineFailed
		}

		lifecycleCtx, lifecycleCancel := context.WithCancel(p.baseContext)
		vp.LifecycleCancel = lifecycleCancel
		go p.reconcilePodLifecycle(lifecycleCtx, vp)

		return nil
	}

	if errMainLoop := backoff.Retry(op, bo); errMainLoop == nil {
		vp.SetProvisioningCompleted()
		p.metrics.podsProvisioningTotal.WithLabelValues("true", "ok").Inc()
		p.metrics.podsByPhase.WithLabelValues("Running").Inc()
		p.metrics.podsRunning.Inc()
	} else {
		p.metrics.podsByPhase.WithLabelValues("Failed").Inc()
	}

	dur := time.Since(start).Seconds()
	p.metrics.podsProvisioningDurationSecs.Add(dur)
}

func (p *Provider) selectAndProvisionMachine(ctx context.Context, pod *v1.Pod, authToken string) (machineID string, err error) {
	logger := log.G(ctx)
	logger.Infof("Initializing instance for pod")

	bo := backoff.NewConstantBackOff(60 * time.Second)

	op := func() error {
		p.mutex.Lock()
		defer p.mutex.Unlock()
		time.Sleep(1 * time.Second)

		machineSpec := newMachineSpecification(pod)
		var offers []virtualpod.Offer
		offers, err = p.client.GetRentalCandidates(ctx, machineSpec)
		if err != nil {
			p.metrics.podsProvisioningTotal.WithLabelValues("false", "rental_candidates_search_failed").Inc()
			logger.Error(err)
			return err
		}

		if len(offers) == 0 {
			logger.Warn("No offers matching pod's criteria found!!!")
		}

		// Filter out banned machines
		var candidatesFiltered []string
		banDuration := time.Duration(p.config.GetMachineBanDuration()) * time.Second
		for _, offer := range offers {
			if banTime, banned := p.machineBans[offer.MachineID]; !banned || (banDuration > 0 && time.Since(banTime) > banDuration) {
				candidatesFiltered = append(candidatesFiltered, offer.OfferID)
			}
		}

		machineID, err = p.client.ProvisionMachine(ctx, candidatesFiltered, pod, authToken, p.config.Proxy.Enable, p.config.Promtail.Enable)
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
	logger.Infof("Waiting for machine to be running: %s", vp.MachineRentID())

	retryCtx, cancel := context.WithTimeout(ctx, p.config.GetStartupTimeout())
	defer cancel()

	var bo backoff.BackOff = backoff.NewConstantBackOff(30 * time.Second)
	bo = backoff.WithContext(bo, retryCtx)

	op := func() error {
		if err := retryCtx.Err(); err != nil {
			return err
		}

		machine, err := p.client.GetMachine(retryCtx, vp.MachineRentID())
		if err != nil {
			return err
		}

		vp.SetMachine(machine)

		switch machine.State {
		case virtualpod.MachineStateRunning:
			return nil
		case virtualpod.MachineStateFailed:
			return backoff.Permanent(ErrMachineFailed)
		default:
			return ErrMachineNotRunning
		}
	}

	return backoff.Retry(op, bo)
}
