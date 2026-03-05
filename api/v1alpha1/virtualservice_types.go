package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// VirtualServiceSpec defines the desired state of VirtualService
type VirtualServiceSpec struct {
	// Gateway identifies which gateway pod(s) should handle this VirtualService
	Gateway GatewaySelector `json:"gateway"`

	// Service defines the Service-like specification for virtual pods
	Service ServiceSpec `json:"service"`
}

// GatewaySelector identifies the gateway pod(s) that will handle this VirtualService
type GatewaySelector struct {
	// Selector is a label selector for identifying the gateway pod(s)
	// In practice, should select exactly one gateway pod
	// +kubebuilder:validation:Required
	Selector map[string]string `json:"selector"`
}

// ServiceSpec defines a restricted Service-like specification
type ServiceSpec struct {
	// Type determines how the Service is exposed. Defaults to ClusterIP.
	// Valid options are ClusterIP and NodePort.
	// +optional
	// +kubebuilder:validation:Enum=ClusterIP;NodePort;""
	// +kubebuilder:default=ClusterIP
	Type string `json:"type,omitempty"`

	// Selector is a label selector for virtual pods
	// +kubebuilder:validation:Required
	Selector map[string]string `json:"selector"`

	// Ports is the list of ports exposed by this service
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Ports []ServicePort `json:"ports"`
}

// ServicePort defines a port exposed by the VirtualService
type ServicePort struct {
	// Name is an optional name for this port
	// +optional
	Name string `json:"name,omitempty"`

	// Port is the service port number
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`

	// TargetPort is the port number on the virtual pod (must be an int)
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	TargetPort int32 `json:"targetPort"`

	// NodePort is the port on each node on which this service is exposed when type is NodePort.
	// Usually assigned by the system. If specified, it must be in the node port range (typically 30000-32767).
	// If unspecified, a port will be allocated automatically.
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=65535
	NodePort int32 `json:"nodePort,omitempty"`

	// Protocol is the IP protocol for this port. Must be TCP.
	// Defaults to TCP.
	// +optional
	// +kubebuilder:validation:Enum=TCP;""
	// +kubebuilder:default=TCP
	Protocol string `json:"protocol,omitempty"`
}

// VirtualServiceStatus defines the observed state of VirtualService
type VirtualServiceStatus struct {
	// AllocatedPorts maps each service port to its allocated gateway port
	// +optional
	AllocatedPorts []AllocatedPort `json:"allocatedPorts,omitempty"`

	// Conditions represents the latest available observations of the VirtualService's state
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration reflects the generation of the most recently observed VirtualService
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// AllocatedPort represents a mapping between service port and allocated gateway port
type AllocatedPort struct {
	// Name is the optional name of the port
	// +optional
	Name string `json:"name,omitempty"`

	// ServicePort is the port number from the service spec
	ServicePort int32 `json:"servicePort"`

	// TargetPort is the target port on the virtual pod
	TargetPort int32 `json:"targetPort"`

	// GatewayPort is the allocated port on the gateway
	GatewayPort int32 `json:"gatewayPort"`

	// Protocol is the IP protocol
	// +optional
	Protocol string `json:"protocol,omitempty"`
}

// Condition types for VirtualService
const (
	// ConditionTypeReady indicates whether the VirtualService is ready
	ConditionTypeReady = "Ready"
)

// Condition reasons
const (
	ReasonReconciled        = "Reconciled"
	ReasonReconcileError    = "ReconcileError"
	ReasonUnsupportedSpec   = "UnsupportedSpec"
	ReasonServiceConflict   = "ServiceConflict"
	ReasonGatewayNotFound   = "GatewayNotFound"
	ReasonPortAllocationErr = "PortAllocationError"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=vsvc
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Reason",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].reason"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// VirtualService is the Schema for the virtualservices API
type VirtualService struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VirtualServiceSpec   `json:"spec,omitempty"`
	Status VirtualServiceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// VirtualServiceList contains a list of VirtualService
type VirtualServiceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VirtualService `json:"items"`
}

func init() {
	SchemeBuilder.Register(&VirtualService{}, &VirtualServiceList{})
}
