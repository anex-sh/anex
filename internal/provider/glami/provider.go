package glami

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/virtual-kubelet/virtual-kubelet/errdefs"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"github.com/virtual-kubelet/virtual-kubelet/trace"
	"gitlab.devklarka.cz/ai/gpu-provider/cloudprovider"
	"gitlab.devklarka.cz/ai/gpu-provider/cloudprovider/vastai"
	"gitlab.devklarka.cz/ai/gpu-provider/internal/utils"
	"gitlab.devklarka.cz/ai/gpu-provider/virtualpod"
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
	podUpdateCh           chan *v1.Pod
	config                ProviderConfig
	startTime             time.Time
	notifier              func(*v1.Pod)
	client                cloudprovider.Client
	rc                    *retryablehttp.Client
	serverProxySettings   virtualpod.ProxyServerConfig
	clientProxySettings   []virtualpod.ProxyClientConfig
	baseContext           context.Context
	provisioningWG        sync.WaitGroup
	mutex                 sync.RWMutex
	eventRecorder         record.EventRecorder
	eventRecorderShutdown func()
	machineBans           map[string]time.Time
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

func NewGlamiProvider(providerConfig string, nodeName, operatingSystem string, internalIP string, daemonEndpointPort int32) (*Provider, error) {
	config, err := loadConfig(providerConfig, nodeName)
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
		nodeName:              nodeName,
		operatingSystem:       operatingSystem,
		internalIP:            internalIP,
		daemonEndpointPort:    daemonEndpointPort,
		virtualPods:           make(map[string]*virtualpod.VirtualPod),
		podUpdateCh:           make(chan *v1.Pod, 100),
		config:                config,
		startTime:             time.Now(),
		rc:                    utils.NewDefaultRetryClient(),
		eventRecorder:         recorder,
		eventRecorderShutdown: shutdown,
		machineBans:           make(map[string]time.Time),
		k8s:                   clientSet,
	}

	// Configure cloud provider - currently only VastAI is supported
	provider.client = vastai.NewClient("https://console.vast.ai/api/v0", config.CloudProvider.VastAI.APIKey, clusterUUID, nodeName)

	ctx := context.Background()
	// Initialize WireGuard keys and assignments if proxy is enabled
	if config.Proxy.Enable {
		err = provider.loadProxyConfig()
		if err != nil {
			log.G(ctx).Errorf("failed to load wireguard keys: %v", err)
		}
	}

	// Load persisted machine bans if configured
	if config.VirtualKubelet.Provisioning.MachineBansStore.LocalFile.Enable {
		if err := provider.loadMachineBansFromFile(); err != nil {
			log.G(ctx).Errorf("failed to load machine bans: %v", err)
		}
	}

	provider.metrics = NewMetrics()

	// List pods for virtual node as registered by apiserver
	//pods, err := listPodsForNode(ctx, clientSet, provider.nodeName)
	//if err != nil {
	//	return nil, fmt.Errorf("error listing pods: %v", err)
	//}

	// Delete any dangling machines with non-matching pod UID
	// TODO: Enable pod listing after machine to pod mapping fix
	var podUIDS []string
	//for _, pod := range pods.Items {
	//	podUIDS = append(podUIDS, string(pod.UID))
	//}

	// TODO: Map running machines to pods
	err = provider.client.PruneDanglingMachines(ctx, podUIDS)
	if err != nil {
		log.G(ctx).Errorf("failed to prune dangling machines: %v", err)
	}

	return &provider, nil
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

func (p *Provider) getProxyConfigForVirtualPod() (*virtualpod.ProxyClientConfig, error) {
	for _, proxy := range p.clientProxySettings {
		if proxy.Assigned {
			continue
		}

		proxy.Assigned = true
		return &proxy, nil
	}

	return nil, fmt.Errorf("no proxy keys available")
}

// CreatePod accepts a pod definition and stores it in memory.
func (p *Provider) CreatePod(ctx context.Context, pod *v1.Pod) error {
	p.metrics.createPodOperationsTotal.Inc()
	p.metrics.podsByPhase.WithLabelValues("Pending").Inc()
	ctx, span := trace.StartSpan(ctx, "CreatePod")
	defer span.End()

	// Add the pod's coordinates to the current span.
	ctx = addAttributes(ctx, span, namespaceKey, pod.Namespace, nameKey, pod.Name)

	log.G(ctx).Infof("receive CreatePod %q", pod.Name)

	if len(pod.Spec.Containers) > 1 {
		return fmt.Errorf("Glami Provider does not support multiple containers")
	}

	cfg, err := p.getProxyConfigForVirtualPod()
	if err != nil {
		return err
	}
	proxyConfig := virtualpod.ProxyConfig{
		Server: p.serverProxySettings,
		Client: *cfg,
	}

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

	configMaps := p.loadMountedConfigMaps(ctx, pod)
	mountPaths, _ := buildMountedConfigMaps(pod, configMaps)

	key := buildKey(pod)
	createCtx, cancel := context.WithCancel(p.baseContext)

	vp := virtualpod.NewVirtualPod(key, pod, &virtualpod.Machine{}, proxyConfig, configMaps, mountPaths, p.config.AgentAuthToken)
	vp.ProvisionCancel = cancel
	p.virtualPods[key] = vp

	p.provisioningWG.Add(1)
	go p.initializeVirtualPod(createCtx, vp, false)

	return nil
}

// UpdatePod accepts a pod definition and updates its reference.
func (p *Provider) UpdatePod(ctx context.Context, pod *v1.Pod) error {
	p.metrics.updatePodOperationsTotal.Inc()
	ctx, span := trace.StartSpan(ctx, "UpdatePod")
	defer span.End()

	// Add the pod's coordinates to the current span.
	ctx = addAttributes(ctx, span, namespaceKey, pod.Namespace, nameKey, pod.Name)

	log.G(ctx).Infof("receive UpdatePod %q", pod.Name)

	//key, err := buildKey(pod)
	//if err != nil {
	//	return err
	//}
	// TODO: Fix update
	// p.notifier(pod)

	return nil
}

// DeletePod deletes the specified pod out of memory.
func (p *Provider) DeletePod(ctx context.Context, pod *v1.Pod) (err error) {
	p.metrics.deletePodOperationsTotal.Inc()
	ctx, span := trace.StartSpan(ctx, "DeletePod")
	defer span.End()

	// Add the pod's coordinates to the current span.
	ctx = addAttributes(ctx, span, namespaceKey, pod.Namespace, nameKey, pod.Name)

	log.G(ctx).Infof("receive DeletePod %q", pod.Name)

	key := buildKey(pod)

	vp, exists := p.virtualPods[key]
	if !exists {
		return errdefs.NotFound("pod not found")
	}

	p.mutex.Lock()
	defer p.mutex.Unlock()

	if vp.ProvisioningCompleted() {
		vp.LifecycleCancel()
		p.metrics.podsRunning.Dec()
		err = p.client.DestroyMachine(ctx, vp.MachineID())
		if err != nil {
			log.G(ctx).Infof("Error destroying instance: %v", err)
			return err
		}

		delete(p.virtualPods, key)
	} else {
		vp.ProvisionCancel()
		delete(p.virtualPods, key)
	}

	// p.notifier(pod)

	return nil
}

// GetPod returns a pod by name that is stored in memory.
func (p *Provider) GetPod(ctx context.Context, namespace, name string) (pod *v1.Pod, err error) {
	p.metrics.getPodOperationsTotal.Inc()
	ctx, span := trace.StartSpan(ctx, "GetPod")
	defer func() {
		span.SetStatus(err)
		span.End()
	}()

	// Add the pod's coordinates to the current span.
	ctx = addAttributes(ctx, span, namespaceKey, namespace, nameKey, name)

	// log.G(ctx).Infof("receive GetPod %q", name)

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

	log.G(ctx).Infof("receive ContainerState %q", name)
	// Add namespace and name as attributes to the current span.
	ctx = addAttributes(ctx, span, namespaceKey, namespace, nameKey, name)

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
	p.metrics.getPodsOperationsTotal.Inc()
	ctx, span := trace.StartSpan(ctx, "GetPods")
	defer span.End()

	log.G(ctx).Info("receive GetPods")

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
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	client := retryablehttp.NewClient()
	client.RetryWaitMin = 200 * time.Millisecond
	client.RetryWaitMax = 5 * time.Second
	client.HTTPClient.Timeout = 10 * time.Second

	reconcile := func() {
		update, err := vp.PodStatusUpdate(ctx, client)
		if err != nil {
			log.G(ctx).Errorf("Error getting pod status: %v", err)
			return
		}

		if update.Changed {
			p.notifyPodUpdate(vp.Pod())
		}

		if update.Restarts {
			err := restartBackoff(ctx, update.Backoff)
			if err != nil {
				return
			}

			restartCtx, restartCancel := context.WithCancel(p.baseContext)
			p.mutex.Lock()
			vp.ProvisionCancel = restartCancel
			p.mutex.Unlock()

			vp.LifecycleCancel()
			p.provisioningWG.Add(1)
			go p.initializeVirtualPod(restartCtx, vp, true)
			return
		}

		if update.Terminated {
			p.mutex.Lock()
			delete(p.virtualPods, vp.ID())
			p.mutex.Unlock()

			machineID := vp.MachineID()
			// TODO: Implement retry
			p.client.DestroyMachine(ctx, machineID)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reconcile()
		}
	}
}

// NotifyPods is called to set a pod notifier callback function. This should be called before any operations are done
// within the provider.
func (p *Provider) NotifyPods(ctx context.Context, notifier func(*v1.Pod)) {
	p.notifier = notifier
}

func (p *Provider) notifyPodUpdate(pod *v1.Pod) {
	p.podUpdateCh <- pod
}
