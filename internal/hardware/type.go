package hardware

import (
	"math"

	"github.com/jaypipes/ghw"
	"github.com/shirou/gopsutil/v3/mem"
)

type Specs struct {
	CpuModel    string
	CpuCores    int64
	RamTotal    float64
	RamAvailabl float64
	GPUName     string
	VRAMTotal   float64
	HasOllama   bool
	HasLlamaCPP bool
}

func GetSpecs() Specs {
	var s Specs
	s.DetectCPU()
	s.DetectGPU()
	s.DetectRAM()
	//s.DetectBackends()
	return s
}

// Note i will be using ghw to detect the hardware information you can check the docs for more info
// https://github.com/jaypipes/ghw.git

// I will split each function to make it easier to read , understand and scale in the future
func (s *Specs) DetectCPU() {

	// First i checked if the cpu is detected and if there is an error
	if cpu, err := ghw.CPU(); cpu != nil && err == nil {
		// i will assign the total cores directly
		s.CpuCores = int64(cpu.TotalCores)

		// I wrapped the cpu.Processores in an if statement to prevent panicing
		// because The library typically fills in the Processors slice by reading data from the OS.
		// If something goes wrong while enumerating individual processors, the slice could be left empty—even if TotalCores
		// was derived from a different lower‑level API that still worked.
		// for more info check docs (1- explenation)
		if len(cpu.Processors) > 0 {
			s.CpuModel = cpu.Processors[0].Model
		} else {
			s.CpuModel = "Unknown"
		}
	}
}

// Note while ghw.Memory() is a good way to detect the ram but it is not the best way to detect the ram usage
// because it does not detect the ram usage in real time
// instead i will use the gopsutil package to detect the ram usage in real time

func (s *Specs) DetectRAM() {

	v, err := mem.VirtualMemory()
	if err != nil {
		return
	}
	// Total physical RAM in GB
	s.RamTotal = math.Round((float64(v.Total)/(1024*1024*1024))*10) / 10
	// Currently available memory (free + cache/buffers that can be reclaimed)
	s.RamAvailabl = math.Round((float64(v.Available)/(1024*1024*1024))*10) / 10
}

func (s *Specs) DetectGPU() {
	if gpu, err := ghw.GPU(); err == nil && gpu != nil {
		if len(gpu.GraphicsCards) > 0 {
			// Grabs the primary GPU
			card := gpu.GraphicsCards[0]
			if card.DeviceInfo != nil {
				s.GPUName = card.DeviceInfo.Product.Name
			}

			// Note: VRAM reporting via ghw can be inconsistent depending on
			// the proprietary drivers installed on the host machine.
		}
	} else {
		s.GPUName = "None/Unsupported"
	}
}
