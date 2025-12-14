package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"gopkg.in/yaml.v3"
)

func findOwnerRef(ctx context.Context, client *kubernetes.Clientset, ns, kind, name string) (metav1.OwnerReference, error) {
	switch kind {
	case "Deployment":
		d, err := client.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return metav1.OwnerReference{}, err
		}
		return metav1.OwnerReference{
			APIVersion: appsv1.SchemeGroupVersion.String(),
			Kind:       "Deployment",
			Name:       d.Name,
			UID:        d.UID,
			// Controller false is fine here; GC doesn't care.
		}, nil
	default:
		return metav1.OwnerReference{}, fmt.Errorf("unsupported OWNER_KIND=%s", kind)
	}
}

func upsertConfigMap(
	ctx context.Context,
	client *kubernetes.Clientset,
	ns, name string,
	ownerRef metav1.OwnerReference,
	content string,
) error {
	cmClient := client.CoreV1().ConfigMaps(ns)

	existing, err := cmClient.Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		// patch / update existing
		existing.OwnerReferences = []metav1.OwnerReference{ownerRef}
		if existing.Data == nil {
			existing.Data = map[string]string{}
		}
		existing.Data["config.yaml"] = content

		_, err = cmClient.Update(ctx, existing, metav1.UpdateOptions{})
		return err
	}

	// if not found, create new
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       ns,
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Data: map[string]string{
			"config.yaml": content,
		},
	}
	_, err = cmClient.Create(ctx, cm, metav1.CreateOptions{})
	return err
}

type PeerConfig struct {
	Address           string `yaml:"address"`
	PrivateKey        string `yaml:"private_key"`
	PublicKey         string `yaml:"public_key"`
	GatewayPortOffset int    `yaml:"gateway_port_offset"`
}

type ServerConfig struct {
	PrivateKey string `yaml:"private_key"`
	PublicKey  string `yaml:"public_key"`
	Endpoint   string `yaml:"endpoint"`
	Port       int    `yaml:"port"`
}

type FullConfig struct {
	Server ServerConfig `yaml:"server"`
	Peers  []PeerConfig `yaml:"peers"`
}

func generateWireguardConfig(endpoint string, port int, peerCount int) (string, error) {
	// --- Generate server keypair ---
	serverPriv, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		return "", fmt.Errorf("generate server private key: %w", err)
	}
	serverPub := serverPriv.PublicKey()

	cfg := FullConfig{
		Server: ServerConfig{
			PrivateKey: serverPriv.String(),
			PublicKey:  serverPub.String(),
			Endpoint:   endpoint,
			Port:       port,
		},
		Peers: make([]PeerConfig, 0, peerCount),
	}

	// --- Generate peers ---
	offset := 10000
	baseIP := 11 // starting from 10.254.254.11

	for i := 0; i < peerCount; i++ {
		priv, err := wgtypes.GeneratePrivateKey()
		if err != nil {
			return "", fmt.Errorf("generate peer private key: %w", err)
		}
		pub := priv.PublicKey()

		peer := PeerConfig{
			Address:           fmt.Sprintf("10.254.254.%d/32", baseIP+i),
			PrivateKey:        priv.String(),
			PublicKey:         pub.String(),
			GatewayPortOffset: offset + (i * 100),
		}

		cfg.Peers = append(cfg.Peers, peer)
	}

	// --- Marshal to YAML ---
	out, err := yaml.Marshal(&cfg)
	if err != nil {
		return "", fmt.Errorf("marshal yaml: %w", err)
	}

	return string(out), nil
}

func mustEnv(name string) string {
	v := os.Getenv(name)
	if v == "" {
		log.Fatalf("env %s must be set", name)
	}
	return v
}

func getKubeConfig() (*rest.Config, error) {
	// Try in-cluster config first
	cfg, err := rest.InClusterConfig()
	if err == nil {
		return cfg, nil
	}

	// Otherwise fall back to local kubeconfig (~/.kube/config)
	kubeconfigPath := filepath.Join(homedir.HomeDir(), ".kube", "config")
	return clientcmd.BuildConfigFromFlags("", kubeconfigPath)
}

func getLoadBalancerIP(ctx context.Context, client *kubernetes.Clientset, ns, svcName string) (string, error) {
	svc, err := client.CoreV1().Services(ns).Get(ctx, svcName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get service: %w", err)
	}

	if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return "", fmt.Errorf("service %s is not of type LoadBalancer", svcName)
	}

	if len(svc.Status.LoadBalancer.Ingress) > 0 {
		if svc.Status.LoadBalancer.Ingress[0].IP != "" {
			return svc.Status.LoadBalancer.Ingress[0].IP, nil
		}
		if svc.Status.LoadBalancer.Ingress[0].Hostname != "" {
			return svc.Status.LoadBalancer.Ingress[0].Hostname, nil
		}
	}

	return "", fmt.Errorf("no external IP/hostname found for service %s", svcName)
}

func waitForDNS(gatewayEndpoint string, attempts int) {
	for i := 1; i <= attempts; i++ {
		_, err := net.LookupHost(gatewayEndpoint)
		if err == nil {
			return
		}

		log.Printf("[%d/%d] DNS record not ready for %s: %v\n", i, attempts, gatewayEndpoint, err)
		time.Sleep(10 * time.Second)
	}

	log.Fatalf("Load Balancer ExternalIP unreachable")
}

func main() {
	ctx := context.Background()

	ns := mustEnv("POD_NAMESPACE")
	ownerKind := mustEnv("OWNER_KIND")  // e.g. "Deployment"
	ownerName := mustEnv("OWNER_NAME")  // e.g. "myapp"
	cmName := mustEnv("CONFIGMAP_NAME") // e.g. "myapp-generated-config"
	gatewayEndpoint := os.Getenv("GATEWAY_ENDPOINT")
	gatewaySvcName := os.Getenv("GATEWAY_SERVICE_NAME")

	gatewayPort := 51820
	if portStr := os.Getenv("GATEWAY_PORT"); portStr != "" {
		if p, err := fmt.Sscanf(portStr, "%d", &gatewayPort); err != nil || p != 1 {
			log.Fatalf("invalid GATEWAY_PORT: %s", portStr)
		}
	}

	peerCount := 128
	//if peerStr := os.Getenv("PEER_COUNT"); peerStr != "" {
	//	if p, err := fmt.Sscanf(peerStr, "%d", &peerCount); err != nil || p != 1 {
	//		log.Fatalf("invalid PEER_COUNT: %s", peerStr)
	//	}
	//}

	cfg, err := getKubeConfig()
	if err != nil {
		log.Fatalf("failed to locate servicetoken or load kubeconfig: %v", err)
	}

	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("client: %v", err)
	}

	// If gatewayEndpoint is not provided, try to get it from the LoadBalancer service
	if gatewayEndpoint == "" && gatewaySvcName != "" {
		log.Printf("GATEWAY_ENDPOINT not set, fetching from service %s...", gatewaySvcName)
		gatewayEndpoint, err = getLoadBalancerIP(ctx, client, ns, gatewaySvcName)
		if err != nil {
			log.Printf("Warning: could not get LoadBalancer IP: %v", err)
			log.Printf("Using empty gatewayEndpoint - you may need to update the ConfigMap manually")
		} else {
			log.Printf("Using LoadBalancer endpoint: %s", gatewayEndpoint)
		}
	}

	waitForDNS(gatewayEndpoint, 90)

	// Generate your config content here:
	configContent, err := generateWireguardConfig(
		gatewayEndpoint,
		gatewayPort,
		peerCount,
	)
	if err != nil {
		panic(err)
	}

	fmt.Println(configContent)

	ownerRef, err := findOwnerRef(ctx, client, ns, ownerKind, ownerName)
	if err != nil {
		log.Fatalf("find owner: %v", err)
	}

	if err := upsertConfigMap(ctx, client, ns, cmName, ownerRef, configContent); err != nil {
		log.Fatalf("upsert configmap: %v", err)
	}

	log.Printf("configmap %s updated", cmName)
}
