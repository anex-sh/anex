package glami

import (
	"context"
	"math/rand"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_model/go"
	"github.com/virtual-kubelet/virtual-kubelet/trace"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubelet/pkg/apis/stats/v1alpha1"
)

type Metrics struct {
	reg *prometheus.Registry

	// existing metrics
	requestsTotal   prometheus.Counter
	inFlight        prometheus.Gauge
	workLatencySecs prometheus.Histogram

	// pod operation counters
	getPodOperationsTotal       prometheus.Counter
	getPodStatusOperationsTotal prometheus.Counter
	getPodsOperationsTotal      prometheus.Counter
	createPodOperationsTotal    prometheus.Counter
	updatePodOperationsTotal    prometheus.Counter
	deletePodOperationsTotal    prometheus.Counter

	// pod info metrics
	containerRestarts            *prometheus.CounterVec // label: result (Succeeded|Failed)
	podsByPhase                  *prometheus.CounterVec // label: phase
	podsRunning                  prometheus.Gauge       // gauge of running pods
	podsProvisioningTotal        *prometheus.CounterVec // labels: success, reason
	podsProvisioningDurationSecs prometheus.Counter     // total seconds spent provisioning

	// node health
	nodeStatus *prometheus.GaugeVec // label: status
}

func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()

	m := &Metrics{
		reg: reg,

		// pod operation counters
		getPodOperationsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "get_pod_operations_total",
			Help: "Total number of GetPod operations.",
		}),
		getPodStatusOperationsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "get_pod_status_operations_total",
			Help: "Total number of GetPodStatus operations.",
		}),
		getPodsOperationsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "get_pods_operations_total",
			Help: "Total number of GetPods operations.",
		}),
		createPodOperationsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "create_pod_operations_total",
			Help: "Total number of CreatePod operations.",
		}),
		updatePodOperationsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "update_pod_operations_total",
			Help: "Total number of UpdatePod operations.",
		}),
		deletePodOperationsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "delete_pod_operations_total",
			Help: "Total number of DeletePod operations.",
		}),

		// pod info
		containerRestarts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "container_restarts_total",
			Help: "Total number of container restarts labeled by result.",
		}, []string{"result"}),
		// TODO: Change phase to enum
		podsByPhase: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "pods",
			Help: "Count of pods by phase.",
		}, []string{"phase"}),
		podsRunning: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "pods_running",
			Help: "Current number of running pods.",
		}),
		podsProvisioningTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "pods_provisioning_total",
			Help: "Number of pod provisioning attempts by success and reason.",
		}, []string{"success", "reason"}),
		podsProvisioningDurationSecs: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "pods_provisioning_duration_seconds",
			Help: "Total time in seconds spent provisioning pods.",
		}),

		// node health
		nodeStatus: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "node",
			Help: "Node status gauge labeled by status (Ready/NotReady).",
		}, []string{"status"}),
	}

	reg.MustRegister(
		m.getPodOperationsTotal,
		m.getPodStatusOperationsTotal,
		m.getPodsOperationsTotal,
		m.createPodOperationsTotal,
		m.updatePodOperationsTotal,
		m.deletePodOperationsTotal,
		m.containerRestarts,
		m.podsByPhase,
		m.podsRunning,
		m.podsProvisioningTotal,
		m.podsProvisioningDurationSecs,
		m.nodeStatus,
	)

	return m
}

// GetStatsSummary returns dummy stats for all pods known by this provider.
func (p *Provider) GetStatsSummary(ctx context.Context) (*v1alpha1.Summary, error) {
	var span trace.Span
	ctx, span = trace.StartSpan(ctx, "GetStatsSummary") //nolint: ineffassign,staticcheck
	defer span.End()

	// Grab the current timestamp so we can report it as the time the stats were generated.
	time := v1.NewTime(time.Now())

	// Create the Summary object that will later be populated with node and pod stats.
	res := &v1alpha1.Summary{}

	// Populate the Summary object with basic node stats.
	res.Node = v1alpha1.NodeStats{
		NodeName:  p.nodeName,
		StartTime: v1.NewTime(p.startTime),
	}

	// Populate the Summary object with dummy stats for each pod known by this provider.
	for _, vp := range p.virtualPods {
		pod := vp.Pod()
		if pod == nil {
			continue
		}
		var (
			// totalUsageNanoCores will be populated with the sum of the values of UsageNanoCores computes across all containers in the pod.
			totalUsageNanoCores uint64
			// totalUsageBytes will be populated with the sum of the values of UsageBytes computed across all containers in the pod.
			totalUsageBytes uint64
		)

		// Create a PodStats object to populate with pod stats.
		pss := v1alpha1.PodStats{
			PodRef: v1alpha1.PodReference{
				Name:      pod.Name,
				Namespace: pod.Namespace,
				UID:       string(pod.UID),
			},
			StartTime: pod.CreationTimestamp,
		}

		// Single container: compute dummy stats once
		/* #nosec */
		dummyUsageNanoCores := uint64(rand.Uint32())
		totalUsageNanoCores += dummyUsageNanoCores
		/* #nosec */
		dummyUsageBytes := uint64(rand.Uint32())
		totalUsageBytes += dummyUsageBytes
		c := pod.Spec.Containers[0]
		pss.Containers = append(pss.Containers, v1alpha1.ContainerStats{
			Name:      c.Name,
			StartTime: pod.CreationTimestamp,
			CPU: &v1alpha1.CPUStats{
				Time:           time,
				UsageNanoCores: &dummyUsageNanoCores,
			},
			Memory: &v1alpha1.MemoryStats{
				Time:       time,
				UsageBytes: &dummyUsageBytes,
			},
		})

		// Populate the CPU and RAM stats for the pod and append the PodsStats object to the Summary object to be returned.
		pss.CPU = &v1alpha1.CPUStats{
			Time:           time,
			UsageNanoCores: &totalUsageNanoCores,
		}
		pss.Memory = &v1alpha1.MemoryStats{
			Time:       time,
			UsageBytes: &totalUsageBytes,
		}
		res.Pods = append(res.Pods, pss)
	}

	// Return the dummy stats.
	return res, nil
}

func (p *Provider) generateMockMetrics(metricsMap map[string][]*io_prometheus_client.Metric, resourceType string, label []*io_prometheus_client.LabelPair) map[string][]*io_prometheus_client.Metric {
	var (
		cpuMetricSuffix    = "_cpu_usage_seconds_total"
		memoryMetricSuffix = "_memory_working_set_bytes"
		dummyValue         = float64(100)
	)

	if metricsMap == nil {
		metricsMap = map[string][]*io_prometheus_client.Metric{}
	}

	finalCpuMetricName := resourceType + cpuMetricSuffix
	finalMemoryMetricName := resourceType + memoryMetricSuffix

	newCPUMetric := io_prometheus_client.Metric{
		Label: label,
		Counter: &io_prometheus_client.Counter{
			Value: &dummyValue,
		},
	}
	newMemoryMetric := io_prometheus_client.Metric{
		Label: label,
		Gauge: &io_prometheus_client.Gauge{
			Value: &dummyValue,
		},
	}
	// if metric family exists add to metric array
	if cpuMetrics, ok := metricsMap[finalCpuMetricName]; ok {
		metricsMap[finalCpuMetricName] = append(cpuMetrics, &newCPUMetric)
	} else {
		metricsMap[finalCpuMetricName] = []*io_prometheus_client.Metric{&newCPUMetric}
	}
	if memoryMetrics, ok := metricsMap[finalMemoryMetricName]; ok {
		metricsMap[finalMemoryMetricName] = append(memoryMetrics, &newMemoryMetric)
	} else {
		metricsMap[finalMemoryMetricName] = []*io_prometheus_client.Metric{&newMemoryMetric}
	}

	return metricsMap
}

func (p *Provider) getMetricType(metricName string) *io_prometheus_client.MetricType {
	var (
		dtoCounterMetricType = io_prometheus_client.MetricType_COUNTER
		dtoGaugeMetricType   = io_prometheus_client.MetricType_GAUGE
		cpuMetricSuffix      = "_cpu_usage_seconds_total"
		memoryMetricSuffix   = "_memory_working_set_bytes"
	)
	if strings.HasSuffix(metricName, cpuMetricSuffix) {
		return &dtoCounterMetricType
	}
	if strings.HasSuffix(metricName, memoryMetricSuffix) {
		return &dtoGaugeMetricType
	}

	return nil
}

func (p *Provider) GetMetricsResource(ctx context.Context) ([]*io_prometheus_client.MetricFamily, error) {
	var span trace.Span
	ctx, span = trace.StartSpan(ctx, "GetMetricsResource") //nolint: ineffassign,staticcheck
	defer span.End()

	var (
		nodeNameStr      = "NodeName"
		podNameStr       = "PodName"
		containerNameStr = "containerName"
	)
	nodeLabels := []*io_prometheus_client.LabelPair{
		{
			Name:  &nodeNameStr,
			Value: &p.nodeName,
		},
	}

	metricsMap := p.generateMockMetrics(nil, "node", nodeLabels)
	for _, vp := range p.virtualPods {
		pod := vp.Pod()
		if pod == nil {
			continue
		}
		podLabels := []*io_prometheus_client.LabelPair{
			{
				Name:  &nodeNameStr,
				Value: &p.nodeName,
			},
			{
				Name:  &podNameStr,
				Value: &pod.Name,
			},
		}
		metricsMap = p.generateMockMetrics(metricsMap, "pod", podLabels)
		c := pod.Spec.Containers[0]
		containerLabels := []*io_prometheus_client.LabelPair{
			{
				Name:  &nodeNameStr,
				Value: &p.nodeName,
			},
			{
				Name:  &podNameStr,
				Value: &pod.Name,
			},
			{
				Name:  &containerNameStr,
				Value: &c.Name,
			},
		}
		metricsMap = p.generateMockMetrics(metricsMap, "container", containerLabels)
	}

	res := []*io_prometheus_client.MetricFamily{}
	for metricName := range metricsMap {
		tempName := metricName
		tempMetrics := metricsMap[tempName]

		metricFamily := io_prometheus_client.MetricFamily{
			Name:   &tempName,
			Type:   p.getMetricType(tempName),
			Metric: tempMetrics,
		}
		res = append(res, &metricFamily)
	}

	return res, nil
}
