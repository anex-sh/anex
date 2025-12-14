package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

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

func main() {
	ctx := context.Background()

	// Generate your config content here:
	configContent, err := generateWireguardConfig(
		"myaddress",
		51820,
		3,
	)
	if err != nil {
		panic(err)
	}

	fmt.Println(configContent)

	//ns := mustEnv("POD_NAMESPACE")
	//ownerKind := mustEnv("OWNER_KIND")  // e.g. "Deployment"
	//ownerName := mustEnv("OWNER_NAME")  // e.g. "myapp"
	//cmName := mustEnv("CONFIGMAP_NAME") // e.g. "myapp-generated-config"

	ns := "skarupa-exp"
	ownerKind := "Deployment"
	ownerName := "test"
	cmName := "myapp-generated-config"

	cfg, err := getKubeConfig()
	if err != nil {
		log.Fatalf("failed to locate servicetoken or load kubeconfig: %v", err)
	}

	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("client: %v", err)
	}

	ownerRef, err := findOwnerRef(ctx, client, ns, ownerKind, ownerName)
	if err != nil {
		log.Fatalf("find owner: %v", err)
	}

	if err := upsertConfigMap(ctx, client, ns, cmName, ownerRef, configContent); err != nil {
		log.Fatalf("upsert configmap: %v", err)
	}

	log.Printf("configmap %s updated", cmName)
}
