package glami

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anex-sh/anex/cloudprovider"
	"github.com/anex-sh/anex/cloudprovider/mock"
	"github.com/anex-sh/anex/cloudprovider/runpod"
	"github.com/anex-sh/anex/cloudprovider/vastai"
	"github.com/anex-sh/anex/internal/utils"
	"github.com/anex-sh/anex/virtualpod"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/virtual-kubelet/virtual-kubelet/errdefs"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	"github.com/virtual-kubelet/virtual-kubelet/trace"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
)

// Provider implements the virtual-kubelet provider interface and stores pods in memory.
type Provider struct {
	nodeName              string
	operatingSystem       string
	internalIP            string
	daemonEndpointPort    int32
	virtualPods           map[string]*virtualpod.VirtualPod
	virtualPodsRestored   map[string]*virtualpod.VirtualPod
	virtualPodsToRestart  map[string]*virtualpod.VirtualPod
	podUpdateCh           chan *v1.Pod
	config                ProviderConfig
	startTime             time.Time
	notifier              func(*v1.Pod)
	client                cloudprovider.Client
	rc                    *retryablehttp.Client
	serverProxySettings   virtualpod.ProxyServerConfig
	clientProxySettings   []*virtualpod.ProxyClientConfig
	baseContext           context.Context
	provisioningWG        sync.WaitGroup
	mutex                 sync.RWMutex
	eventRecorder         record.EventRecorder
	eventRecorderShutdown func()
	k8s                   *kubernetes.Clientset
	metrics               *Metrics
}

func newCoreV1Recorder(client kubernetes.Interface, scheme *runtime.Scheme, component string) (record.EventRecorder, func()) {
	b := record.NewBroadcaster()
	// IMPORTANT: use the core/v1 sink here, not events/v1
	b.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: client.CoreV1().Events("")})
	rec := b.NewRecorder(scheme, v1.EventSource{Component: component})
	return rec, b.Shutdown
}

func NewGlamiProvider(providerConfig string, operatingSystem string, internalIP string, daemonEndpointPort int32) (*Provider, error) {
	ctx := context.Background()
	config, err := LoadConfig(providerConfig)
	if err != nil {
		return nil, err
	}

	clusterUUID := config.Cluster.ClusterUUID
	if clusterUUID == "" {
		return nil, fmt.Errorf("cluster.clusterUUID is not set in config")
	}

	// Build Kubernetes client (works in-cluster with ServiceAccount or out-of-cluster with kubeconfig)
	clientSet, err := newClusterClient()
	if err != nil {
		return nil, fmt.Errorf("failed to build kubernetes client: %v", err)
	}

	var scheme = runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	recorder, shutdown := newCoreV1Recorder(clientSet, scheme, "virtualpod-controller")

	provider := Provider{
		nodeName:              config.VirtualNode.NodeName,
		operatingSystem:       operatingSystem,
		internalIP:            internalIP,
		daemonEndpointPort:    daemonEndpointPort,
		virtualPods:           make(map[string]*virtualpod.VirtualPod),
		virtualPodsRestored:   make(map[string]*virtualpod.VirtualPod),
		virtualPodsToRestart:  make(map[string]*virtualpod.VirtualPod),
		podUpdateCh:           make(chan *v1.Pod, 100),
		config:                config,
		startTime:             time.Now(),
		rc:                    utils.NewDefaultRetryClient(),
		eventRecorder:         recorder,
		eventRecorderShutdown: shutdown,
		k8s:                   clientSet,
	}

	// Configure cloud provider
	switch strings.ToLower(config.CloudProvider.Active) {
	case "mock":
		provider.client = mock.NewClient(clusterUUID, config.VirtualNode.NodeName)
	case "runpod":
		provider.client = runpod.NewClient(
			config.CloudProvider.RunPod.APIKey,
			clusterUUID,
			config.VirtualNode.NodeName,
			runpod.URLConfig{
				InitURL:      config.CloudProvider.RunPod.InitURL,
				AgentURL:     config.CloudProvider.RunPod.AgentURL,
				WireproxyURL: config.CloudProvider.RunPod.WireproxyURL,
				WstunnelURL:  config.CloudProvider.RunPod.WstunnelURL,
				PromtailURL:  config.CloudProvider.RunPod.PromtailURL,
			},
		)
	case "vastai":
		banDuration := time.Duration(config.GetMachineBanDuration()) * time.Second
		bansConfig := vastai.BansConfig{
			Enable:   config.Provisioning.MachineBansStore.LocalFile.Enable,
			FilePath: config.Provisioning.MachineBansStore.LocalFile.Path,
			Duration: banDuration,
		}
		provider.client = vastai.NewClient("https://console.vast.ai/api/v0", config.CloudProvider.VastAI.APIKey, clusterUUID, config.VirtualNode.NodeName, bansConfig)
	default:
		return nil, fmt.Errorf("unknown cloud provider: %s", config.CloudProvider.Active)
	}

	// Initialize WireGuard keys and assignments if proxy is enabled
	wgKeysDirty := false
	err = provider.loadProxyConfig()
	if err != nil {
		log.G(ctx).Errorf("failed to load wireguard keys: %v", err)
	}

	dirtyFilePath := filepath.Join(filepath.Dir(config.Gateway.ConfigPath), ".dirty")
	if _, err := os.Stat(dirtyFilePath); err == nil {
		wgKeysDirty = true
	}

	// TODO: Temporary build hack
	log.G(ctx).Infof("dirty: %v", wgKeysDirty)

	// Load persisted machine bans for VastAI provider
	if vastaiClient, ok := provider.client.(*vastai.Client); ok && config.Provisioning.MachineBansStore.LocalFile.Enable {
		bansPath := config.Provisioning.MachineBansStore.LocalFile.Path
		if _, err := os.Stat(bansPath); os.IsNotExist(err) {
			if err := os.MkdirAll(filepath.Dir(bansPath), 0o755); err != nil {
				log.G(ctx).Errorf("failed to create directory for bans file: %v", err)
			} else {
				if err := os.WriteFile(bansPath, []byte("{}"), 0o600); err != nil {
					log.G(ctx).Errorf("failed to create bans file: %v", err)
				}
			}
		}

		// TODO: Remove temporary init
		if bansOverwrite := os.Getenv("BANS_OVERWRITE"); bansOverwrite != "" {
			if err := os.WriteFile(bansPath, []byte(bansOverwrite), 0o600); err != nil {
				log.G(ctx).Errorf("failed to write bans overwrite to file: %v", err)
			}
		}

		if err := vastaiClient.LoadMachineBansFromFile(); err != nil {
			log.G(ctx).Errorf("failed to load machine bans: %v", err)
		}
	}

	provider.metrics = NewMetrics()

	// Map existing machines to running pods on startup (recovery after restart)
	pods, err := listPodsForNode(ctx, clientSet, provider.nodeName)
	if err != nil {
		return nil, fmt.Errorf("error listing pods: %v", err)
	}

	podsMachinesMapping, err := provider.client.MapRunningMachines(ctx, pods)
	if err != nil {
		log.G(ctx).Errorf("failed to map running machines: %v", err)
		podsMachinesMapping = make(map[string]*virtualpod.Machine)
	}

	for _, pod := range pods.Items {
		if pod.Status.Phase != v1.PodRunning {
			continue
		}

		key := buildKey(&pod)
		proxySlotIndex, _ := strconv.Atoi(pod.Annotations["anex.sh/proxy-slot-id"])
		if provider.clientProxySettings[proxySlotIndex].Assigned {
			log.G(ctx).Errorf("proxy slot %d already assigned, skipping pod %s", proxySlotIndex, key)
			continue
		}
		provider.clientProxySettings[proxySlotIndex].Assigned = true

		if machine, ok := podsMachinesMapping[string(pod.UID)]; ok {
			port := 10000 + proxySlotIndex*100
			vp := virtualpod.NewVirtualPod(key, &pod, machine, proxySlotIndex, nil, nil, provider.config.AgentAuthToken)
			vp.SetAgentPort(port)

			if rc, ok := provider.client.(*runpod.Client); ok {
				ep := fmt.Sprintf("http://10.254.254.%d:%d", 11+proxySlotIndex, port)
				rc.RegisterAgentEndpoint(machine.ID, ep)
			}

			if wgKeysDirty {
				log.G(ctx).Info("Renewing machine keys")
			}
			vp.SetProvisioningCompleted(true)
			provider.virtualPodsRestored[key] = vp
		} else {
			if pod.Spec.RestartPolicy != v1.RestartPolicyNever {
				vp := virtualpod.NewVirtualPod(key, &pod, &virtualpod.Machine{}, proxySlotIndex, nil, nil, provider.config.AgentAuthToken)
				provider.virtualPodsToRestart[key] = vp
			} else {
				pod.Status.Phase = v1.PodFailed
				pod.Status.ContainerStatuses[0].State = v1.ContainerState{
					Terminated: &v1.ContainerStateTerminated{
						ExitCode:   1,
						Reason:     "Failed",
						FinishedAt: metav1.Now(),
					},
				}
				provider.notifyPodUpdate(pod.DeepCopy())
			}
		}
	}

	return &provider, nil
}

func (p *Provider) machineGarbageCollector(ctx context.Context) {
	logger := log.G(ctx)

	// List pods for virtual node as registered by apiserver
	pods, err := listPodsForNode(ctx, p.k8s, p.nodeName)
	if err != nil {
		logger.Errorf("failed to list pods for node %s: %v", p.nodeName, err)
		return
	}

	// Delete any dangling machines with non-matching pod UID let's compare two attacks: ATK A1, AP4, Deadly(6) and plain ATK A6, AP4
	var podUIDS []string
	for _, pod := range pods.Items {
		podUIDS = append(podUIDS, string(pod.UID))
	}

	// TODO: Map running machines to pods
	err = p.client.PruneDanglingMachines(ctx, podUIDS)
	if err != nil {
		log.G(ctx).Errorf("failed to prune dangling machines: %v", err)
	}
}

func buildKeyFromNames(namespace, name string) string {
	return fmt.Sprintf("%s-%s", namespace, name)
}

func buildKey(pod *v1.Pod) string {
	return fmt.Sprintf("%s-%s", pod.Namespace, pod.Name)
}

// addAttributes adds the specified attributes to the provided span.
// attrs must be an even-sized list of string arguments.
// Otherwise, the span won't be modified.
func addAttributes(ctx context.Context, span trace.Span, attrs ...string) context.Context {
	if len(attrs)%2 == 1 {
		return ctx
	}
	for i := 0; i < len(attrs); i += 2 {
		ctx = span.WithField(ctx, attrs[i], attrs[i+1])
	}
	return ctx
}

func (p *Provider) ProvisioningWG() *sync.WaitGroup {
	return &p.provisioningWG
}

func (p *Provider) NodeName() string {
	return p.nodeName
}

func (p *Provider) reserveGatewaySlot() (int, error) {
	//if !p.config.Gateway.Enable {
	//	return 0, nil
	//}

	for idx, proxy := range p.clientProxySettings {
		if proxy.Assigned {
			continue
		}

		proxy.Assigned = true
		return idx, nil
	}

	return 0, fmt.Errorf("no free Gateway slot available")
}

func (p *Provider) getPodProxyConfigById(id int) (podProxyConfig virtualpod.PodProxyConfig, err error) {
	clientProxy := p.clientProxySettings[id]
	if !clientProxy.Assigned {
		return podProxyConfig, fmt.Errorf("proxy slot id=%d not assigned", id)
	}

	config := virtualpod.PodProxyConfig{
		Enabled: true,
		Server:  p.serverProxySettings,
		Client:  *clientProxy,
	}

	return config, nil
}

// CreatePod accepts a pod definition and stores it in memory.
func (p *Provider) CreatePod(ctx context.Context, pod *v1.Pod) error {
	logger := log.G(p.baseContext)
	logger = logger.WithFields(log.Fields{"pod.name": pod.Name, "pod.namespace": pod.Namespace})

	p.metrics.createPodOperationsTotal.Inc()
	p.metrics.podsByPhase.WithLabelValues("Pending").Inc()
	ctx, span := trace.StartSpan(ctx, "CreatePod")
	defer span.End()

	// Add the pod's coordinates to the current span.
	ctx = addAttributes(ctx, span, namespaceKey, pod.Namespace, nameKey, pod.Name)

	logger.Info("Creating pod")
	p.eventRecorder.Event(pod, v1.EventTypeNormal, "Creating", "Creating pod")

	if len(pod.Spec.Containers) > 1 {
		return fmt.Errorf("Glami Provider does not support multiple containers")
	}

	var err error
	var gatewaySlotId int
	gatewaySlotId, err = p.reserveGatewaySlot()

	// TODO: No Gateway for mock
	//gatewaySlotId = 0
	//err = nil

	if err != nil {
		return err
	}
	pod.Annotations["anex.sh/proxy-slot-id"] = strconv.Itoa(gatewaySlotId)

	now := metav1.NewTime(time.Now())
	pod.Status = v1.PodStatus{
		Phase:     v1.PodPending,
		HostIP:    "1.2.3.4",
		PodIP:     "5.6.7.8",
		StartTime: &now,
		Conditions: []v1.PodCondition{
			{
				Type:   v1.PodScheduled,
				Status: v1.ConditionTrue,
			},
		},
	}

	c := pod.Spec.Containers[0]
	pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, v1.ContainerStatus{
		Name:         c.Name,
		Image:        c.Image,
		Ready:        false,
		RestartCount: 0,
		State: v1.ContainerState{
			Waiting: &v1.ContainerStateWaiting{
				Message: "Waiting machine init and container start",
			},
		},
	})

	p.notifyPodUpdate(pod)
	configMaps := p.loadMountedConfigMaps(ctx, pod)
	mountPaths, _ := buildMountedConfigMaps(pod, configMaps)

	key := buildKey(pod)
	vp := virtualpod.NewVirtualPod(key, pod, &virtualpod.Machine{}, gatewaySlotId, configMaps, mountPaths, p.config.AgentAuthToken)

	createCtx, cancel := context.WithCancel(p.baseContext)
	annotatedLogger := logger.WithFields(log.Fields{"pod.name": pod.Name, "pod.namespace": pod.Namespace})
	createCtx = log.WithLogger(createCtx, annotatedLogger)

	vp.ProvisionCancel = cancel
	p.mutex.Lock()
	p.virtualPods[key] = vp
	p.mutex.Unlock()

	p.provisioningWG.Add(1)
	go p.initializeVirtualPod(createCtx, vp, false)

	return nil
}

// UpdatePod accepts a pod definition and updates its reference.
func (p *Provider) UpdatePod(ctx context.Context, pod *v1.Pod) error {
	logger := log.G(p.baseContext)
	logger = logger.WithFields(log.Fields{"pod.name": pod.Name, "pod.namespace": pod.Namespace})

	p.metrics.updatePodOperationsTotal.Inc()
	ctx, span := trace.StartSpan(ctx, "UpdatePod")
	defer span.End()

	logger.Infof("receive UpdatePod", pod.Name)

	key := buildKey(pod)
	p.mutex.Lock()
	p.virtualPods[key].UpdatePod(pod)
	p.mutex.Unlock()

	p.notifyPodUpdate(pod)

	return nil
}

// DeletePod deletes the specified pod out of memory.
func (p *Provider) DeletePod(ctx context.Context, pod *v1.Pod) (err error) {
	logger := log.G(p.baseContext)
	logger = logger.WithFields(log.Fields{"pod.name": pod.Name, "pod.namespace": pod.Namespace})

	p.metrics.deletePodOperationsTotal.Inc()
	p.metrics.podsByPhase.WithLabelValues("Deleted").Inc()
	ctx, span := trace.StartSpan(ctx, "DeletePod")
	defer span.End()

	logger.Info("received delete pod")
	p.eventRecorder.Event(pod, v1.EventTypeNormal, "Deleting", "Deleting pod")

	key := buildKey(pod)

	vp, exists := p.virtualPods[key]
	if !exists {
		logger.Warn("Pod not found during deletion")
		return errdefs.NotFound("pod not found")
	}

	// TODO: Refactor this!
	vp.SyncUpdateDelete.Lock()
	defer vp.SyncUpdateDelete.Unlock()

	// TODO: Change to sending SIGTERM, setting status Terminated, notify update
	//		 Complete refactoring needed!
	if vp.ProvisioningCompleted() {
		if !vp.Finalized() {
			logger.Infof("Terminating machine %s", vp.MachineRentID())
			vp.LifecycleCancel()
			vp.TerminatePod(0)

			err = p.client.DestroyMachine(p.baseContext, vp.MachineRentID())
			if err != nil {
				logger.Errorf("Error destroying instance: %v", err)
				return err
			}

			p.clientProxySettings[vp.ProxySlot()].Assigned = false
			vp.Finalize()

			logger.Info("Machine destroyed and resources cleaned up")
			p.metrics.podsRunning.Dec()
			p.metrics.podsByPhase.WithLabelValues("Deleted").Inc()
		}
		p.mutex.Lock()
		delete(p.virtualPods, key)
		p.mutex.Unlock()
	} else {
		logger.Info("Cancelling provisioning for pod")
		vp.ProvisionCancel()
		p.mutex.Lock()
		p.clientProxySettings[vp.ProxySlot()].Assigned = false
		delete(p.virtualPods, key)
		p.mutex.Unlock()
		vp.TerminatePod(0)
		// TODO: What about pod's phase??
	}

	// Avoid Metadata overwrite
	pod.Status = *vp.PodStatus().DeepCopy()

	// TODO: This should return AFTER DeletePod returns and processes (After grace anyway)
	p.notifyPodUpdate(pod)

	return nil
}

// GetPod returns a pod by name that is stored in memory.
func (p *Provider) GetPod(ctx context.Context, namespace, name string) (pod *v1.Pod, err error) {
	logger := log.G(p.baseContext)
	p.metrics.getPodOperationsTotal.Inc()
	ctx, span := trace.StartSpan(ctx, "GetPod")
	defer func() {
		span.SetStatus(err)
		span.End()
	}()

	logger.Infof("received GetPod for %s/%s", namespace, name)

	key := buildKeyFromNames(namespace, name)

	if vp, ok := p.virtualPods[key]; ok {
		return vp.Pod(), nil
	}
	return nil, errdefs.NotFoundf("pod \"%s/%s\" is not known to the provider", namespace, name)
}

// GetPodStatus returns the status of a pod by name that is "running".
// returns nil if a pod by that name is not found.
func (p *Provider) GetPodStatus(ctx context.Context, namespace, name string) (*v1.PodStatus, error) {
	p.metrics.getPodStatusOperationsTotal.Inc()
	ctx, span := trace.StartSpan(ctx, "ContainerState")
	defer span.End()

	key := buildKeyFromNames(namespace, name)

	p.mutex.RLock()
	vp, ok := p.virtualPods[key]
	p.mutex.RUnlock()

	if !ok {
		return nil, errdefs.NotFoundf("pod \"%s/%s\" is not known to the provider", namespace, name)
	}

	return vp.PodStatus(), nil
}

// GetPods returns a list of all pods known to be "running".
func (p *Provider) GetPods(ctx context.Context) ([]*v1.Pod, error) {
	logger := log.G(p.baseContext)
	p.metrics.getPodsOperationsTotal.Inc()
	ctx, span := trace.StartSpan(ctx, "GetPods")
	defer span.End()

	logger.Info("received GetPods")

	var pods []*v1.Pod

	p.mutex.RLock()
	defer p.mutex.RUnlock()

	for _, vp := range p.virtualPods {
		pods = append(pods, vp.Pod())
	}

	return pods, nil
}

func restartBackoff(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (p *Provider) reconcilePodLifecycle(ctx context.Context, vp *virtualpod.VirtualPod) {
	logger := log.G(p.baseContext)
	logger = logger.WithFields(log.Fields{"pod.name": vp.PodName(), "pod.namespace": vp.PodNamespace()})

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	client := retryablehttp.NewClient()
	client.RetryWaitMin = 1 * time.Second
	client.RetryWaitMax = 4 * time.Second
	client.RetryMax = 4
	client.HTTPClient.Timeout = 10 * time.Second
	client.Logger = nil

	lastStatusUpdateTime := time.Now()
	statusReportTimeout := p.config.GetStatusReportTimeout()

	reconcile := func() {
		// TODO: Refactor this!
		vp.SyncUpdateDelete.Lock()
		defer vp.SyncUpdateDelete.Unlock()
		if ctx.Err() != nil {
			return
		}

		logger.Debugf("Reconcile loop for pod %s", vp.PodName())
		update, err := vp.PodStatusUpdate(ctx, client)
		if err != nil {
			logger.Warnf("Error getting pod status: %v", err)

			if time.Since(lastStatusUpdateTime) > statusReportTimeout {
				logger.Errorf("Pod status report timeout exceeded (%v), failing pod", statusReportTimeout)
				restarts := vp.Pod().Spec.RestartPolicy != v1.RestartPolicyNever
				if !restarts {
					vp.FailContainer("Pod stopped responding")
				}

				update = virtualpod.StatusUpdate{
					Changed:    true,
					Terminated: !restarts,
					Succeeded:  false,
					Restarts:   restarts,
				}
			}
		} else {
			lastStatusUpdateTime = time.Now()
		}

		if update.Changed {
			p.notifyPodUpdate(vp.Pod())
		}

		statusLabel := "Failed"
		if update.Succeeded {
			statusLabel = "Succeeded"
		}

		if update.Restarts {
			logger.Infof("Container is restarting")
			p.eventRecorder.Eventf(vp.Pod(), v1.EventTypeWarning, "ContainerRestarting", "Container is restarting")
			p.metrics.containerRestarts.WithLabelValues(statusLabel).Inc()
			p.metrics.podsRunning.Dec()

			if update.Backoff > 0 {
				err = restartBackoff(ctx, update.Backoff)
				if err != nil {
					return
				}
				vp.CrashLoopBackOffDone()
				p.notifyPodUpdate(vp.Pod())
			}

			restartCtx, restartCancel := context.WithCancel(p.baseContext)
			vp.ProvisionCancel = restartCancel

			vp.LifecycleCancel()
			p.provisioningWG.Add(1)
			go p.initializeVirtualPod(restartCtx, vp, true)
			return
		}

		if update.Terminated {
			logger.Infof("Container terminated with status: %s", statusLabel)
			p.eventRecorder.Eventf(vp.Pod(), v1.EventTypeNormal, "ContainerTerminated", "Container terminated with status: %s", statusLabel)
			p.metrics.podsByPhase.WithLabelValues(statusLabel).Inc()
			p.metrics.podsRunning.Dec()

			logger.Info("Finalizing pod after container termination")
			p.mutex.Lock()
			key := buildKey(vp.Pod())
			p.virtualPods[key].LifecycleCancel()
			p.clientProxySettings[vp.ProxySlot()].Assigned = false
			p.mutex.Unlock()
			vp.Finalize()

			machineID := vp.MachineRentID()
			// TODO: Implement retry
			p.client.DestroyMachine(p.baseContext, machineID)
		}
	}

	// Likely already running at this point
	reconcile()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reconcile()
		}
	}
}

// GetContainerLogs retrieves the logs of a container by name from the provider.
func (p *Provider) GetContainerLogs(ctx context.Context, namespace, podName, containerName string, opts api.ContainerLogOpts) (io.ReadCloser, error) {
	ctx, span := trace.StartSpan(ctx, "GetContainerLogs")
	defer span.End()

	// Add pod and container attributes to the current span.
	ctx = addAttributes(ctx, span, namespaceKey, namespace, nameKey, podName, containerNameKey, containerName)

	logger := log.G(ctx)
	logger.Infof("GetContainerLogs for pod %s/%s container %s", namespace, podName, containerName)

	key := buildKeyFromNames(namespace, podName)

	p.mutex.RLock()
	vp, ok := p.virtualPods[key]
	p.mutex.RUnlock()

	if !ok {
		return nil, errdefs.NotFoundf("pod %s/%s not found", namespace, podName)
	}

	// Build the logs URL with query parameters
	logsURL := vp.GetAgentAddress() + "/logs"

	// Add query parameters
	query := make(map[string]string)
	if opts.Follow {
		query["follow"] = "true"
	}
	if opts.Tail > 0 {
		query["tail"] = fmt.Sprintf("%d", opts.Tail)
	}

	// Build URL with query string
	if len(query) > 0 {
		logsURL += "?"
		first := true
		for k, v := range query {
			if !first {
				logsURL += "&"
			}
			logsURL += fmt.Sprintf("%s=%s", k, v)
			first = false
		}
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, logsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create logs request: %w", err)
	}

	// Add auth header if configured
	if p.config.AgentAuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.config.AgentAuthToken)
	}

	// Make the request
	httpClient := &http.Client{
		Timeout: 0, // No timeout for streaming logs
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get logs from agent: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("agent returned status %d for logs request", resp.StatusCode)
	}

	return resp.Body, nil
}

// NotifyPods is called to set a pod notifier callback function. This should be called before any operations are done
// within the provider.
func (p *Provider) NotifyPods(ctx context.Context, notifier func(*v1.Pod)) {
	p.notifier = notifier
}

func (p *Provider) notifyPodUpdate(pod *v1.Pod) {
	p.podUpdateCh <- pod
}
