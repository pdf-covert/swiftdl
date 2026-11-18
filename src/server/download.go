// 下载引擎:N 个连接并行,每个分片直写最终位置;连接空闲时偷走剩余最大
// 分片的后半段,直到最后一字节都在干活。上游 crates/engine/src/download.rs
// 与 segment.rs 的 Go 移植。

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// 再拆分的分片不得小于此值的一半:新连接的握手开销在 ~512KiB 以下不划算。
const minSplitHalf = 512 * 1024
const maxRetries = 8

type Outcome int

const (
	OutcomeCompleted Outcome = iota
	// 被取消(暂停)。服务器支持断点时进度已保存,否则下次从头开始。
	OutcomePaused
)

func (o Outcome) String() string {
	if o == OutcomeCompleted {
		return "completed"
	}
	return "paused"
}

type Request struct {
	URL         string
	Dest        string // 最终路径;<Dest>.part 与 <Dest>.swiftdl 在它旁边,直到完成
	Connections int
	Headers     http.Header
	// 无视已有进度,从头开始。
	Restart bool
	// 重新下载(经同名确认框):BT 侧用来重校分片完成记录;HTTP 引擎
	// 忽略(添加时旧文件已由 purgeSameTarget 清掉,自然从头开始)。
	Overwrite bool
	// 同一文件的其他地址:逐个校验(同大小、允许分段),通过的共享连接,
	// 某个服务器失败时连接换下一个。
	Mirrors []string
}

// 前端可随时读取的实时数字(原子量)。
type Progress struct {
	Total       atomic.Int64
	Downloaded  atomic.Int64
	Connections atomic.Int64
	Resumable   atomic.Bool

	// 网络层收包计数(仅 BT 任务写):已完成字节是整颗分片跳着长的,
	// 拿它差分算速度会锯齿(四十几/十几来回跳);引擎的收包计数按 16KB
	// 块平滑递增,速度显示用它才稳。HTTP 任务不写,保持 0。
	Wire atomic.Int64

	mu        sync.Mutex
	remaining [][2]int64
	segments  [][3]int64
}

func (p *Progress) RemainingRanges() [][2]int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([][2]int64, len(p.remaining))
	copy(out, p.remaining)
	return out
}

func (p *Progress) Spans() [][3]int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([][3]int64, len(p.segments))
	copy(out, p.segments)
	return out
}

// 一个分片是字节区间 [pos, end)。pos 随写入前移;空闲连接偷走剩余后半段时
// end 后移(动态分段)。一把锁管住写与分割,双方不会争同一个字节。
type segment struct {
	start int64
	mu    sync.Mutex
	pos   int64
	end   int64
}

func newSegment(pos, end int64) *segment { return &segment{start: pos, pos: pos, end: end} }

func (s *segment) range_() (int64, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pos, s.end
}

// (start, pos, end):从 start 开始,已写到 pos,拥有到 end。
func (s *segment) span() (int64, int64, int64) {
	pos, end := s.range_()
	return s.start, pos, end
}

func (s *segment) remaining() int64 {
	pos, end := s.range_()
	return max(end-pos, 0)
}

// 把 data 中仍属于本分片的部分写到文件最终偏移处。分片完成(可能因尾部
// 被偷走而提前)时返回 false,调用方应挂断连接。
func (s *segment) write(f *os.File, data []byte) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pos >= s.end {
		return false, nil
	}
	n := int64(min(len(data), int(s.end-s.pos)))
	if _, err := f.WriteAt(data[:n], s.pos); err != nil {
		return false, err
	}
	s.pos += n
	return s.pos < s.end, nil
}

// 让出剩余的后半段并作为新分片返回;剩余太小、不值得开新连接时返回 nil。
func (s *segment) split(minHalf int64) *segment {
	s.mu.Lock()
	defer s.mu.Unlock()
	rem := s.end - s.pos
	if s.pos >= s.end || rem < minHalf*2 {
		return nil
	}
	mid := s.pos + rem/2
	tail := newSegment(mid, s.end)
	s.end = mid
	return tail
}

// 把 [0, size) 均分为 n 段。
func initialSegments(size int64, n int) [][2]int64 {
	n = int(min(int64(max(n, 1)), max(size, 1)))
	chunk := size / int64(n)
	out := make([][2]int64, n)
	for i := range out {
		start := int64(i) * chunk
		end := start + chunk
		if i == n-1 {
			end = size
		}
		out[i] = [2]int64{start, end}
	}
	return out
}

type table struct {
	mu   sync.Mutex
	segs []*segment
	// 此下标之前的分片都已交给过工人。
	next int
}

// 工人来领活:有没领过的分片就领;否则偷剩余最多的分片的后半段。
func (t *table) claim() *segment {
	t.mu.Lock()
	defer t.mu.Unlock()
	for t.next < len(t.segs) {
		seg := t.segs[t.next]
		t.next++
		if seg.remaining() > 0 {
			return seg
		}
	}
	var victim *segment
	for _, s := range t.segs {
		if victim == nil || s.remaining() > victim.remaining() {
			victim = s
		}
	}
	if victim == nil {
		return nil
	}
	stolen := victim.split(minSplitHalf)
	if stolen == nil {
		return nil
	}
	t.segs = append(t.segs, stolen)
	t.next = len(t.segs)
	return stolen
}

func (t *table) remainingRanges() [][2]int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out [][2]int64
	for _, s := range t.segs {
		pos, end := s.range_()
		if pos < end {
			out = append(out, [2]int64{pos, end})
		}
	}
	return out
}

func (t *table) remainingBytes() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	var sum int64
	for _, s := range t.segs {
		sum += s.remaining()
	}
	return sum
}

func (t *table) spans() [][3]int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([][3]int64, len(t.segs))
	for i, s := range t.segs {
		a, b, c := s.span()
		out[i] = [3]int64{a, b, c}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

type shared struct {
	client   *http.Client
	urls     []string // 原地址,加上校验通过的镜像
	headers  http.Header
	file     *os.File
	table    *table
	progress *Progress
	limiter  *SpeedLimiter
	size     int64
}

func (sh *shared) publish() {
	rem := sh.table.remainingBytes()
	sh.progress.Downloaded.Store(sh.size - rem)
	sh.progress.mu.Lock()
	sh.progress.remaining = sh.table.remainingRanges()
	sh.progress.segments = sh.table.spans()
	sh.progress.mu.Unlock()
}

// 新建 HTTP 客户端:强制 HTTP/1.1(否则所有 Range 会复用一条 HTTP/2 连接、
// 共享一个拥塞窗口,多连接就没有意义),可带代理。注意 Go 里干净的
// Transport 即使 ForceAttemptHTTP2=false 也会自动启用 h2,必须放一个
// 非空的空 TLSNextProto 才真正挡住自动升级。
func buildClient(proxy string) (*http.Client, error) {
	tr := &http.Transport{
		ForceAttemptHTTP2: false,
		// 空 map 而非 nil:阻止标准库给这条 Transport 自动配 HTTP/2。
		TLSNextProto:          map[string]func(authority string, c *tls.Conn) http.RoundTripper{},
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	if proxy != "" {
		if u, err := url.Parse(proxy); err == nil {
			tr.Proxy = http.ProxyURL(u)
		}
	}
	return &http.Client{Transport: tr}, nil
}

// 下载 req 直到完成、失败或 ctx 取消。
func run(ctx context.Context, client *http.Client, req *Request, progress *Progress, limiter *SpeedLimiter) (Outcome, error) {
	remote, err := Probe(ctx, client, req.URL, req.Headers)
	if err != nil {
		return OutcomePaused, err
	}
	if dir := filepath.Dir(req.Dest); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return OutcomePaused, fmt.Errorf("无法创建目录 %s: %w", dir, err)
		}
	}
	progress.Total.Store(remote.Size)
	progress.Resumable.Store(remote.Resumable)

	part := partPath(req.Dest)
	statePath := statePath(req.Dest)
	var outcome Outcome
	if remote.Resumable && remote.Size > 0 {
		outcome, err = downloadSegmented(ctx, client, req, remote, progress, limiter, part, statePath)
	} else {
		outcome, err = downloadSingle(ctx, client, req, remote, progress, limiter, part)
	}
	if err != nil {
		return outcome, err
	}
	if outcome == OutcomeCompleted {
		os.Remove(statePath)
		if _, statErr := os.Stat(req.Dest); statErr == nil {
			os.Remove(req.Dest)
		}
		if err := os.Rename(part, req.Dest); err != nil {
			return OutcomePaused, fmt.Errorf("无法改名为 %s: %w", req.Dest, err)
		}
	}
	return outcome, nil
}

func downloadSegmented(ctx context.Context, client *http.Client, req *Request, remote *RemoteFile, progress *Progress, limiter *SpeedLimiter, part, statePath string) (Outcome, error) {
	var saved *savedState
	if !req.Restart {
		if _, statErr := os.Stat(part); statErr == nil {
			if s := loadSavedState(statePath); s != nil && s.matches(req.URL, remote.Size, remote.Etag, remote.LastModified) {
				saved = s
			}
		}
	}

	f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return OutcomePaused, err
	}
	defer f.Close()

	var ranges [][2]int64
	if saved != nil {
		ranges = saved.Segments
	} else {
		// 预留整个大小,分片可以写到任何位置,收尾不需要拼接。
		if err := f.Truncate(remote.Size); err != nil {
			return OutcomePaused, fmt.Errorf("磁盘空间不足? %w", err)
		}
		ranges = initialSegments(remote.Size, req.Connections)
	}

	urls := []string{remote.URL}
	urls = append(urls, usableMirrors(ctx, client, req, remote.Size)...)
	tb := &table{}
	for _, r := range ranges {
		tb.segs = append(tb.segs, newSegment(r[0], r[1]))
	}
	sh := &shared{client: client, urls: urls, headers: req.Headers, file: f, table: tb, progress: progress, limiter: limiter, size: remote.Size}
	sh.publish()

	meta := savedState{URL: req.URL, Size: remote.Size, Etag: remote.Etag, LastModified: remote.LastModified}
	save := func() error {
		s := meta
		s.Segments = tb.remainingRanges()
		return s.save(statePath)
	}

	// 每 250ms 发布进度;每 1s 落一次断点。
	bgCtx, bgCancel := context.WithCancel(ctx)
	defer bgCancel()
	bgDone := make(chan struct{})
	go func() {
		defer close(bgDone)
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for n := 0; ; n++ {
			select {
			case <-bgCtx.Done():
				return
			case <-tick.C:
				sh.publish()
				if n%4 == 0 {
					_ = save()
				}
			}
		}
	}()

	var wg sync.WaitGroup
	errCh := make(chan error, req.Connections)
	for id := 0; id < max(req.Connections, 1); id++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			errCh <- worker(ctx, sh, id)
		}(id)
	}
	wg.Wait()
	close(errCh)
	var firstErr error
	for e := range errCh {
		if e != nil && firstErr == nil {
			firstErr = e
		}
	}

	bgCancel()
	<-bgDone
	progress.Connections.Store(0)
	sh.publish()

	if ctx.Err() != nil || errors.Is(firstErr, errCanceled) {
		_ = save()
		return OutcomePaused, nil
	}
	if firstErr != nil {
		_ = save()
		return OutcomePaused, firstErr
	}
	if tb.remainingBytes() == 0 {
		if err := f.Sync(); err != nil {
			return OutcomePaused, err
		}
		return OutcomeCompleted, nil
	}
	return OutcomePaused, errors.New("连接全部结束但数据仍缺(这是 bug)")
}

var errCanceled = errors.New("已取消")

// 真正持有该文件、且大小一致的镜像。
func usableMirrors(ctx context.Context, client *http.Client, req *Request, size int64) []string {
	var out []string
	for _, m := range req.Mirrors {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		r, err := Probe(c, client, m, req.Headers)
		cancel()
		if err == nil && r.Resumable && r.Size == size {
			out = append(out, r.URL)
		}
	}
	return out
}

// 一个工人的一生:领一个分片下完,再领(或偷别人的),直到没有值得领的。
func worker(ctx context.Context, sh *shared, id int) error {
	for {
		seg := sh.table.claim()
		if seg == nil {
			return nil
		}
		sh.progress.Connections.Add(1)
		err := downloadSegment(ctx, sh, seg, id)
		sh.progress.Connections.Add(-1)
		if err != nil {
			return err
		}
	}
}

// 下载一个分片,出错从断点重连。只要有一次尝试取得进展,重试计数就归零:
// 只有真正卡死的连接才放弃。
func downloadSegment(ctx context.Context, sh *shared, seg *segment, id int) error {
	failures := 0
	for {
		pos, end := seg.range_()
		if pos >= end {
			return nil
		}
		// 每个连接轮流用不同服务器;失败一次换下一个。
		u := sh.urls[(id+failures)%len(sh.urls)]
		err := fetchRange(ctx, sh, u, seg, pos, end)
		newPos, _ := seg.range_()
		progressed := newPos > pos
		if progressed {
			failures = 0
		}
		if err == nil && progressed {
			continue
		}
		if err == nil && !progressed {
			failures++
			if failures > maxRetries {
				return fmt.Errorf("服务器一直在第 %d 字节处断开连接", pos)
			}
		} else if err != nil {
			if errors.Is(err, errCanceled) || errors.Is(err, context.Canceled) {
				return errCanceled
			}
			failures++
			if failures > maxRetries {
				return err
			}
		}
		backoff := 250 * time.Millisecond << uint(min(failures, 5))
		select {
		case <-ctx.Done():
			return errCanceled
		case <-time.After(backoff):
		}
	}
}

func fetchRange(ctx context.Context, sh *shared, u string, seg *segment, pos, end int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	for k, vs := range sh.headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", pos, end-1))
	resp, err := sh.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("分段请求应答 206,得到 %s", resp.Status)
	}
	buf := make([]byte, 64*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			sh.limiter.Take(n)
			more, werr := seg.write(sh.file, buf[:n])
			if werr != nil {
				return werr
			}
			if !more {
				// 分片完成(或尾部被偷走):直接返回,defer 的 Body.Close()
				// 会关闭未读完的连接,相当于挂断。
				return nil
			}
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			if errors.Is(rerr, context.Canceled) {
				return errCanceled
			}
			return rerr
		}
	}
}

// 不支持 Range 的服务器:单条顺序流。暂停会丢进度,因为服务器无法续传。
func downloadSingle(ctx context.Context, client *http.Client, dlReq *Request, remote *RemoteFile, progress *Progress, limiter *SpeedLimiter, part string) (Outcome, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, remote.URL, nil)
	if err != nil {
		return OutcomePaused, err
	}
	for k, vs := range dlReq.Headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return OutcomePaused, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return OutcomePaused, fmt.Errorf("服务器返回 %s", resp.Status)
	}
	f, err := os.Create(part)
	if err != nil {
		return OutcomePaused, err
	}
	defer f.Close()
	progress.Downloaded.Store(0)
	progress.Connections.Store(1)
	defer progress.Connections.Store(0)
	buf := make([]byte, 64*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			limiter.Take(n)
			if _, werr := f.Write(buf[:n]); werr != nil {
				return OutcomePaused, werr
			}
			progress.Downloaded.Add(int64(n))
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			if errors.Is(rerr, context.Canceled) {
				return OutcomePaused, nil
			}
			return OutcomePaused, rerr
		}
	}
	if err := f.Sync(); err != nil {
		return OutcomePaused, err
	}
	return OutcomeCompleted, nil
}

// 为 filename 在 dir 里挑一个空位:已有同名文件时追加 " (1)"、" (2)"…
// 而不是覆盖;旁边有未完成的 .part 时直接复用,以便续传。
func ResolveDest(dir, filename string) string {
	path := filepath.Join(dir, filename)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return path
	}
	if _, err := os.Stat(partPath(path)); err == nil {
		return path
	}
	ext := filepath.Ext(path)
	stem := path[:len(path)-len(ext)]
	for i := 1; ; i++ {
		cand := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		if _, err := os.Stat(cand); errors.Is(err, os.ErrNotExist) {
			if _, err := os.Stat(partPath(cand)); errors.Is(err, os.ErrNotExist) {
				return cand
			}
		}
	}
}

func partPath(dest string) string  { return dest + ".part" }
func statePath(dest string) string { return dest + ".swiftdl" }

// 断点文件:<Dest>.swiftdl,记录仍缺的字节区间。崩溃、断电、断网最多
// 损失最后一秒的进度。
type savedState struct {
	URL          string     `json:"url"`
	Size         int64      `json:"size"`
	Etag         string     `json:"etag,omitempty"`
	LastModified string     `json:"lastModified,omitempty"`
	Segments     [][2]int64 `json:"segments"`
}

// 先写临时文件再改名,状态文件永远不会写一半。
func (s *savedState) save(path string) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func loadSavedState(path string) *savedState {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var s savedState
	if json.Unmarshal(data, &s) != nil {
		return nil
	}
	return &s
}

// 该状态描述的是否为同一个远端文件版本。
func (s *savedState) matches(url string, size int64, etag, lastModified string) bool {
	if s.URL != url || s.Size != size {
		return false
	}
	if s.Etag != "" && etag != "" && s.Etag != etag {
		return false
	}
	if s.LastModified != "" && lastModified != "" && s.LastModified != lastModified {
		return false
	}
	return true
}

// 已暂停任务的缺失区间,从断点文件读。
func SavedRemaining(dest string) [][2]int64 {
	if s := loadSavedState(statePath(dest)); s != nil {
		return s.Segments
	}
	return nil
}

// 把已暂停任务的断点指向同一文件的新地址(链接过期被换新)。
// 恢复时仍会校验大小与服务器验证器,换了文件则从头开始。
func RebaseSaved(dest, newURL string) bool {
	path := statePath(dest)
	s := loadSavedState(path)
	if s == nil {
		return false
	}
	s.URL = newURL
	return s.save(path) == nil
}
