package scripts

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type GPU struct {
	Name                  string
	VRAM_GB               string
	Memory_Type           string
	Memory_Bus            string
	PCIe                  string
	Shading_Units         string
	TMUs                  string
	ROPs                  string
	Release_Date          string
	Architecture          string
	Memory_Bandwidth_GBps string
	FP16_TFLOPS           string
	INT8_TOPS             string
	TDP_Watts             string
}

type AppleSiliconGPU struct {
	Name                  string
	GPU_Cores             string
	Memory_GB             string
	Memory_Type           string
	Memory_Bandwidth_GBps string
	GPU_TFLOPS            string
	Neural_Engine_TOPS    string
	TDP_Watts             string
	Release_Date          string
	Architecture          string
}

func escapeSQL(s string) string {
	s = strings.ReplaceAll(s, "'", "''")
	return s
}

func readCSV(filename string) ([][]string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", filename, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", filename, err)
	}
	return records, nil
}

func generateGPUsSQL(records [][]string) string {
	var sb strings.Builder
	sb.WriteString("DELETE FROM gpus;\n")
	sb.WriteString("INSERT INTO gpus (name, vram_gb, memory_type, memory_bus, pcie, shading_units, tmus, rops, release_date, architecture, memory_bandwidth_gbps, fp16_tflops, int8_tops, tdp_watts) VALUES\n")

	for i, row := range records {
		if i == 0 {
			continue
		}
		name := escapeSQL(row[0])
		vramGB := strings.TrimSpace(row[1])
		memType := escapeSQL(row[2])
		memBus := strings.TrimSpace(row[3])
		pcie := escapeSQL(row[4])
		shadingUnits := strings.TrimSpace(row[5])
		tmus := strings.TrimSpace(row[6])
		rops := strings.TrimSpace(row[7])
		releaseDate := strings.TrimSpace(row[8])
		architecture := escapeSQL(row[9])
		memBW := strings.TrimSpace(row[10])
		fp16 := strings.TrimSpace(row[11])
		int8 := strings.TrimSpace(row[12])
		tdp := strings.TrimSpace(row[13])

		if vramGB == "" {
			vramGB = "0"
		}
		if memBus == "" {
			memBus = "0"
		}
		if shadingUnits == "" {
			shadingUnits = "0"
		}
		if tmus == "" {
			tmus = "0"
		}
		if rops == "" {
			rops = "0"
		}
		if memBW == "" {
			memBW = "0"
		}
		if fp16 == "" {
			fp16 = "0"
		}
		if int8 == "" {
			int8 = "0"
		}
		if tdp == "" {
			tdp = "0"
		}

		vramGBInt, _ := strconv.Atoi(vramGB)
		memBusInt, _ := strconv.Atoi(memBus)
		shadingUnitsInt, _ := strconv.Atoi(shadingUnits)
		tmusInt, _ := strconv.Atoi(tmus)
		ropsInt, _ := strconv.Atoi(rops)
		memBWFloat, _ := strconv.ParseFloat(memBW, 64)
		fp16Float, _ := strconv.ParseFloat(fp16, 64)
		int8Float, _ := strconv.ParseFloat(int8, 64)
		tdpInt, _ := strconv.Atoi(tdp)

		comma := ","
		if i == len(records)-1 {
			comma = ";"
		}

		sb.WriteString(fmt.Sprintf("  ('%s', %d, '%s', %d, '%s', %d, %d, %d, '%s', '%s', %.2f, %.1f, %.1f, %d)%s\n",
			name, vramGBInt, memType, memBusInt, pcie, shadingUnitsInt, tmusInt, ropsInt,
			releaseDate, architecture, memBWFloat, fp16Float, int8Float, tdpInt, comma))
	}

	return sb.String()
}

func generateAppleSiliconSQL(records [][]string) string {
	var sb strings.Builder
	sb.WriteString("DELETE FROM apple_silicon_gpus;\n")
	sb.WriteString("INSERT INTO apple_silicon_gpus (name, gpu_cores, memory_gb, memory_type, memory_bandwidth_gbps, gpu_tflops, neural_engine_tops, tdp_watts, release_date, architecture) VALUES\n")

	for i, row := range records {
		if i == 0 {
			continue
		}
		name := escapeSQL(row[0])
		gpuCores := strings.TrimSpace(row[1])
		memGB := strings.TrimSpace(row[2])
		memType := escapeSQL(row[3])
		memBW := strings.TrimSpace(row[4])
		gpuTF := strings.TrimSpace(row[5])
		neuralTops := strings.TrimSpace(row[6])
		tdp := strings.TrimSpace(row[7])
		releaseDate := strings.TrimSpace(row[8])
		architecture := escapeSQL(row[9])

		if gpuCores == "" {
			gpuCores = "0"
		}
		if memGB == "" {
			memGB = "0"
		}
		if memBW == "" {
			memBW = "0"
		}
		if gpuTF == "" {
			gpuTF = "0"
		}
		if neuralTops == "" {
			neuralTops = "0"
		}
		if tdp == "" {
			tdp = "0"
		}

		gpuCoresInt, _ := strconv.Atoi(gpuCores)
		memGBInt, _ := strconv.Atoi(memGB)
		memBWFloat, _ := strconv.ParseFloat(memBW, 64)
		gpuTFFloat, _ := strconv.ParseFloat(gpuTF, 64)
		neuralTopsFloat, _ := strconv.ParseFloat(neuralTops, 64)
		tdpInt, _ := strconv.Atoi(tdp)

		comma := ","
		if i == len(records)-1 {
			comma = ";"
		}

		sb.WriteString(fmt.Sprintf("  ('%s', %d, %d, '%s', %.2f, %.1f, %.1f, %d, '%s', '%s')%s\n",
			name, gpuCoresInt, memGBInt, memType, memBWFloat, gpuTFFloat, neuralTopsFloat,
			tdpInt, releaseDate, architecture, comma))
	}

	return sb.String()
}

func main() {
	gpuRecords, err := readCSV("GPUs.csv")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading GPUs.csv: %v\n", err)
		os.Exit(1)
	}

	appleRecords, err := readCSV("Apple_Silicon_GPUs.csv")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading Apple_Silicon_GPUs.csv: %v\n", err)
		os.Exit(1)
	}

	gpuSQL := generateGPUsSQL(gpuRecords)
	appleSQL := generateAppleSiliconSQL(appleRecords)

	os.MkdirAll("migrations", 0755)

	if err := os.WriteFile("internal/db/migrations/002_seed_gpus.sql", []byte(gpuSQL), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing seed_gpus.sql: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile("internal/db/migrations/003_seed_apple_silicon.sql", []byte(appleSQL), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing seed_apple_silicon.sql: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Generated migrations/002_seed_gpus.sql (%d rows)\n", len(gpuRecords)-1)
	fmt.Printf("Generated migrations/003_seed_apple_silicon.sql (%d rows)\n", len(appleRecords)-1)
}
