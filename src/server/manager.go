// 下载列表:排队、持久化、实时进度,以及引擎与前端之间的桥。
// 全部状态在一把互斥锁后,且从不在持锁时做长操作。
//
// 上游经 Tauri 事件推送快照;fnOS 版改为版本号 + 轮询:每次可感知变化
// version 自增,前端带旧版本号来问,没变就不回传数据。

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Status string

const (
	StatusQueued      Status = "queued"
	StatusDownloading Status = "downloading"
	StatusPaused      Status = "paused"
	StatusCompleted   Status = "completed"
	StatusError       Status = "error"
)

// Item 的 JSON 字段与上游 downloads.json 兼容(camelCase)。
// 实时字段(speed/connections/segments/note)随快照一起序列化,
// 加载时清零。
type Item struct {
	ID          uint64      `json:"id"`
	URL         string      `json:"url"`
	Filename    string      `json:"filename"`
	Path        string      `json:"path"`
	Size        *int64      `json:"size"`
	Downloaded  int64       `json:"downloaded"`
	Status      Status      `json:"status"`
	Error       *string     `json:"error"`
	Category    string      `json:"category"`
	AddedAt     int64       `json:"addedAt"` // epoch 毫秒
	CompletedAt *int64      `json:"completedAt"`
	Resumable   bool        `json:"resumable"`
	Headers     [][2]string `json:"headers"` // Cookie/Referer/UA 等,完成后清空
	Remaining   [][2]int64  `json:"remaining"`
	Segments    [][3]int64  `json:"segments"` // 每连接 (start,pos,end),仅下载中
	Speed       float64     `json:"speed"`
	Connections int         `json:"connections"`
	Note        string      `json:"note"`
	// 非 nil 即 BT 任务:选中的文件下标。
	TorrentFiles *[]int `json:"torrentFiles,omitempty"`
	// 重新下载(一次性,不落盘):本次运行要把分片完成记录按磁盘重校。
	Overwrite bool     `json:"-"`
	Checksum  string   `json:"checksum,omitempty"`
	Mirrors   []string `json:"mirrors,omitempty"`
	Verified  bool     `json:"verified,omitempty"`
}

type AddRequest struct {
	URL          string      `json:"url"`
	Filename     string      `json:"filename"`
	Dir          string      `json:"dir"`
	Headers      [][2]string `json:"headers"`
	Start        bool        `json:"start"`
	Size         *int64      `json:"size"`
	Checksum     string      `json:"checksum"`
	Mirrors      []string    `json:"mirrors"`
	TorrentFiles *[]int      `json:"torrentFiles"`
	// 重新下载:目标路径已存在同名时,用户在确认框选了「重新下载」——
	// 先清掉同路径旧任务与旧文件,再干净地新建(绕过自动改名)。
	Overwrite bool `json:"overwrite"`
}

type runningTask struct {
	path     string
	cancel   context.CancelFunc
	progress *Progress
	note     *Note
	speed    float64
	wire     bool // 速度当前是否用 Wire 收包计数(BT)
	// 最近 ~10s 的(时刻,流量计数)样本;速度 = 窗口两端差分。
	// 逐拍差分不管对什么计数(分片完成、16KB 收包)都是锯齿,EMA 也压不住。
	window []rateSample
}

type rateSample struct {
	at   time.Time
	rate int64
}

type manager struct {
	client  *http.Client
	limiter *SpeedLimiter
	dataDir string
	// 每次可感知变化自增;前端轮询凭它跳过未变的快照。
	version atomic.Uint64

	mu       sync.Mutex
	items    []*Item
	settings Settings
	running  map[uint64]*runningTask
	// 删除时勾了「同时删除本地文件」的运行中任务:文件还挂在引擎手里,
	// 等收尾(finish)时再删主文件。
	deleteOnFinish map[uint64]bool
	nextID         uint64
	dirty          bool
}

func newManager(dataDir, defaultDownloadDir string) (*manager, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	m := &manager{dataDir: dataDir, nextID: 1, running: map[uint64]*runningTask{}, deleteOnFinish: map[uint64]bool{}}
	m.settings = defaultSettings()
	loadJSONFile(filepath.Join(dataDir, "settings.json"), &m.settings)
	if m.settings.DownloadDir == "" && defaultDownloadDir != "" {
		m.settings.DownloadDir = defaultDownloadDir
	}
	if m.settings.Connections <= 0 {
		m.settings.Connections = 8
	}
	if m.settings.MaxConcurrent <= 0 {
		m.settings.MaxConcurrent = 3
	}
	var items []*Item
	loadJSONFile(filepath.Join(dataDir, "downloads.json"), &items)
	for _, it := range items {
		if it.ID >= m.nextID {
			m.nextID = it.ID + 1
		}
		// 服务中断时正在下载的任务:恢复为暂停。
		if it.Status == StatusDownloading {
			it.Status = StatusPaused
		}
		if it.Status != StatusCompleted && len(it.Remaining) == 0 {
			it.Remaining = SavedRemaining(it.Path)
		}
		// 落盘时是实时的数字,现在不作数了。
		it.Speed = 0
		it.Connections = 0
		it.Segments = nil
		it.Note = ""
	}
	m.items = items
	client, err := buildClient(m.settings.Proxy)
	if err != nil {
		return nil, err
	}
	m.client = client
	m.limiter = NewSpeedLimiter(int64(m.settings.SpeedLimitKib) * 1024)
	m.version.Store(1)
	return m, nil
}

func (m *manager) settings_() Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings
}

func (m *manager) snapshot() []Item {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Item, len(m.items))
	for i, it := range m.items {
		out[i] = *it
	}
	return out
}

// 此类别文件的默认保存目录。下载目录必须由用户在授权文件夹里选定;
// 没选之前返回空串,添加任务时会被拦下。
func (m *manager) defaultDir(category string) string {
	s := m.settings_()
	if s.DownloadDir == "" {
		return ""
	}
	if s.SortIntoCategories && category != "Other" {
		return filepath.Join(s.DownloadDir, category)
	}
	return s.DownloadDir
}

func (m *manager) item(id uint64) *Item {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, it := range m.items {
		if it.ID == id {
			out := *it
			return &out
		}
	}
	return nil
}

func (m *manager) add(req AddRequest) uint64 {
	filename := strings.TrimSpace(req.Filename)
	path := ResolveDest(req.Dir, filename)
	if req.Overwrite && filename != "" {
		// 重新下载:路径原样使用,不让 ResolveDest 改名成 a (1).zip。
		path = filepath.Join(req.Dir, filename)
		m.purgeSameTarget(req.URL, path)
	}
	m.mu.Lock()
	id := m.nextID
	m.nextID++
	m.items = append(m.items, &Item{
		ID: id, URL: req.URL,
		Filename:     filepath.Base(path),
		Path:         path,
		Size:         req.Size,
		Status:       pick(req.Start, StatusQueued, StatusPaused),
		Category:     categoryFor(filename),
		AddedAt:      nowMillis(),
		Resumable:    true,
		Headers:      req.Headers,
		TorrentFiles: req.TorrentFiles,
		Overwrite:    req.Overwrite,
		Checksum:     req.Checksum,
		Mirrors:      req.Mirrors,
	})
	m.mu.Unlock()
	m.changed()
	m.schedule()
	return id
}

// purgeSameTarget 清理「重新下载」的目标路径:同路径的旧任务连本地文件
// 一起删;没有任务认领的残留(记录已清掉或外部放入的)与断点文件也一并
// 移除,让新任务从零开始。路径由调用方保证来自 dir + 净化过的文件名。
func (m *manager) purgeSameTarget(url, path string) {
	m.mu.Lock()
	var ids []uint64
	for _, it := range m.items {
		// 同一目标路径,或同一个链接:旧任务可能因当年的自动改名带着
		// 「 (1)」后缀,路径对不上,但链接是同一个,也算原任务。
		if it.Path == path || (url != "" && it.URL == url) {
			ids = append(ids, it.ID)
		}
	}
	m.mu.Unlock()
	if len(ids) > 0 {
		m.remove(ids, true)
	}
	os.Remove(statePath(path))
	os.Remove(partPath(path))
	os.RemoveAll(path)
}

// clearOverwrite 清掉「重新下载」的一次性标志:分片记录已按磁盘重校,
// 之后暂停/恢复按普通续传走,不再整盘重校验。
func (m *manager) clearOverwrite(id uint64) {
	m.mu.Lock()
	for _, it := range m.items {
		if it.ID == id {
			it.Overwrite = false
		}
	}
	m.mu.Unlock()
}

func (m *manager) pause(ids []uint64) {
	m.mu.Lock()
	for _, id := range ids {
		if r, ok := m.running[id]; ok {
			r.cancel() // 任务停止时自己标记为暂停
			continue
		}
		for _, it := range m.items {
			if it.ID == id && it.Status == StatusQueued {
				it.Status = StatusPaused
			}
		}
	}
	m.mu.Unlock()
	m.changed()
}

func (m *manager) resume(ids []uint64) {
	m.mu.Lock()
	for _, it := range m.items {
		if slices.Contains(ids, it.ID) && (it.Status == StatusPaused || it.Status == StatusError) {
			it.Status = StatusQueued
			it.Error = nil
		}
	}
	m.mu.Unlock()
	m.changed()
	m.schedule()
}

func (m *manager) remove(ids []uint64, deleteFiles bool) {
	m.mu.Lock()
	var removed []*Item
	kept := m.items[:0]
	for _, it := range m.items {
		if slices.Contains(ids, it.ID) {
			if r, ok := m.running[it.ID]; ok {
				r.cancel()
			}
			if deleteFiles && it.Status == StatusDownloading {
				m.deleteOnFinish[it.ID] = true
			}
			removed = append(removed, it)
		} else {
			kept = append(kept, it)
		}
	}
	m.items = kept
	m.mu.Unlock()
	for _, it := range removed {
		// 运行中的任务由其收尾逻辑清理临时文件。
		if it.Status != StatusDownloading {
			removeTempFiles(it.Path)
		}
		// 电驴任务:把引擎里的句柄和临时目录里的半份数据一并摘掉。
		if isED2KURL(it.URL) {
			ed2kForget(it.URL)
			if it.Status != StatusDownloading {
				os.RemoveAll(ed2kTempDir(m.dataDir, it.ID))
			}
		}
		// 勾了删本地文件:非运行中的(暂停/排队/失败/已完成)直接删,
		// 完整或半份文件都算「本地文件」;种子的数据是整个目录,用
		// RemoveAll 才删得掉;运行中的等收尾再删。
		if deleteFiles && it.Status != StatusDownloading {
			os.RemoveAll(it.Path)
		}
	}
	m.changed()
}

func (m *manager) setSettings(s Settings) {
	m.limiter.SetRate(int64(s.SpeedLimitKib) * 1024)
	m.mu.Lock()
	m.settings = s
	m.mu.Unlock()
	// 与上游一致:代理在服务重启后生效。
	m.save()
	m.changed()
	m.schedule()
}

// Metalink:一个文件多个镜像,可带校验和。返回文件名与镜像数。
func (m *manager) addMetalink(ctx context.Context, url string) (string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", 0, err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", 0, fmt.Errorf("服务器返回 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", 0, err
	}
	ml, err := parseMetalink(string(body))
	if err != nil {
		return "", 0, err
	}
	dir := m.defaultDir(categoryFor(ml.Name))
	m.add(AddRequest{
		URL: ml.URLs[0], Filename: ml.Name, Dir: dir, Start: true,
		Size: ml.Size, Checksum: ml.SHA256, Mirrors: ml.URLs[1:],
	})
	return ml.Name, len(ml.URLs), nil
}

// 把排队的任务提到队首。
func (m *manager) downloadNext(id uint64) bool {
	m.mu.Lock()
	pos := slices.IndexFunc(m.items, func(i *Item) bool { return i.ID == id && i.Status == StatusQueued })
	first := slices.IndexFunc(m.items, func(i *Item) bool { return i.Status == StatusQueued })
	moved := pos >= 0 && first >= 0
	if moved {
		it := m.items[pos]
		m.items = slices.Delete(m.items, pos, pos+1)
		m.items = append(m.items[:first], append([]*Item{it}, m.items[first:]...)...)
	}
	m.mu.Unlock()
	if moved {
		m.changed()
		m.save()
	}
	return moved
}

// 批量粘贴的链接:逐个探测真实文件名后直接开始。
func (m *manager) addLinks(text string) int {
	links := expandLinks(text)
	for _, u := range links {
		go func(u string) {
			// ed2k 链接不用探测:文件名和大小链接里就有。
			if isED2KURL(u) {
				name, size, err := parseED2KLink(u)
				if err != nil {
					return
				}
				dir := m.defaultDir(categoryFor(name))
				m.add(AddRequest{URL: u, Filename: name, Size: &size, Dir: dir, Start: true})
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			filename := "download.bin"
			if rf, err := Probe(ctx, m.client, u, nil); err == nil {
				filename = rf.Filename
			}
			dir := m.defaultDir(categoryFor(filename))
			m.add(AddRequest{URL: u, Filename: filename, Dir: dir, Start: true})
		}(u)
	}
	return len(links)
}

// 更换已停止任务的地址(链接过期后换新链接续传)。
func (m *manager) setURL(id uint64, url string) error {
	url = strings.TrimSpace(url)
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("这不是网页链接")
	}
	m.mu.Lock()
	var it *Item
	for _, cand := range m.items {
		if cand.ID == id {
			it = cand
			break
		}
	}
	if it == nil {
		m.mu.Unlock()
		return fmt.Errorf("找不到该任务")
	}
	if it.TorrentFiles != nil {
		m.mu.Unlock()
		return fmt.Errorf("BT 任务不能更换地址")
	}
	if isED2KURL(it.URL) {
		m.mu.Unlock()
		return fmt.Errorf("电驴任务不能更换地址")
	}
	if it.Status == StatusCompleted || it.Status == StatusDownloading {
		m.mu.Unlock()
		return fmt.Errorf("请先暂停任务")
	}
	it.URL = url
	it.Error = nil
	m.mu.Unlock()
	RebaseSaved(it.Path, url)
	m.resume([]uint64{id})
	return nil
}

// 把排队中的任务启动到 maxConcurrent 个。
func (m *manager) schedule() {
	type start struct {
		id       uint64
		ctx      context.Context
		req      *Request
		torrent  bool
		files    []int
		progress *Progress
		note     *Note
	}
	var toStart []*start
	m.mu.Lock()
	held := m.settings.StartAt > nowMillis()
	free := 0
	if !held {
		free = max(m.settings.MaxConcurrent, 1) - len(m.running)
	}
	if free > 0 {
		connections := m.settings.Connections
		for _, it := range m.items {
			if free <= 0 {
				break
			}
			if it.Status != StatusQueued {
				continue
			}
			it.Status = StatusDownloading
			free--
			ctx, cancel := context.WithCancel(context.Background())
			progress := &Progress{}
			progress.Downloaded.Store(it.Downloaded)
			if it.Size != nil {
				progress.Total.Store(*it.Size)
			}
			note := &Note{}
			r := &runningTask{path: it.Path, cancel: cancel, progress: progress, note: note}
			r.window = append(r.window, rateSample{at: time.Now(), rate: it.Downloaded})
			m.running[it.ID] = r
			st := &start{
				id: it.ID, ctx: ctx, progress: progress, note: note,
				req: &Request{URL: it.URL, Dest: it.Path, Connections: connections, Headers: headerMap(it.Headers), Mirrors: it.Mirrors, Overwrite: it.Overwrite},
			}
			if it.TorrentFiles != nil {
				st.torrent = true
				st.files = *it.TorrentFiles
			}
			toStart = append(toStart, st)
		}
	}
	m.mu.Unlock()
	if len(toStart) == 0 {
		return
	}
	for _, st := range toStart {
		go m.runTask(st.id, st.ctx, st.req, st.torrent, st.files, st.progress, st.note)
	}
	m.changed()
}

func (m *manager) runTask(id uint64, ctx context.Context, req *Request, isTorrent bool, files []int, progress *Progress, note *Note) {
	var (
		outcome Outcome
		err     error
	)
	if isTorrent {
		job := torrentJob{
			URL: req.URL, Headers: req.Headers, Dir: filepath.Dir(req.Dest), Files: files,
			Seed: m.settings_().SeedUntil, DataDir: m.dataDir, Overwrite: req.Overwrite,
			OnOverwriteApplied: func() { m.clearOverwrite(id) },
		}
		outcome, err = torrentRun(ctx, m.client, job, progress, note)
	} else if isED2KURL(req.URL) {
		job := ed2kJob{
			URL: req.URL, Dest: req.Dest, DataDir: m.dataDir,
			TempDir: ed2kTempDir(m.dataDir, id),
		}
		outcome, err = ed2kRun(ctx, job, progress, note)
	} else {
		outcome, err = run(ctx, m.client, req, progress, m.limiter)
	}
	if err == nil && outcome == OutcomeCompleted && m.needsVerify(id) {
		outcome, err = m.verify(id)
	}
	m.finish(id, outcome, err)
}

func (m *manager) needsVerify(id uint64) bool {
	it := m.item(id)
	return it != nil && it.Checksum != ""
}

// 完成后按用户给的 SHA-256 核对(如果有)。
func (m *manager) verify(id uint64) (Outcome, error) {
	it := m.item(id)
	if it == nil || it.Checksum == "" {
		return OutcomeCompleted, nil
	}
	note := func() *Note {
		m.mu.Lock()
		defer m.mu.Unlock()
		if r, ok := m.running[id]; ok {
			return r.note
		}
		return nil
	}()
	if note != nil {
		note.Set("正在校验文件")
		defer note.Set("")
	}
	f, err := os.Open(it.Path)
	if err != nil {
		return OutcomeCompleted, fmt.Errorf("下载完成,但读取文件校验失败: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return OutcomeCompleted, fmt.Errorf("下载完成,但读取文件校验失败: %w", err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	want := strings.ToLower(strings.TrimSpace(it.Checksum))
	if got != want {
		return OutcomeCompleted, fmt.Errorf("文件 SHA-256 不符:应为 %s,实为 %s。文件可能损坏,或不是预期的文件", want, got)
	}
	m.mu.Lock()
	for _, cand := range m.items {
		if cand.ID == id {
			cand.Verified = true
		}
	}
	m.mu.Unlock()
	return OutcomeCompleted, nil
}

func (m *manager) finish(id uint64, outcome Outcome, err error) {
	m.mu.Lock()
	running := m.running[id]
	delete(m.running, id)
	var it *Item
	for _, cand := range m.items {
		if cand.ID == id {
			it = cand
			break
		}
	}
	if it == nil {
		// 运行中被删除:清掉留下的临时文件;勾了删本地文件的连主文件一起删。
		delPath := ""
		if running != nil {
			removeTempFiles(running.path)
			if m.deleteOnFinish[id] {
				delPath = running.path
				delete(m.deleteOnFinish, id)
			}
		}
		// 电驴的半份数据在数据目录的临时目录里,任务没了就一并清掉。
		os.RemoveAll(ed2kTempDir(m.dataDir, id))
		m.mu.Unlock()
		// 种子的「文件」是整个数据目录,RemoveAll 才删得掉(与 remove() 的直删路径
		// 一致);挪到锁外删,不攥着锁做 IO。
		if delPath != "" {
			os.RemoveAll(delPath)
		}
		m.changed()
		return
	}
	if running != nil {
		it.Downloaded = running.progress.Downloaded.Load()
		if total := running.progress.Total.Load(); total > 0 {
			sz := total
			it.Size = &sz
		}
		it.Resumable = running.progress.Resumable.Load()
		it.Remaining = running.progress.RemainingRanges()
		it.Segments = running.progress.Spans()
	}
	it.Speed = 0
	it.Connections = 0
	it.Note = ""
	switch {
	case err == nil && outcome == OutcomeCompleted:
		it.Status = StatusCompleted
		t := nowMillis()
		it.CompletedAt = &t
		if it.Size == nil {
			sz := it.Downloaded
			it.Size = &sz
		}
		it.Downloaded = *it.Size
		it.Headers = nil
		it.Remaining = nil
	case err == nil && outcome == OutcomePaused:
		it.Status = StatusPaused
	default:
		it.Status = StatusError
		msg := fmt.Sprintf("%v", err)
		it.Error = &msg
	}
	m.mu.Unlock()
	m.changed()
	m.save()
	m.schedule()
}

// 到点后放开定时开始的队列。
func (m *manager) checkStartTime() {
	m.mu.Lock()
	due := m.settings.StartAt != 0 && nowMillis() >= m.settings.StartAt
	if due {
		m.settings.StartAt = 0
	}
	m.mu.Unlock()
	if due {
		m.save()
		m.changed()
		m.schedule()
	}
}

// 每 500ms:把实时进度搬进列表、算速度;有变化就自增版本号。
func (m *manager) tick() {
	m.mu.Lock()
	now := time.Now()
	changed := m.dirty
	m.dirty = false
	for id, r := range m.running {
		done := r.progress.Downloaded.Load()
		// 速度差分的信号源:HTTP 用已写入字节;BT 用网络层收包计数——
		// 已完成字节是整颗分片跳着长的,拿它逐拍差分算速度会在四十几/
		// 十几之间锯齿,而线上流量其实是稳的。
		rate := r.progress.Wire.Load()
		usingWire := rate > 0
		if !usingWire {
			rate = done
		}
		if usingWire != r.wire {
			// 信号源刚切换(BT 收到第一个数据包):旧窗口的基线作废。
			r.wire = usingWire
			r.window = r.window[:0]
		}
		r.window = append(r.window, rateSample{at: now, rate: rate})
		// 窗口最长 10s;计数回落(分片重核/重加句柄)时把高过当前值的锚点
		// 丢掉,避免负差分把速度卡在 0。
		for len(r.window) > 1 &&
			(now.Sub(r.window[1].at) > 10*time.Second || r.window[0].rate > rate) {
			r.window = r.window[1:]
		}
		if oldest := r.window[0]; now.Sub(oldest.at).Seconds() >= 1 {
			r.speed = max(float64(rate-oldest.rate), 0) / now.Sub(oldest.at).Seconds()
		}
		for _, it := range m.items {
			if it.ID != id {
				continue
			}
			it.Downloaded = done
			if total := r.progress.Total.Load(); total > 0 {
				sz := total
				it.Size = &sz
			}
			it.Speed = r.speed
			it.Connections = int(r.progress.Connections.Load())
			it.Remaining = r.progress.RemainingRanges()
			it.Segments = r.progress.Spans()
			it.Note = r.note.Get()
			break
		}
		changed = true
	}
	m.mu.Unlock()
	if changed {
		m.version.Add(1)
	}
}

func (m *manager) changed() {
	m.mu.Lock()
	m.dirty = true
	m.mu.Unlock()
	m.version.Add(1)
}

func (m *manager) save() {
	m.mu.Lock()
	items := make([]*Item, len(m.items))
	copy(items, m.items)
	s := m.settings
	m.mu.Unlock()
	_ = saveJSON(filepath.Join(m.dataDir, "downloads.json"), items)
	_ = saveJSON(filepath.Join(m.dataDir, "settings.json"), s)
}

func (m *manager) startTicker() {
	go func() {
		for range time.Tick(500 * time.Millisecond) {
			m.checkStartTime()
			m.tick()
		}
	}()
	m.schedule()
}

// ---- 供 api 层读取 ----

func (m *manager) stateJSON() map[string]any {
	return map[string]any{
		"items":    m.snapshot(),
		"settings": m.settings_(),
		"pending":  []any{},
		"version":  appVersion,
		"v":        m.version.Load(),
		// 本地开发(SWIFTDL_DEV_FOLDERS 回退)时前端隐藏目录选择弹窗:
		// 开发目录就一个,没有可挑的。token 只做存在性判断,不落盘不外传。
		"dev": os.Getenv("TRIM_API_TOKEN") == "" && strings.TrimSpace(os.Getenv("SWIFTDL_DEV_FOLDERS")) != "",
	}
}

// ---- 工具 ----

func categoryFor(filename string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
	switch ext {
	case "zip", "rar", "7z", "tar", "gz", "tgz", "xz", "bz2", "zst", "iso", "img":
		return "Compressed"
	case "pdf", "doc", "docx", "xls", "xlsx", "ppt", "pptx", "odt", "txt", "rtf", "epub", "csv", "md":
		return "Documents"
	case "mp3", "wav", "flac", "aac", "ogg", "m4a", "opus", "wma":
		return "Music"
	case "mp4", "mkv", "avi", "mov", "webm", "flv", "wmv", "m4v", "mpg", "mpeg", "3gp", "ts":
		return "Video"
	case "exe", "msi", "dmg", "pkg", "deb", "rpm", "apk", "appimage", "msix", "bat", "jar":
		return "Programs"
	case "jpg", "jpeg", "png", "gif", "webp", "svg", "bmp", "heic", "avif":
		return "Images"
	default:
		return "Other"
	}
}

func headerMap(headers [][2]string) http.Header {
	h := http.Header{}
	for _, kv := range headers {
		h.Add(kv[0], kv[1])
	}
	return h
}

func removeTempFiles(path string) {
	os.Remove(partPath(path))
	os.Remove(statePath(path))
}

func nowMillis() int64 { return time.Now().UnixMilli() }

func pick[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
}

func loadJSONFile(path string, into any) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, into)
}

func saveJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// loadJSON 的设置默认值。
func defaultSettings() Settings {
	return Settings{
		Connections:        8,
		MaxConcurrent:      3,
		SortIntoCategories: true,
		Theme:              "system",
		SeedUntil:          "stop",
		UIScale:            100,
	}
}
