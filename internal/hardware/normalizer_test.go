package hardware

import (
	"testing"
)

func TestNormalizeGPUName_Basic(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"NVIDIA GeForce RTX 4090", "4090"},
		{"NVIDIA GeForce GTX 1080 Ti", "1080 ti"},
		{"AMD Radeon RX 7900 XTX", "7900 xtx"},
		{"Intel Arc A770", "a770"},
		{"Apple M4 Max (GPU)", "m4 max"},
		{"NVIDIA RTX A6000", "a6000"},
		{"Advanced Micro Devices Radeon RX 6900 XT", "radeon rx 6900 xt"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := NormalizeGPUName(tc.input)
			if got != tc.expected {
				t.Errorf("NormalizeGPUName(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

func TestNormalizeGPUName_Whitespace(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// Trailing/leading whitespace trimmed before prefix matching.
		{"  NVIDIA GeForce RTX 4090  ", "4090"},
		// Internal extra spaces NOT collapsed before prefix check, so
		// "nvidia   geforce   rtx   4090" doesn't match "nvidia geforce rtx ".
		{"NVIDIA   GeForce   RTX   4090", "geforce rtx 4090"},
		{"\tNVIDIA GeForce RTX 4090\n", "4090"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := NormalizeGPUName(tc.input)
			if got != tc.expected {
				t.Errorf("NormalizeGPUName(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

func TestNormalizeGPUName_CaseInsensitive(t *testing.T) {
	input := "nvidia geforce rtx 4090"
	expected := "4090"
	got := NormalizeGPUName(input)
	if got != expected {
		t.Errorf("NormalizeGPUName(%q) = %q, want %q", input, got, expected)
	}
}

func TestNormalizeGPUName_LaptopVariants(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"NVIDIA GeForce RTX 3080 Laptop GPU", "3080 laptop"},
		{"NVIDIA GeForce GTX 1070 Mobile", "1070 laptop"},
		{"NVIDIA GeForce RTX 3070 with Max-Q Design", "3070"},
		{"NVIDIA GeForce RTX 3080 Max-Q", "3080"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := NormalizeGPUName(tc.input)
			if got != tc.expected {
				t.Errorf("NormalizeGPUName(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

func TestNormalizeGPUName_RemovesDriverVersion(t *testing.T) {
	got := NormalizeGPUName("NVIDIA GeForce RTX 4090 545.23.06")
	if got != "4090" {
		t.Errorf("expected version stripped, got %q", got)
	}
}

func TestNormalizeGPUName_RemovesSpecialChars(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// "(TM)" stripped by trademark replacer before the branding replacer.
		{"NVIDIA GeForce RTX 4090 (TM)", "4090"},
		// Registered/trademark symbols block prefix matching, so
		// "nvidia® geforce® rtx™ 4090" → TM replacer removes ®/™ → becomes
		// "nvidia geforce rtx 4090" but prefix stripping already failed.
		{"NVIDIA\u00ae GeForce\u00ae RTX\u2122 4090", "nvidia geforce rtx 4090"},
		// "@" removed by specialCharRe, "." kept (in [\\w\\s-] class).
		{"NVIDIA GeForce RTX 4090 @ 2.5GHz", "4090 25ghz"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := NormalizeGPUName(tc.input)
			if got != tc.expected {
				t.Errorf("NormalizeGPUName(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

func TestNormalizeGPUName_RemovesGraphics(t *testing.T) {
	got := NormalizeGPUName("NVIDIA GeForce RTX 4090 Graphics")
	if got != "4090" {
		t.Errorf("expected 'Graphics' removed, got %q", got)
	}
}

func TestNormalizeGPUName_RemovesGPU(t *testing.T) {
	got := NormalizeGPUName("NVIDIA GeForce RTX 4090 GPU")
	if got != "4090" {
		t.Errorf("expected 'GPU' removed, got %q", got)
	}
}

func TestNormalizeGPUName_Empty(t *testing.T) {
	got := NormalizeGPUName("")
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestNormalizeGPUName_OnlyWhitespace(t *testing.T) {
	got := NormalizeGPUName("   \t\n  ")
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestNormalizeGPUName_KnownPatterns(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// nvidia-smi output
		{"NVIDIA GeForce RTX 5090", "5090"},
		// "laptop gpu" → "laptop"
		{"NVIDIA GeForce RTX 3050 8GB Laptop GPU", "3050 8gb laptop"},
		{"NVIDIA RTX 6000 Ada Generation", "6000 ada generation"},

		// rocm-smi / system_profiler
		{"AMD Radeon RX 7900 XTX", "7900 xtx"},
		{"Apple M4 Max", "m4 max"},

		// lspci descriptions — comma breaks prefix match, so vendor stays
		{"Advanced Micro Devices, Inc. [AMD/ATI] Radeon RX 6800 XT", "advanced micro devices inc amdati radeon rx 6800 xt"},
		{"NVIDIA Corporation GP104 [GeForce GTX 1070]", "gp104 geforce gtx 1070"},
		{"Advanced Micro Devices, Inc. [AMD/ATI] Navi 31 [Radeon RX 7900 XTX]", "advanced micro devices inc amdati navi 31 radeon rx 7900 xtx"},

		// Just vendor — prefix stripped, remainder is canonical
		{"NVIDIA Corporation", "corporation"},
		{"AMD", "amd"},
		{"Intel Corporation", "corporation"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := NormalizeGPUName(tc.input)
			if got != tc.expected {
				t.Errorf("NormalizeGPUName(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}
