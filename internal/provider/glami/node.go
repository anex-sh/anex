package glami

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"github.com/virtual-kubelet/virtual-kubelet/trace"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	v2 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RunMetricsServer starts an HTTP server that serves Prometheus metrics on /metrics.
func RunMetricsServer(ctx context.Context, addr string, registry *prometheus.Registry) error {
	logger := log.G(ctx)
	mux := http.NewServeMux()
	// mux.Handle("/metrics", promhttp.Handler())
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		// Leave these defaults; no extra instrumentation here.
	}))

	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	// Bind early so we can return startup errors (e.g., port in use)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	// Shutdown when parent ctx is canceled
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Infof("metrics server shutdown error: %v", err)
		}
	}()

	// Serve in the background
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Errorf("metrics server error: %v", err)
		}
	}()

	return nil
}

func (p *Provider) ConfigureNode(ctx context.Context, n *v1.Node) {
	logger := log.G(ctx)
	ctx, span := trace.StartSpan(ctx, "cloud.ConfigureNode") //nolint:staticcheck,ineffassign
	defer span.End()

	p.baseContext = ctx

	n.Status.Capacity = p.capacity()
	n.Status.Allocatable = p.capacity()
	n.Status.Conditions = p.nodeConditions()
	n.Status.Addresses = p.nodeAddresses()
	n.Status.DaemonEndpoints = p.nodeDaemonEndpoints()
	os := p.operatingSystem
	if os == "" {
		os = "linux"
	}
	n.Status.NodeInfo.OperatingSystem = os
	n.Status.NodeInfo.Architecture = "amd64"
	n.Labels["alpha.service-controller.kubernetes.io/exclude-balancer"] = "true"
	n.Labels["node.kubernetes.io/exclude-from-external-load-balancers"] = "true"

	// Append the following taint so cluster-autoscaler ignores this node
	// ignore-taint.cluster-autoscaler.kubernetes.io/manual-ignore=true:NoSchedule
	already := false
	for _, t := range n.Spec.Taints {
		if t.Key == "ignore-taint.cluster-autoscaler.kubernetes.io/manual-ignore" && t.Value == "true" && t.Effect == v1.TaintEffectNoSchedule {
			already = true
			break
		}
	}
	if !already {
		n.Spec.Taints = append(n.Spec.Taints, v1.Taint{
			Key:    "ignore-taint.cluster-autoscaler.kubernetes.io/manual-ignore",
			Value:  "true",
			Effect: v1.TaintEffectNoSchedule,
		})
	}

	// Start the notifier goroutine
	go func(ctx context.Context) {
		for {
			select {
			case <-ctx.Done():
				return
			case pod := <-p.podUpdateCh:
				if pod == nil {
					continue
				}
				if p.notifier != nil {
					p.notifier(pod)
				}
			}
		}
	}(p.baseContext)

	p.metrics.nodeStatus.WithLabelValues("Ready").Set(1)
	p.metrics.nodeStatus.WithLabelValues("NotReady").Set(0)

	if err := RunMetricsServer(ctx, ":9090", p.metrics.reg); err != nil {
		logger.Errorf("failed to start metrics server: %v", err)
	}
}

// Capacity returns a resource list containing the capacity limits.
func (p *Provider) capacity() v1.ResourceList {
	rl := v1.ResourceList{
		"cpu":    resource.MustParse(p.config.VirtualNode.CPU),
		"memory": resource.MustParse(p.config.VirtualNode.Memory),
		"pods":   resource.MustParse(p.config.VirtualNode.Pods),
	}
	return rl
}

// NodeConditions returns a list of conditions (Ready, OutOfDisk, etc), for updates to the node status
// within Kubernetes.
func (p *Provider) nodeConditions() []v1.NodeCondition {
	// TODO: Make this configurable
	return []v1.NodeCondition{
		{
			Type:               "Ready",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  v2.Now(),
			LastTransitionTime: v2.Now(),
			Reason:             "KubeletPending",
			Message:            "kubelet is pending.",
		},
		{
			Type:               "OutOfDisk",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  v2.Now(),
			LastTransitionTime: v2.Now(),
			Reason:             "KubeletHasSufficientDisk",
			Message:            "kubelet has sufficient disk space available",
		},
		{
			Type:               "MemoryPressure",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  v2.Now(),
			LastTransitionTime: v2.Now(),
			Reason:             "KubeletHasSufficientMemory",
			Message:            "kubelet has sufficient memory available",
		},
		{
			Type:               "DiskPressure",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  v2.Now(),
			LastTransitionTime: v2.Now(),
			Reason:             "KubeletHasNoDiskPressure",
			Message:            "kubelet has no disk pressure",
		},
		{
			Type:               "NetworkUnavailable",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  v2.Now(),
			LastTransitionTime: v2.Now(),
			Reason:             "RouteCreated",
			Message:            "RouteController created a route",
		},
	}

}

// NodeAddresses returns a list of addresses for the node status
// within Kubernetes.
func (p *Provider) nodeAddresses() []v1.NodeAddress {
	return []v1.NodeAddress{
		{
			Type:    "InternalIP",
			Address: p.internalIP,
		},
	}
}

// NodeDaemonEndpoints returns NodeDaemonEndpoints for the node status
// within Kubernetes.
func (p *Provider) nodeDaemonEndpoints() v1.NodeDaemonEndpoints {
	return v1.NodeDaemonEndpoints{
		KubeletEndpoint: v1.DaemonEndpoint{
			Port: p.daemonEndpointPort,
		},
	}
}
