package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/cezaryt5/ciri/internal/downloader"
	"github.com/cezaryt5/ciri/internal/model"
)

// maxConcurrentDownloads caps the concurrent mode so a queue of shards cannot
// open an unbounded number of connections against the hub.
const maxConcurrentDownloads = 2

type jobStatus int

const (
	jobQueued jobStatus = iota
	jobResolving
	jobDownloading
	jobVerifying
	jobDone
	jobFailed
	jobCancelled
)

type downloadProgressMsg struct {
	jobID    int
	progress downloader.Progress
}

type downloadDoneMsg struct {
	jobID int
	err   error
}

// downloadFilesMsg resolves a legacy flat-catalog repo (one without per-file
// metadata in the catalog) into the weight files that should be fetched.
type downloadFilesMsg struct {
	jobID int
	files []model.QuantFile
	err   error
}

type downloadJob struct {
	id         int
	repo       string
	display    string
	quant      string
	files      []model.QuantFile
	fileIdx    int
	status     jobStatus
	progress   downloader.Progress
	err        error
	cancel     context.CancelFunc
	cancelling bool
	finished   time.Time
	byteSize   int64
}

func (j *downloadJob) final() bool {
	return j.status == jobDone || j.status == jobFailed || j.status == jobCancelled
}

func (j *downloadJob) totalSize() int64 {
	if j.byteSize > 0 {
		return j.byteSize
	}
	var n int64
	for _, f := range j.files {
		n += f.Size
	}
	return n
}

// downloadsModel owns the download queue for the whole app. It deliberately
// knows nothing about the catalog browser: downloads are started from model
// detail and this screen is only the manager.
type downloadsModel struct {
	client *downloader.Client
	jobs   []*downloadJob
	events chan tea.Msg
	nextID int

	selectedID int
	scroll     int

	// promptID is the job waiting for the user to choose between concurrent
	// and sequential mode. modeChosen remembers the answer for the session.
	promptID   int
	modeChosen bool
	concurrent bool
}

func newDownloadsModel(client *downloader.Client) *downloadsModel {
	if client == nil {
		client = downloader.NewClient()
	}
	return &downloadsModel{
		client:     client,
		events:     make(chan tea.Msg, 256),
		selectedID: -1,
	}
}

// waitEvent keeps one reader on the event channel for the program lifetime.
// Every handled download message re-arms it.
func (dm *downloadsModel) waitEvent() tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-dm.events
		if !ok {
			return nil
		}
		return msg
	}
}

func (dm *downloadsModel) byID(id int) *downloadJob {
	for _, j := range dm.jobs {
		if j.id == id {
			return j
		}
	}
	return nil
}

func (dm *downloadsModel) activeCount() int {
	n := 0
	for _, j := range dm.jobs {
		switch j.status {
		case jobResolving, jobDownloading, jobVerifying:
			n++
		}
	}
	return n
}

func (dm *downloadsModel) ordered() []*downloadJob {
	live := make([]*downloadJob, 0, len(dm.jobs))
	history := make([]*downloadJob, 0, len(dm.jobs))
	for _, j := range dm.jobs {
		if j.final() {
			history = append(history, j)
		} else {
			live = append(live, j)
		}
	}
	sort.SliceStable(live, func(i, j int) bool { return live[i].id < live[j].id })
	sort.SliceStable(history, func(i, j int) bool { return history[i].finished.After(history[j].finished) })
	return append(live, history...)
}

func (dm *downloadsModel) indexOf(order []*downloadJob) int {
	for i, j := range order {
		if j.id == dm.selectedID {
			return i
		}
	}
	return 0
}

func (dm *downloadsModel) selectedJob() *downloadJob {
	order := dm.ordered()
	if len(order) == 0 {
		return nil
	}
	for _, j := range order {
		if j.id == dm.selectedID {
			return j
		}
	}
	dm.selectedID = order[0].id
	return order[0]
}

func (dm *downloadsModel) moveSelection(delta int) {
	order := dm.ordered()
	if len(order) == 0 {
		return
	}
	idx := dm.indexOf(order) + delta
	if idx < 0 {
		idx = 0
	}
	if idx >= len(order) {
		idx = len(order) - 1
	}
	dm.selectedID = order[idx].id
}

// enqueue adds a job and starts it when the current mode allows. A second job
// in an unconfigured session opens the concurrency prompt instead of guessing.
func (dm *downloadsModel) enqueue(repo, display, quant string, files []model.QuantFile) *downloadJob {
	for _, j := range dm.jobs {
		if !j.final() && j.repo == repo && j.quant == quant {
			dm.selectedID = j.id
			return j
		}
	}

	job := &downloadJob{
		id:      dm.nextID,
		repo:    repo,
		display: display,
		quant:   quant,
		files:   append([]model.QuantFile(nil), files...),
		status:  jobQueued,
	}
	dm.nextID++
	job.byteSize = sumFileSizes(job.files)
	dm.jobs = append(dm.jobs, job)
	dm.selectedID = job.id

	switch {
	case dm.activeCount() == 0:
		dm.promptID = 0
		dm.startJob(job)
	case !dm.modeChosen:
		if dm.promptID == 0 {
			dm.promptID = job.id
		}
	case dm.concurrent && dm.activeCount() < maxConcurrentDownloads:
		dm.startJob(job)
	}
	return job
}

func (dm *downloadsModel) startJob(job *downloadJob) {
	if job == nil || job.status != jobQueued {
		return
	}
	if dm.client == nil {
		dm.finishJob(job, errors.New("downloader unavailable"))
		return
	}
	if len(job.files) == 0 {
		dm.startResolve(job)
		return
	}
	dm.startCurrentFile(job)
}

func (dm *downloadsModel) startResolve(job *downloadJob) {
	job.status = jobResolving
	ctx, cancel := context.WithCancel(context.Background())
	job.cancel = cancel

	id, repo, quant := job.id, job.repo, job.quant
	go func() {
		files, err := dm.client.ListRepoFiles(ctx, repo, "main")
		var out []model.QuantFile
		if err == nil {
			out = legacyWeightFiles(files, quant)
			if len(out) == 0 {
				err = errors.New("no weight files found in repository")
			}
		}
		dm.events <- downloadFilesMsg{jobID: id, files: out, err: err}
	}()
}

func (dm *downloadsModel) startCurrentFile(job *downloadJob) {
	if job.fileIdx >= len(job.files) {
		dm.finishJob(job, nil)
		dm.pumpQueue()
		return
	}

	f := job.files[job.fileIdx]
	if job.cancel != nil {
		job.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	job.cancel = cancel
	job.cancelling = false
	job.status = jobDownloading
	job.progress = downloader.Progress{
		Dest:  filepath.Join(downloader.RepoDestDir(job.repo), filepath.FromSlash(f.Path)),
		File:  f.Path,
		Total: f.Size,
		Phase: "downloading",
	}

	req := downloader.Request{
		Repo:        job.repo,
		Revision:    "main",
		Path:        f.Path,
		DestDir:     downloader.RepoDestDir(job.repo),
		SizeHint:    f.Size,
		SHA256:      f.SHA256,
		Connections: downloader.DefaultConnections,
	}
	id := job.id
	go func() {
		_, err := dm.client.Download(ctx, req, func(p downloader.Progress) {
			select {
			case dm.events <- downloadProgressMsg{jobID: id, progress: p}:
			default:
			}
		})
		dm.events <- downloadDoneMsg{jobID: id, err: err}
	}()
}

// handleMsg consumes downloader messages regardless of which screen is visible,
// which is what lets downloads keep running while the user browses the catalog.
func (dm *downloadsModel) handleMsg(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case downloadProgressMsg:
		if job := dm.byID(msg.jobID); job != nil {
			job.progress = msg.progress
			switch msg.progress.Phase {
			case "verifying":
				job.status = jobVerifying
			default:
				if job.status != jobCancelled {
					job.status = jobDownloading
				}
			}
		}
		return dm.waitEvent()

	case downloadFilesMsg:
		job := dm.byID(msg.jobID)
		if job == nil {
			return dm.waitEvent()
		}
		if job.cancel != nil {
			job.cancel()
		}
		job.cancel = nil
		if msg.err != nil {
			dm.finishJob(job, msg.err)
			dm.pumpQueue()
			return dm.waitEvent()
		}
		if job.cancelling {
			dm.finishJob(job, context.Canceled)
			dm.pumpQueue()
			return dm.waitEvent()
		}
		job.files = msg.files
		job.fileIdx = 0
		job.byteSize = sumFileSizes(msg.files)
		job.status = jobDownloading
		dm.startCurrentFile(job)
		return dm.waitEvent()

	case downloadDoneMsg:
		job := dm.byID(msg.jobID)
		if job == nil {
			return dm.waitEvent()
		}
		if msg.err != nil {
			dm.finishJob(job, msg.err)
			dm.pumpQueue()
			return dm.waitEvent()
		}
		if job.cancelling {
			dm.finishJob(job, context.Canceled)
			dm.pumpQueue()
			return dm.waitEvent()
		}
		job.fileIdx++
		if job.fileIdx < len(job.files) {
			dm.startCurrentFile(job)
		} else {
			dm.finishJob(job, nil)
			dm.pumpQueue()
		}
		return dm.waitEvent()
	}
	return nil
}

func (dm *downloadsModel) finishJob(job *downloadJob, err error) {
	cancelling := job.cancelling
	if job.cancel != nil {
		job.cancel()
		job.cancel = nil
	}
	job.finished = time.Now()
	job.cancelling = false
	job.err = err
	switch {
	case err == nil:
		job.status = jobDone
		job.progress.Phase = "done"
		job.progress.Downloaded = job.progress.Total
	case cancelling || errors.Is(err, context.Canceled):
		job.status = jobCancelled
	default:
		job.status = jobFailed
	}
}

// pumpQueue starts queued jobs up to the current concurrency limit. While a
// prompt is open nothing is started: the user is deciding the mode.
func (dm *downloadsModel) pumpQueue() {
	if dm.promptID != 0 {
		if dm.activeCount() > 0 {
			return
		}
		// Nothing is running, so there is no concurrency decision to make.
		dm.promptID = 0
	}
	limit := 1
	if dm.modeChosen && dm.concurrent {
		limit = maxConcurrentDownloads
	}
	for dm.activeCount() < limit {
		var next *downloadJob
		for _, j := range dm.jobs {
			if j.status == jobQueued {
				if next == nil || j.id < next.id {
					next = j
				}
			}
		}
		if next == nil {
			return
		}
		dm.startJob(next)
	}
}

func (dm *downloadsModel) answerPrompt(concurrent bool) {
	dm.modeChosen = true
	dm.concurrent = concurrent
	dm.promptID = 0
	dm.pumpQueue()
}

func (dm *downloadsModel) cancelSelected() {
	job := dm.selectedJob()
	if job == nil {
		return
	}
	switch job.status {
	case jobQueued:
		if dm.promptID == job.id {
			dm.promptID = 0
		}
		job.status = jobCancelled
		job.finished = time.Now()
		dm.pumpQueue()
	case jobResolving, jobDownloading, jobVerifying:
		job.cancelling = true
		if job.cancel != nil {
			job.cancel()
			return
		}
		dm.finishJob(job, context.Canceled)
		dm.pumpQueue()
	}
}

func (dm *downloadsModel) clearHistory() {
	kept := dm.jobs[:0]
	for _, j := range dm.jobs {
		if !j.final() {
			kept = append(kept, j)
		}
	}
	dm.jobs = kept
	dm.scroll = 0
	if len(dm.jobs) == 0 {
		dm.selectedID = -1
		return
	}
	order := dm.ordered()
	dm.selectedID = order[0].id
}

func (dm *downloadsModel) update(a *App, msg tea.KeyMsg) tea.Cmd {
	if dm.promptID != 0 {
		switch msg.String() {
		case "c":
			dm.answerPrompt(true)
		case "s", "esc":
			dm.answerPrompt(false)
		}
		return nil
	}

	switch msg.String() {
	case "esc":
		target := a.dlOrigin
		if target == screenDownload {
			target = screenHome
		}
		return func() tea.Msg { return navigateMsg{target: target} }
	case "up", "k":
		dm.moveSelection(-1)
	case "down", "j":
		dm.moveSelection(1)
	case "x":
		dm.cancelSelected()
	case "c":
		dm.clearHistory()
	}
	return nil
}

func (dm *downloadsModel) promptActive() bool {
	return dm.promptID != 0
}

func (dm *downloadsModel) view(a *App) string {
	inner := dlInnerWidth(a)
	ordered := dm.ordered()
	selIdx := dm.indexOf(ordered)

	var b strings.Builder
	b.WriteString("  " + alignLine(Label.Render("DOWNLOAD MANAGER"), Tagline.Render(dm.summary()), inner) + "\n")
	b.WriteString("  " + Rule.Render(strings.Repeat("\u2500", inner)) + "\n")

	if dm.promptID != 0 {
		b.WriteString("  " + StatusAlert.Render("\u25a0") + " " +
			Value.Render("A DOWNLOAD IS ALREADY RUNNING") + "\n")
		b.WriteString("  " + HintKey.Render("C") + " " + HintLabel.Render("CONCURRENT") + "   " +
			HintKey.Render("S") + " " + HintLabel.Render("QUEUE") + "   " +
			HintKey.Render("ESC") + " " + HintLabel.Render("QUEUE") + "\n")
	}

	if len(ordered) == 0 {
		b.WriteString("\n  " + Label.Render("NO DOWNLOADS YET") + "\n")
		b.WriteString("  " + Tagline.Render("START ONE FROM A MODEL'S DETAIL PAGE") + "\n")
		return b.String()
	}

	lineBudget := a.height - 9
	if lineBudget < 6 {
		lineBudget = 6
	}
	promptLines := 0
	if dm.promptID != 0 {
		promptLines = 2
	}
	maxRows := lineBudget - 4 - promptLines
	if maxRows < 1 {
		maxRows = 1
	}

	if selIdx < dm.scroll {
		dm.scroll = selIdx
	}
	if selIdx >= dm.scroll+maxRows {
		dm.scroll = selIdx - maxRows + 1
	}
	maxScroll := len(ordered) - maxRows
	if maxScroll < 0 {
		maxScroll = 0
	}
	if dm.scroll > maxScroll {
		dm.scroll = maxScroll
	}
	if dm.scroll < 0 {
		dm.scroll = 0
	}

	end := dm.scroll + maxRows
	if end > len(ordered) {
		end = len(ordered)
	}

	if dm.scroll > 0 {
		b.WriteString("  " + Tagline.Render(fmt.Sprintf("\u2191 %d ABOVE", dm.scroll)) + "\n")
	} else {
		b.WriteString("\n")
	}
	for i := dm.scroll; i < end; i++ {
		b.WriteString(renderDownloadRow(ordered[i], i == selIdx, inner) + "\n")
	}
	if end < len(ordered) {
		b.WriteString("  " + Tagline.Render(fmt.Sprintf("\u2193 %d MORE", len(ordered)-end)) + "\n")
	} else {
		b.WriteString("\n")
	}
	return b.String()
}

func (dm *downloadsModel) summary() string {
	active, queued, history := 0, 0, 0
	for _, j := range dm.jobs {
		switch j.status {
		case jobResolving, jobDownloading, jobVerifying:
			active++
		case jobQueued:
			queued++
		default:
			history++
		}
	}
	return fmt.Sprintf("%d ACTIVE  %d QUEUED  %d HISTORY", active, queued, history)
}

func jobStatusLabel(job *downloadJob) (string, lipgloss.Style) {
	if job.cancelling {
		return "CANCELLING", StatusAlert
	}
	switch job.status {
	case jobQueued:
		return "QUEUED", Tagline
	case jobResolving:
		return "RESOLVING", ValueDim
	case jobDownloading:
		return "DOWNLOADING", StatusOn
	case jobVerifying:
		return "VERIFYING", ValueDim
	case jobDone:
		return "DONE", FitPerfect
	case jobFailed:
		return "FAILED", StatusAlert
	case jobCancelled:
		return "CANCELLED", Tagline
	}
	return "", Tagline
}

func jobDetail(job *downloadJob) string {
	switch job.status {
	case jobDownloading, jobVerifying:
		p := job.progress
		pct := 0
		if p.Total > 0 {
			pct = int(p.Downloaded * 100 / p.Total)
			if pct > 100 {
				pct = 100
			}
		}
		parts := make([]string, 0, 4)
		if len(job.files) > 1 {
			parts = append(parts, fmt.Sprintf("%d/%d", job.fileIdx+1, len(job.files)))
		}
		parts = append(parts, fmt.Sprintf("%d%%", pct))
		if p.SpeedBps > 0 {
			parts = append(parts, formatSpeed(p.SpeedBps))
		}
		if p.ETA > 0 && p.Phase == "downloading" {
			parts = append(parts, "ETA "+shortETA(p.ETA))
		}
		return strings.Join(parts, "  ")
	case jobQueued, jobResolving:
		if job.byteSize > 0 {
			return fmt.Sprintf("%s  %s", filesLabel(len(job.files)), formatBytes(job.byteSize))
		}
		return "WAITING"
	case jobDone:
		return fmt.Sprintf("%s  %s", filesLabel(len(job.files)), formatBytes(job.totalSize()))
	case jobFailed:
		if job.err != nil {
			msg := strings.Join(strings.Fields(job.err.Error()), " ")
			return strings.ToUpper(truncate(msg, 36))
		}
		return "FAILED"
	case jobCancelled:
		if job.progress.Downloaded > 0 {
			return "PARTIAL KEPT"
		}
		return ""
	}
	return ""
}

func renderDownloadRow(job *downloadJob, selected bool, inner int) string {
	status, statusStyle := jobStatusLabel(job)
	name := job.display
	if name == "" {
		name = job.repo
	}
	label := name
	if job.quant != "" {
		label += " \u00b7 " + job.quant
	}
	right := jobDetail(job)

	const prefixW = 4
	const statusW = 13
	const gap = "  "
	rightW := lipgloss.Width(right)
	labelW := inner - prefixW - statusW - len(gap) - len(gap) - rightW
	if labelW < 10 {
		right = ""
		rightW = 0
		labelW = inner - prefixW - statusW - len(gap)
	}
	if labelW < 4 {
		labelW = 4
	}
	label = padRight(truncate(label, labelW), labelW)
	status = padRight(truncate(status, statusW), statusW)

	prefix := "  " + Faint.Render(" ") + " "
	if selected {
		prefix = "  " + Cursor.Render("\u258c") + " "
	}

	if selected {
		plain := status + gap + label
		if right != "" {
			plain += gap + padRight(right, rightW)
		}
		restW := inner - lipgloss.Width(prefix)
		if restW < 0 {
			restW = 0
		}
		return prefix + SelectedRow.Width(restW).MaxWidth(restW).Render(plain)
	}

	line := prefix + statusStyle.Render(status) + gap + Value.Render(label)
	if right != "" {
		line += gap + Tagline.Render(padRight(right, rightW))
	}
	return line
}

func sumFileSizes(files []model.QuantFile) int64 {
	var n int64
	for _, f := range files {
		n += f.Size
	}
	return n
}

func filesLabel(n int) string {
	if n == 1 {
		return "1 FILE"
	}
	return fmt.Sprintf("%d FILES", n)
}

// legacyWeightFiles picks weight-bearing files for a flat-catalog repo. When
// the quant label appears in a filename, only those files are used; otherwise
// every weight file is downloaded.
func legacyWeightFiles(files []downloader.RepoFile, quant string) []model.QuantFile {
	ql := strings.ToLower(strings.TrimSpace(quant))
	var all, match []model.QuantFile
	for _, f := range files {
		if !weightFile(f.Path) {
			continue
		}
		qf := model.QuantFile{Path: f.Path, Size: f.Size, SHA256: f.SHA256}
		all = append(all, qf)
		if ql != "" && strings.Contains(strings.ToLower(f.Path), ql) {
			match = append(match, qf)
		}
	}
	if len(match) > 0 {
		return match
	}
	return all
}

func weightFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".gguf", ".safetensors", ".bin", ".pt", ".pth":
		return true
	}
	return false
}

func shortETA(d time.Duration) string {
	if d <= 0 {
		return "\u2014"
	}
	d = d.Round(time.Second)
	if d >= time.Hour {
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	if d >= time.Minute {
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

func dlInnerWidth(a *App) int {
	inner := a.width - 6
	if inner < 24 {
		inner = 24
	}
	return inner
}

func formatSpeed(bps float64) string {
	if bps <= 0 {
		return "\u2014"
	}
	return formatBytes(int64(bps)) + "/s"
}
