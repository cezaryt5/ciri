package tui

import (
	"strings"
	"testing"

	"github.com/cezaryt5/Can_I_Run_IT/internal/hardware"
	"github.com/cezaryt5/Can_I_Run_IT/internal/model"
	"github.com/cezaryt5/Can_I_Run_IT/internal/predictor"
)

func TestApp_VersionStored(t *testing.T) {
	gpuDB := []hardware.GPU{
		{Name: "RTX 4090", VRAMGB: 24},
	}
	models := []model.Model{
		{Name: "test-model", MinVRAMGB: 4, MinRAMGB: 8},
	}
	pred := predictor.NewPredictor(&gpuDB[0], 32, models, nil)

	app := NewApp(hardware.Specs{RamAvailGB: 32}, &gpuDB[0], models, pred, nil, "1.0.0")
	if app.version != "1.0.0" {
		t.Errorf("expected version \"1.0.0\", got %q", app.version)
	}
}

func TestApp_VersionEmptyDefault(t *testing.T) {
	gpuDB := []hardware.GPU{
		{Name: "RTX 4090", VRAMGB: 24},
	}
	models := []model.Model{
		{Name: "test-model", MinVRAMGB: 4, MinRAMGB: 8},
	}
	pred := predictor.NewPredictor(&gpuDB[0], 32, models, nil)

	app := NewApp(hardware.Specs{RamAvailGB: 32}, &gpuDB[0], models, pred, nil, "")
	if app.version != "" {
		t.Errorf("expected empty version, got %q", app.version)
	}
}

func TestApp_ViewContainsVersionWhenSet(t *testing.T) {
	gpuDB := []hardware.GPU{
		{Name: "RTX 4090", VRAMGB: 24},
	}
	models := []model.Model{
		{Name: "test-model", MinVRAMGB: 4, MinRAMGB: 8},
	}
	pred := predictor.NewPredictor(&gpuDB[0], 32, models, nil)
	app := NewApp(hardware.Specs{RamAvailGB: 32}, &gpuDB[0], models, pred, nil, "1.0.0")
	app.width = 80
	app.height = 40

	view := app.View()
	if !strings.Contains(view, "CIRI v1.0.0") {
		t.Errorf("expected View to contain \"CIRI v1.0.0\", got:\n%s", view)
	}
}

func TestApp_ViewContainsCiriWithoutVersion(t *testing.T) {
	gpuDB := []hardware.GPU{
		{Name: "RTX 4090", VRAMGB: 24},
	}
	models := []model.Model{
		{Name: "test-model", MinVRAMGB: 4, MinRAMGB: 8},
	}
	pred := predictor.NewPredictor(&gpuDB[0], 32, models, nil)
	app := NewApp(hardware.Specs{RamAvailGB: 32}, &gpuDB[0], models, pred, nil, "")
	app.width = 80
	app.height = 40

	view := app.View()
	if !strings.Contains(view, "CIRI") {
		t.Errorf("expected View to contain \"CIRI\", got:\n%s", view)
	}
	if strings.Contains(view, "CIRI v") {
		t.Errorf("expected View NOT to contain \"CIRI v\" when version is empty, got:\n%s", view)
	}
}
