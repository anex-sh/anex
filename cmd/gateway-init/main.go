package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"gopkg.in/yaml.v3"
)

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
	configPath := mustEnv("GATEWAY_CONFIG_PATH")
	if _, err := os.Stat(configPath); err == nil {
		log.Printf("Config file already exists at %s", configPath)
		os.Exit(0)
	}

	ns := os.Getenv("POD_NAMESPACE")
	gatewayEndpoint := os.Getenv("GATEWAY_ENDPOINT")
	gatewaySvcName := os.Getenv("GATEWAY_SERVICE_NAME")

	portStr := mustEnv("GATEWAY_PORT")
	gatewayPort, _ := strconv.Atoi(portStr)

	// If gatewayEndpoint is not provided, try to get it from the LoadBalancer service
	if gatewayEndpoint == "" && gatewaySvcName != "" {
		cfg, err := getKubeConfig()
		if err != nil {
			log.Fatalf("failed to locate servicetoken or load kubeconfig: %v", err)
		}

		client, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			log.Fatalf("client: %v", err)
		}

		log.Printf("GATEWAY_ENDPOINT not set, fetching from service %s...", gatewaySvcName)
		gatewayEndpoint, err = getLoadBalancerIP(ctx, client, ns, gatewaySvcName)
		if err != nil {
			log.Fatalf("Warning: could not get LoadBalancer IP: %v", err)
		} else {
			log.Printf("Using LoadBalancer endpoint: %s", gatewayEndpoint)
		}
	}

	waitForDNS(gatewayEndpoint, 90)

	// Generate your config content here:
	peerCount := 3
	configContent, err := generateWireguardConfig(
		gatewayEndpoint,
		gatewayPort,
		peerCount,
	)
	if err != nil {
		log.Fatalf("generate config: %v", err)
	}

	// Create .dirty file in the same directory
	dirtyFile := filepath.Join(filepath.Dir(configPath), ".dirty")
	if err := os.WriteFile(dirtyFile, []byte{}, 0644); err != nil {
		log.Fatalf("write dirty file: %v", err)
	}

	// Write config to file
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		log.Fatalf("write config file: %v", err)
	}

	log.Printf("config written to %s", configPath)
}
