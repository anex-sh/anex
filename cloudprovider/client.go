package cloudprovider

import (
	"context"

	"gitlab.devklarka.cz/ai/gpu-provider/virtualpod"
	v1 "k8s.io/api/core/v1"
)

type Client interface {
	GetRentalCandidates(ctx context.Context, specs virtualpod.MachineSpecification) ([]virtualpod.Offer, error)
	ListMachines(ctx context.Context) ([]*virtualpod.Machine, error)
	GetMachine(ctx context.Context, machineID string) (machine *virtualpod.Machine, err error)
	ProvisionMachine(ctx context.Context, candidatesID []string, pod *v1.Pod, authToken string, proxy, promtail bool) (machineID string, err error)
	DestroyMachine(ctx context.Context, id string) error
	RenewMachineKeys(ctx context.Context, machineID string) error
	MapRunningMachines(ctx context.Context, pods *v1.PodList) (map[string]*virtualpod.Machine, error)
	PruneDanglingMachines(ctx context.Context, podUIDs []string) error
	RestartMachine(ctx context.Context, id string, pullImage bool) error
	CopyFileToMachine(ctx context.Context, id string, srcPath, dstPath string) error
}
