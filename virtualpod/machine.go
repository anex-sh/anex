package virtualpod

type MachineState int

const (
	MachineStatePending MachineState = iota
	MachineStateRunning
	MachineStateFailed
	MachineStateUnknown
)

type Region int

const (
	RegionEurope Region = iota
	RegionNorthAmerica
	RegionSouthAmerica
	RegionAsia
	RegionAfrica
	RegionOceania
	RegionAny
)

type MachineSpecification struct {
	// Bool fields
	VerifiedOnly   bool
	DatacenterOnly bool

	// List fields
	GPUNames   []string
	ComputeCap []string
	Regions    []Region

	// Min/Max/Exact fields
	GPUCount         *int
	GPUCountMin      *int
	GPUCountMax      *int
	VRAM             *int // MB
	VRAMMin          *int
	VRAMMax          *int
	VRAMTotal        *int // MB
	VRAMTotalMin     *int
	VRAMTotalMax     *int
	VRAMBandwidth    *float64 // GB/s
	VRAMBandwidthMin *float64
	VRAMBandwidthMax *float64
	TFLOPS           *float64
	TFLOPSMin        *float64
	TFLOPSMax        *float64
	CUDA             *float64
	CUDAMin          *float64
	CUDAMax          *float64
	CPU              *int // cores
	CPUMin           *int
	CPUMax           *int
	RAM              *int // MB
	RAMMin           *int
	RAMMax           *int
	Price            *float64 // per hour
	PriceMin         *float64
	PriceMax         *float64
	VastAIDLPerf     *float64
	VastAIDLPerfMin  *float64
	VastAIDLPerfMax  *float64
	UploadSpeed      *float64 // Mbps
	UploadSpeedMin   *float64
	UploadSpeedMax   *float64
	DownloadSpeed    *float64 // Mbps
	DownloadSpeedMin *float64
	DownloadSpeedMax *float64
}

type Offer struct {
	OfferID   string
	MachineID string
}

type States struct {
	GpuName            string
	GpuVRAM            float64
	GpuTFLOPS          float64
	GpuMemoryBandwidth float64
	CpuCores           float64
	CpuRam             float64
	PricePerHr         float64
}

type Machine struct {
	ID        string
	MachineID string
	PublicIP  string
	AgentPort int
	States    States
	State     MachineState
}
