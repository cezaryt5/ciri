package hardware

// --- Types & Interfaces ---
/*
type Specs struct {
	CpuModel     string
	CpuCores     int64
	RamTotal     float64
	RamAvailable float64

	RawGPUName string
	GPUID      int64
	VRAMTotal  float64

	HasOllama   bool
	HasLlamaCPP bool
}

type GPU struct {
	ID            int64
	Name          string
	CanonicalName string
	VRAMGB        float64
	Bandwidth     float64
	TFLOPS        float64
	IsLaptop      bool
	Architecture  string
}

type DetectionStatus int

const (
	GPUExact DetectionStatus = iota
	GPUUnverified
	GPUNotFound
)

type DetectionResult struct {
	Specs  Specs
	GPU    *GPU
	Status DetectionStatus
}

type GPUMatcher interface {
	Detect(ctx context.Context) (*GPU, float64, error)
}

type PCIInfo struct {
	VendorID          string
	DeviceID          string
	SubsystemVendorID string
	SubsystemDeviceID string
}

// --- Global Normalization Variables ---

var (
	vendorPrefixes = []string{
		"nvidia corporation", "nvidia", "advanced micro devices", "amd",
		"intel corporation", "intel", "apple",
	}

	brandingReplacer = strings.NewReplacer(
		"geforce", "", "radeon (tm)", "radeon", "radeon(tm)", "radeon",
		"graphics", "", "laptop gpu", "laptop", "gpu", "",
		"mobile", "laptop", "with max-q design", "laptop", "max-q", "laptop",
	)

	driverVersionRe = regexp.MustCompile(`\b\d+\.\d+\.\d+\b`)
	multiSpaceRe    = regexp.MustCompile(`\s+`)
	specialCharRe   = regexp.MustCompile(`[^\w\s-]`)
)

// --- Main Entry ---

func GetSpecs(ctx context.Context, q *db.Queries) DetectionResult {
	var res DetectionResult
	res.Specs.DetectCPU()
	res.Specs.DetectRAM()

	status, bestGuess := res.Specs.DetectGPU(ctx, q)
	res.Status = status
	res.GPU = bestGuess

	return res
}

// --- Hardware Detection ---

func (s *Specs) DetectCPU() {
	if cpu, err := ghw.CPU(); cpu != nil && err == nil {
		s.CpuCores = int64(cpu.TotalCores)
		if len(cpu.Processors) > 0 {
			s.CpuModel = cpu.Processors[0].Model
		} else {
			s.CpuModel = "Unknown"
		}
	}
}

func (s *Specs) DetectRAM() {
	v, err := mem.VirtualMemory()
	if err != nil {
		return
	}
	s.RamTotal = math.Round((float64(v.Total)/(1024*1024*1024))*10) / 10
	s.RamAvailable = math.Round((float64(v.Available)/(1024*1024*1024))*10) / 10
}

func (s *Specs) DetectGPU(ctx context.Context, q *db.Queries) (DetectionStatus, *GPU) {
	strategies := []GPUMatcher{
		&PCIMatcher{q: q},
		&VendorAPIMatcher{q: q},
		&GHWFuzzyMatcher{q: q},
	}

	var highestConfidence float64
	var bestGuess *GPU

	for _, strategy := range strategies {
		gpu, confidence, err := strategy.Detect(ctx)
		if err != nil || gpu == nil {
			continue
		}

		if confidence >= 0.95 {
			s.GPUID = gpu.ID
			s.RawGPUName = gpu.Name
			s.VRAMTotal = gpu.VRAMGB
			return GPUExact, gpu
		}

		if confidence > highestConfidence {
			bestGuess = gpu
			highestConfidence = confidence
		}
	}

	if bestGuess != nil {
		s.RawGPUName = "Unverified: " + bestGuess.Name
		return GPUUnverified, bestGuess
	}

	s.RawGPUName = "Unknown GPU"
	return GPUNotFound, nil
}

// --- String Normalization ---

func NormalizeGPUName(raw string) string {
	name := strings.ToLower(strings.TrimSpace(raw))
	name = driverVersionRe.ReplaceAllString(name, "")

	for _, prefix := range vendorPrefixes {
		if strings.HasPrefix(name, prefix) {
			name = strings.TrimPrefix(name, prefix)
			name = strings.TrimSpace(name)
		}
	}

	name = brandingReplacer.Replace(name)
	name = specialCharRe.ReplaceAllString(name, "")
	name = multiSpaceRe.ReplaceAllString(name, " ")

	return strings.TrimSpace(name)
}

// --- DB Mapping Helpers ---

func gpuFromDB(g db.Gpu) *GPU {
	return &GPU{
		ID:            g.ID,
		Name:          g.Name,
		CanonicalName: nullStr(g.CanonicalName),
		VRAMGB:        nullFloat(g.VramGb),
		Bandwidth:     nullFloat(g.MemoryBandwidthGbps),
		TFLOPS:        nullFloat(g.Fp16Tflops),
		IsLaptop:      strings.Contains(strings.ToLower(g.Name), "laptop"),
		Architecture:  nullStr(g.Architecture),
	}
}

func nullStr(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

func nullFloat(nf sql.NullFloat64) float64 {
	if nf.Valid {
		return nf.Float64
	}
	return 0
}

func toNullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}

// --- Command Execution Helper ---

func execWithTimeout(ctx context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return exec.CommandContext(cmdCtx, name, args...).Output()
}

// --- Matcher 1: PCI ID Detection ---

type PCIMatcher struct {
	q *db.Queries
}

func (m *PCIMatcher) Detect(ctx context.Context) (*GPU, float64, error) {
	var pci *PCIInfo

	// Replace runtime.GOOS with build tags in production. Retained here for single-file completion.
	// switch runtime.GOOS { ... }

	pci = detectPCILinux(ctx) // Calling Linux by default for structural example; implement build tags later.

	if pci == nil {
		return nil, 0, nil
	}

	gpu, err := m.q.GetGPUByPCIID(ctx, db.GetGPUByPCIIDParams{
		VendorID:          pci.VendorID,
		DeviceID:          pci.DeviceID,
		SubsystemVendorID: toNullString(pci.SubsystemVendorID),
		SubsystemDeviceID: toNullString(pci.SubsystemDeviceID),
	})
	if err != nil {
		return nil, 0, nil
	}

	return gpuFromDB(gpu), 0.98, nil
}

// --- OS-Specific PCI Parsers ---

func detectPCILinux(ctx context.Context) *PCIInfo {
	out, err := execWithTimeout(ctx, 3*time.Second, "lspci", "-nn")
	if err == nil {
		re := regexp.MustCompile(`\[([0-9a-f]{4}):([0-9a-f]{4})\]`)
		var intelPCI, amdPCI, nvidiaPCI *PCIInfo

		lines := strings.Split(string(out), "\n")
		for _, line := range lines {
			if !strings.Contains(line, "VGA") && !strings.Contains(line, "3D") {
				continue
			}
			matches := re.FindStringSubmatch(line)
			if len(matches) != 3 {
				continue
			}

			pci := &PCIInfo{VendorID: matches[1], DeviceID: matches[2]}
			lineLower := strings.ToLower(line)

			switch {
			case strings.Contains(lineLower, "nvidia"):
				nvidiaPCI = pci
			case strings.Contains(lineLower, "amd") || strings.Contains(lineLower, "ati"):
				amdPCI = pci
			case strings.Contains(lineLower, "intel"):
				intelPCI = pci
			}
		}

		// Enforce dGPU priority
		if nvidiaPCI != nil {
			return nvidiaPCI
		}
		if amdPCI != nil {
			return amdPCI
		}
		if intelPCI != nil {
			return intelPCI
		}
	}

	// Fallback to sysfs
	entries, err := filepath.Glob("/sys/class/drm/card/device/vendor")
	if err != nil || len(entries) == 0 {
		return nil
	}

	vendorPath := entries[0]
	deviceDir := filepath.Dir(vendorPath)

	readHexFile := func(path string) string {
		raw, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		return strings.TrimPrefix(strings.TrimSpace(string(raw)), "0x")
	}

	return &PCIInfo{
		VendorID:          readHexFile(filepath.Join(deviceDir, "vendor")),
		DeviceID:          readHexFile(filepath.Join(deviceDir, "device")),
		SubsystemVendorID: readHexFile(filepath.Join(deviceDir, "subsystem_vendor")),
		SubsystemDeviceID: readHexFile(filepath.Join(deviceDir, "subsystem_device")),
	}
}

func detectPCIWindows(ctx context.Context) *PCIInfo {
	// Try PowerShell first (Modern Windows)
	out, err := execWithTimeout(ctx, 5*time.Second, "powershell", "-Command", `Get-PnpDevice -Class Display | ForEach-Object { $_.InstanceId }`)
	if err == nil {
		re := regexp.MustCompile(`VEN_([0-9A-Fa-f]{4})&DEV_([0-9A-Fa-f]{4})`)
		lines := strings.Split(string(out), "\n")
		for _, line := range lines {
			matches := re.FindStringSubmatch(line)
			if len(matches) == 3 {
				return &PCIInfo{
					VendorID: strings.ToLower(matches[1]),
					DeviceID: strings.ToLower(matches[2]),
				}
			}
		}
	}

	// Fallback to wmic (Deprecated, but works on older builds)
	out, err = execWithTimeout(ctx, 3*time.Second, "wmic", "path", "win32_VideoController", "get", "PNPDeviceID")
	if err == nil {
		lines := strings.Split(string(out), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "PCI\\") {
				re := regexp.MustCompile(`VEN_([0-9A-Fa-f]{4})&DEV_([0-9A-Fa-f]{4})`)
				matches := re.FindStringSubmatch(line)
				if len(matches) == 3 {
					return &PCIInfo{VendorID: strings.ToLower(matches[1]), DeviceID: strings.ToLower(matches[2])}
				}
			}
		}
	}
	return nil
}

func detectPCIDarwin(ctx context.Context) *PCIInfo {
	return nil
}

// --- Matcher 2: Vendor API Detection ---

type VendorAPIMatcher struct {
	q *db.Queries
}

func (m *VendorAPIMatcher) Detect(ctx context.Context) (*GPU, float64, error) {
	name := detectVendorLinux(ctx) // Again, use build tags.

	if name == "" {
		return nil, 0, nil
	}
	return m.resolveByName(ctx, name, 0.95)
}

func (m *VendorAPIMatcher) resolveByName(ctx context.Context, name string, baseConf float64) (*GPU, float64, error) {
	if gpu, err := m.q.GetGPUByName(ctx, name); err == nil {
		return gpuFromDB(gpu), baseConf, nil
	}
	if gpu, err := m.q.GetGPUByAlias(ctx, name); err == nil {
		return gpuFromDB(gpu), baseConf - 0.03, nil
	}
	normalized := NormalizeGPUName(name)
	if gpu, err := m.q.GetGPUByCanonicalName(ctx, toNullString(normalized)); err == nil {
		return gpuFromDB(gpu), baseConf - 0.05, nil
	}
	return nil, 0, nil
}

func detectVendorLinux(ctx context.Context) string {
	if out, err := execWithTimeout(ctx, 3*time.Second, "nvidia-smi", "--query-gpu=name", "--format=csv,noheader"); err == nil {
		return strings.TrimSpace(string(out))
	}
	if out, err := execWithTimeout(ctx, 3*time.Second, "rocm-smi", "--showproductname"); err == nil {
		lines := strings.Split(string(out), "\n")
		for _, line := range lines {
			if strings.Contains(line, "Card Series:") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					return strings.TrimSpace(parts[1])
				}
			}
		}
	}
	return ""
}

func detectVendorWindows(ctx context.Context) string {
	if out, err := execWithTimeout(ctx, 3*time.Second, "nvidia-smi", "--query-gpu=name", "--format=csv,noheader"); err == nil {
		return strings.TrimSpace(string(out))
	}
	if out, err := execWithTimeout(ctx, 3*time.Second, "wmic", "path", "win32_VideoController", "get", "name"); err == nil {
		lines := strings.Split(string(out), "\n")
		if len(lines) > 1 && strings.TrimSpace(lines[1]) != "" {
			return strings.TrimSpace(lines[1])
		}
	}
	return ""
}

func detectVendorDarwin(ctx context.Context) string {
	out, err := execWithTimeout(ctx, 3*time.Second, "system_profiler", "SPDisplaysDataType")
	if err != nil {
		return ""
	}
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		if strings.Contains(line, "Chipset Model:") {
			return strings.TrimSpace(strings.Split(line, ":")[1])
		}
	}
	return ""
}

// --- Matcher 3: GHW Fuzzy Detection ---

type GHWFuzzyMatcher struct {
	q *db.Queries
}

func (m *GHWFuzzyMatcher) Detect(ctx context.Context) (*GPU, float64, error) {
	rawName := detectRawGPUName()
	if rawName == "" || rawName == "None/Unsupported" || rawName == "Unknown" {
		return nil, 0, nil
	}

	if gpu, err := m.q.GetGPUByName(ctx, rawName); err == nil {
		return gpuFromDB(gpu), 0.90, nil
	}
	if gpu, err := m.q.GetGPUByAlias(ctx, rawName); err == nil {
		return gpuFromDB(gpu), 0.85, nil
	}

	normalized := NormalizeGPUName(rawName)
	if gpu, err := m.q.GetGPUByCanonicalName(ctx, toNullString(normalized)); err == nil {
		return gpuFromDB(gpu), 0.80, nil
	}

	candidates, err := m.q.SearchGPUsByNormalizedName(ctx, rawName)
	if err != nil || len(candidates) == 0 {
		return nil, 0, nil
	}
	if len(candidates) == 1 {
		return gpuFromDB(candidates[0]), 0.70, nil
	}

	return gpuFromDB(candidates[0]), 0.50, nil
}

func detectRawGPUName() string {
	gpuInfo, err := ghw.GPU()
	if err != nil || gpuInfo == nil || len(gpuInfo.GraphicsCards) == 0 {
		return "None/Unsupported"
	}

	// Filter out iGPUs if possible
	for _, card := range gpuInfo.GraphicsCards {
		if card == nil || card.DeviceInfo == nil || card.DeviceInfo.Product == nil {
			continue
		}
		name := strings.ToLower(card.DeviceInfo.Product.Name)

		if strings.Contains(name, "intel hd") || strings.Contains(name, "intel uhd") ||
			strings.Contains(name, "intel iris") ||
			(strings.Contains(name, "radeon") && !strings.Contains(name, "rx") && !strings.Contains(name, "pro") && !strings.Contains(name, "vii")) {
			continue
		}

		if strings.Contains(name, "rtx") || strings.Contains(name, "gtx") ||
			strings.Contains(name, "radeon") || strings.Contains(name, "apple") ||
			strings.Contains(name, "quadro") || strings.Contains(name, "tesla") {
			return card.DeviceInfo.Product.Name
		}
	}

	// Fallback to whatever ghw found first if all filters failed
	return gpuInfo.GraphicsCards[0].DeviceInfo.Product.Name
}
*/
