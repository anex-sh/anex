package cloudprovider

import (
	"context"

	"gitlab.devklarka.cz/ai/gpu-provider/virtualpod"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
)

type ProxyConfig struct {
	Enabled          bool
	ServerEndpoint   string
	ServerPublicKey  string
	ClientAddress    string
	ClientPrivateKey string
}

type Client interface {
	SelectAndProvisionMachine(ctx context.Context, spec virtualpod.MachineSpecification, pod *v1.Pod, proxy virtualpod.PodProxyConfig, promtail bool, recorder record.EventRecorder) (machineID string, err error)
	SupportsMachineBans() bool
	BanMachine(stableID string)
	ListMachines(ctx context.Context) ([]*virtualpod.Machine, error)
	GetMachine(ctx context.Context, machineID string) (machine *virtualpod.Machine, err error)
	DestroyMachine(ctx context.Context, id string) error
	RenewMachineKeys(ctx context.Context, machineID string, proxy virtualpod.PodProxyConfig) error
	MapRunningMachines(ctx context.Context, pods *v1.PodList) (map[string]*virtualpod.Machine, error)
	PruneDanglingMachines(ctx context.Context, podUIDs []string) error
	RestartMachine(ctx context.Context, id string, pullImage bool) error
	CopyFileToMachine(ctx context.Context, id string, srcPath, dstPath string) error
}
