//go:build linux

package hardware

import (
	"os"
	"path/filepath"
	"testing"
)

// createFakeSysfsDevice creates a temporary directory structure that mimics
// /sys/class/drm/card0/device/ with vendor, device, and VRAM files.
func createFakeSysfsDevice(t *testing.T, vendorHex, deviceHex, vramBytes string) (string, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "ciri-sysfs-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}

	deviceDir := filepath.Join(tmpDir, "drm", "card0", "device")
	if err := os.MkdirAll(deviceDir, 0o755); err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("MkdirAll: %v", err)
	}

	if err := os.WriteFile(filepath.Join(deviceDir, "vendor"), []byte("0x"+vendorHex+"\n"), 0o644); err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("WriteFile vendor: %v", err)
	}
	if err := os.WriteFile(filepath.Join(deviceDir, "device"), []byte("0x"+deviceHex+"\n"), 0o644); err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("WriteFile device: %v", err)
	}
	if vramBytes != "" {
		if err := os.WriteFile(filepath.Join(deviceDir, "mem_info_vram_total"), []byte(vramBytes+"\n"), 0o644); err != nil {
			os.RemoveAll(tmpDir)
			t.Fatalf("WriteFile vram: %v", err)
		}
	}

	return deviceDir, func() { os.RemoveAll(tmpDir) }
}

func TestReadHexFile_Normal(t *testing.T) {
	tmpDir, cleanup := createFakeSysfsDevice(t, "10de", "2684", "")
	defer cleanup()

	vendor := readHexFile(filepath.Join(tmpDir, "vendor"))
	if vendor != "10de" {
		t.Errorf("expected vendor 10de, got %q", vendor)
	}

	device := readHexFile(filepath.Join(tmpDir, "device"))
	if device != "2684" {
		t.Errorf("expected device 2684, got %q", device)
	}
}

func TestReadHexFile_NoPrefix(t *testing.T) {
	tmpDir, cleanup := createFakeSysfsDevice(t, "1002", "744c", "")
	defer cleanup()

	os.WriteFile(filepath.Join(tmpDir, "vendor"), []byte("1002\n"), 0o644)

	vendor := readHexFile(filepath.Join(tmpDir, "vendor"))
	if vendor != "1002" {
		t.Errorf("expected vendor 1002 without 0x, got %q", vendor)
	}
}

func TestReadHexFile_Whitespace(t *testing.T) {
	tmpDir, cleanup := createFakeSysfsDevice(t, "8086", "e20b", "")
	defer cleanup()

	os.WriteFile(filepath.Join(tmpDir, "device"), []byte("  0xe20b  \n"), 0o644)

	device := readHexFile(filepath.Join(tmpDir, "device"))
	if device != "e20b" {
		t.Errorf("expected device e20b with whitespace, got %q", device)
	}
}

func TestReadHexFile_MissingFile(t *testing.T) {
	result := readHexFile("/nonexistent/sysfs/path/vendor")
	if result != "" {
		t.Errorf("expected empty string for missing file, got %q", result)
	}
}

func TestDetectVRAM_SysfsFallback(t *testing.T) {
	sysfsDir, cleanup := createFakeSysfsDevice(t, "1002", "744c", "17179869184") // 16 GiB
	defer cleanup()

	target := &PCIInfo{
		VendorID: "1002",
		DeviceID: "744c",
		SysfsDir: sysfsDir,
	}

	vram := detectVRAM(nil, target)
	if vram <= 0 {
		t.Errorf("expected positive VRAM from sysfs, got %f", vram)
	}
}
