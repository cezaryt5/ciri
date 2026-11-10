package tui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/cezaryt5/ciri/internal/downloader"
	"github.com/cezaryt5/ciri/internal/model"
)

func testDownloadsModel() *downloadsModel {
	return &downloadsModel{
		events:     make(chan tea.Msg, 16),
		selectedID: -1,
	}
}

func addTestJob(dm *downloadsModel, status jobStatus) *downloadJob {
	job := &downloadJob{
		id:      dm.nextID,
		repo:    "owner/model-GGUF",
		display: "Test Model",
		quant:   "Q4_K_M",
		files: []model.QuantFile{
			{Path: "model-q4_k_m.gguf", Size: 4 << 30},
		},
		status:   status,
		byteSize: 4 << 30,
	}
	dm.nextID++
	dm.jobs = append(dm.jobs, job)
	return job
}

func TestDownloads_OrderedLiveBeforeHistory(t *testing.T) {
	dm := testDownloadsModel()
	done := addTestJob(dm, jobDone)
	done.finished = time.Now()
	queued := addTestJob(dm, jobQueued)
	active := addTestJob(dm, jobDownloading)

	order := dm.ordered()
	if len(order) != 3 {
		t.Fatalf("ordered length = %d, want 3", len(order))
	}
	if order[0] != queued || order[1] != active || order[2] != done {
		t.Fatalf("unexpected order: %d, %d, %d", order[0].id, order[1].id, order[2].id)
	}
}

func TestDownloads_MoveSelectionAndClearHistory(t *testing.T) {
	dm := testDownloadsModel()
	queued := addTestJob(dm, jobQueued)
	done := addTestJob(dm, jobDone)
	done.finished = time.Now()
	dm.selectedID = queued.id

	dm.moveSelection(1)
	if dm.selectedID != done.id {
		t.Fatalf("selection = %d, want finished job %d", dm.selectedID, done.id)
	}

	dm.clearHistory()
	if len(dm.jobs) != 1 || dm.jobs[0] != queued {
		t.Fatalf("history clear kept %d jobs: %+v", len(dm.jobs), dm.jobs)
	}
	if dm.selectedID != queued.id {
		t.Fatalf("selection after clear = %d, want %d", dm.selectedID, queued.id)
	}
}

func TestDownloads_CancelQueued(t *testing.T) {
	dm := testDownloadsModel()
	job := addTestJob(dm, jobQueued)
	dm.selectedID = job.id

	dm.cancelSelected()
	if job.status != jobCancelled {
		t.Fatalf("status = %v, want cancelled", job.status)
	}
	if job.finished.IsZero() {
		t.Fatal("cancelled job must get a finish timestamp")
	}
}

func TestDownloads_PromptOnSecondJob(t *testing.T) {
	dm := testDownloadsModel()
	dm.modeChosen = false
	addTestJob(dm, jobDownloading)

	job := dm.enqueue("owner/second-GGUF", "Second", "Q5_K_M", []model.QuantFile{
		{Path: "model-q5_k_m.gguf", Size: 1 << 30},
	})
	if !dm.promptActive() || dm.promptID != job.id {
		t.Fatalf("expected concurrency prompt for job %d, prompt=%d", job.id, dm.promptID)
	}
	if job.status != jobQueued {
		t.Fatalf("second job status = %v, want queued while prompting", job.status)
	}

	dm.answerPrompt(true)
	if !dm.modeChosen || !dm.concurrent {
		t.Fatal("concurrent choice must be remembered for the session")
	}
	if dm.promptActive() {
		t.Fatal("prompt must close after an answer")
	}
}

func TestDownloads_EnqueueDeduplicates(t *testing.T) {
	dm := testDownloadsModel()
	first := addTestJob(dm, jobDownloading)

	got := dm.enqueue(first.repo, "Again", first.quant, first.files)
	if got != first {
		t.Fatalf("duplicate enqueue returned job %d, want %d", got.id, first.id)
	}
	if len(dm.jobs) != 1 {
		t.Fatalf("jobs = %d, want 1 after duplicate enqueue", len(dm.jobs))
	}
}

func TestDownloads_ViewFitsTerminal(t *testing.T) {
	for _, width := range []int{60, 80, 120} {
		app := testApp("1.0.0")
		app.width = width
		app.height = 30
		app.screen = screenDownload
		dm := testDownloadsModel()
		app.downloads = dm

		done := addTestJob(dm, jobDone)
		done.finished = time.Now()
		addTestJob(dm, jobQueued)
		active := addTestJob(dm, jobDownloading)
		active.progress = downloader.Progress{
			File: "model-q4_k_m.gguf", Total: 4 << 30, Downloaded: 2 << 30,
			SpeedBps: 8 << 20, Phase: "downloading",
		}
		dm.selectedID = active.id

		view := app.View()
		lines := strings.Split(view, "\n")
		if len(lines) > app.height {
			t.Fatalf("width %d: %d lines exceeds height %d", width, len(lines), app.height)
		}
		for i, line := range lines {
			if w := ansi.StringWidth(line); w > width {
				t.Fatalf("width %d: line %d is %d cells wide: %q", width, i, w, line)
			}
		}
		for _, want := range []string{"DOWNLOAD MANAGER", "DOWNLOADING", "DONE"} {
			if !strings.Contains(view, want) {
				t.Fatalf("width %d: view missing %q:\n%s", width, want, view)
			}
		}
	}
}

func TestDownloads_ViewPromptAndScrollFit(t *testing.T) {
	app := testApp("1.0.0")
	app.width = 60
	app.height = 24
	app.screen = screenDownload
	dm := testDownloadsModel()
	app.downloads = dm

	addTestJob(dm, jobDownloading)
	for i := 0; i < 20; i++ {
		addTestJob(dm, jobQueued)
	}
	dm.promptID = dm.jobs[1].id
	dm.selectedID = dm.jobs[len(dm.jobs)-1].id

	view := app.View()
	lines := strings.Split(view, "\n")
	if len(lines) > app.height {
		t.Fatalf("%d lines exceeds height %d", len(lines), app.height)
	}
	for i, line := range lines {
		if w := ansi.StringWidth(line); w > app.width {
			t.Fatalf("line %d is %d cells wide: %q", i, w, line)
		}
	}
	if !strings.Contains(view, "CONCURRENT") {
		t.Fatalf("prompt view missing concurrency choice:\n%s", view)
	}
	if !strings.Contains(view, "ABOVE") {
		t.Fatalf("scrolled view missing the above indicator:\n%s", view)
	}
}

func TestDownloads_EscLeavesWithoutCancelling(t *testing.T) {
	app := testApp("1.0.0")
	app.screen = screenDownload
	app.dlOrigin = screenDetail
	dm := testDownloadsModel()
	app.downloads = dm

	cancelled := false
	job := addTestJob(dm, jobDownloading)
	job.cancel = func() { cancelled = true }
	dm.selectedID = job.id

	cmd := dm.update(app, tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc must navigate back")
	}
	updated, _ := app.Update(cmd())
	if updated.(*App).screen != screenDetail {
		t.Fatalf("screen = %v, want detail", updated.(*App).screen)
	}
	if cancelled {
		t.Fatal("leaving the download screen must not cancel the job")
	}
	if job.status != jobDownloading {
		t.Fatalf("job status = %v, want still downloading", job.status)
	}
}

func TestLegacyWeightFiles_PrefersMatchingQuant(t *testing.T) {
	files := []downloader.RepoFile{
		{Path: "README.md", Size: 10},
		{Path: "model-q4_k_m.gguf", Size: 100},
		{Path: "model-q8_0.gguf", Size: 200},
	}
	got := legacyWeightFiles(files, "Q8_0")
	if len(got) != 1 || got[0].Path != "model-q8_0.gguf" {
		t.Fatalf("legacy filter = %+v, want only the matching quant", got)
	}
}

func TestDownloads_DownloadCompletesAndLandsInHistory(t *testing.T) {
	payload := []byte("ciri test payload")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	t.Setenv("CIRI_MODELS_DIR", t.TempDir())
	client := downloader.NewClient()
	client.Endpoint = srv.URL
	dm := newDownloadsModel(client)

	job := dm.enqueue("owner/repo", "Test Model", "Q4_K_M", []model.QuantFile{
		{Path: "model.gguf", Size: int64(len(payload))},
	})

	deadline := time.After(5 * time.Second)
	for !job.final() {
		select {
		case msg := <-dm.events:
			dm.handleMsg(msg)
		case <-deadline:
			t.Fatalf("download did not finish, job status = %v", job.status)
		}
	}

	if job.status != jobDone {
		t.Fatalf("status = %v, want done (err %v)", job.status, job.err)
	}
	dest := filepath.Join(downloader.RepoDestDir("owner/repo"), "model.gguf")
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("downloaded file missing: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("downloaded payload = %q, want %q", got, payload)
	}
}
