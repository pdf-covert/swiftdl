// 引擎测试:对照上游 crates/engine 的测试语义,用 httptest 服务器验证
// 分段下载逐字节一致、断点续传、无 Range 回退、分片分割与断点状态文件。

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// rangeServer 提供一个支持 Range 的文件服务器(206 + Content-Range),
// 统计各 URL 被实际取数据的次数,支持制造中途失败。
type rangeServer struct {
	data  []byte
	hits  atomic.Int64
	failN atomic.Int64 // 每次请求若 failN>0 则失败(探测除外)
	name  string
}

func newRangeServer(t *testing.T, data []byte, name string) (*rangeServer, *httptest.Server) {
	t.Helper()
	rs := &rangeServer{data: data, name: name}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs.hits.Add(1)
		if rs.failN.Load() > 0 && r.Header.Get("Range") != "bytes=0-0" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
		if r.Header.Get("Range") == "bytes=0-0" {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-0/%d", len(data)))
			w.WriteHeader(http.StatusPartialContent)
			w.Write([]byte{data[0]})
			return
		}
		// 无 Range:整文件
		if r.Header.Get("Range") == "" {
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			w.WriteHeader(http.StatusOK)
			w.Write(data)
			return
		}
		var a, b int
		fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &a, &b)
		if a >= len(data) {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		end := min(b, len(data)-1)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", a, end, len(data)))
		w.Header().Set("Content-Length", fmt.Sprint(end-a+1))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(data[a : end+1])
	}))
	t.Cleanup(srv.Close)
	return rs, srv
}

func TestInitialSplitCoversFile(t *testing.T) {
	segs := initialSegments(1003, 4)
	if segs[0][0] != 0 || segs[len(segs)-1][1] != 1003 {
		t.Fatalf("区间不覆盖整个文件: %v", segs)
	}
	for i := 1; i < len(segs); i++ {
		if segs[i-1][1] != segs[i][0] {
			t.Fatalf("区间不衔接: %v", segs)
		}
	}
	if got := len(initialSegments(3, 8)); got != 3 {
		t.Fatalf("比文件还多的分段应被钳制,得 %d", got)
	}
}

func TestSplitHalvesRemaining(t *testing.T) {
	s := newSegment(100, 300)
	tail := s.split(10)
	if pos, end := s.range_(); pos != 100 || end != 200 {
		t.Fatalf("剩余的前半应留下: %d-%d", pos, end)
	}
	if pos, end := tail.range_(); pos != 200 || end != 300 {
		t.Fatalf("后半应成为新分片: %d-%d", pos, end)
	}
	if newSegment(0, 15).split(10) != nil {
		t.Fatal("剩余太小不应拆分")
	}
}

func TestSegmentedDownloadIsBytePerfect(t *testing.T) {
	data := make([]byte, 3_000_000)
	rand.Read(data)
	_, srv := newRangeServer(t, data, "big.bin")

	dest := filepath.Join(t.TempDir(), "big.bin")
	req := &Request{URL: srv.URL + "/big.bin", Dest: dest, Connections: 8}
	client, _ := buildClient("")
	progress := &Progress{}
	outcome, err := run(context.Background(), client, req, progress, NewSpeedLimiter(0))
	if err != nil || outcome != OutcomeCompleted {
		t.Fatalf("应完成: %v %v", outcome, err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, data) {
		t.Fatalf("文件应逐字节一致 (got %d bytes)", len(got))
	}
	if progress.Total.Load() != int64(len(data)) || !progress.Resumable.Load() {
		t.Fatalf("进度头不对: total=%d resumable=%v", progress.Total.Load(), progress.Resumable.Load())
	}
	if _, err := os.Stat(dest + ".swiftdl"); !os.IsNotExist(err) {
		t.Fatal("完成后断点文件应删除")
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatal("完成后 .part 应改名为目标文件")
	}
}

func TestResumeAfterCancelContinues(t *testing.T) {
	data := make([]byte, 4_000_000)
	rand.Read(data)
	_, srv := newRangeServer(t, data, "iso.bin")

	dest := filepath.Join(t.TempDir(), "iso.bin")
	client, _ := buildClient("")
	req := &Request{URL: srv.URL + "/iso.bin", Dest: dest, Connections: 4}

	// 用限速把下载拖到 ~2s,中途取消,确保留有部分进度。
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(600 * time.Millisecond)
		cancel()
	}()
	outcome, err := run(ctx, client, req, &Progress{}, NewSpeedLimiter(2_000_000))
	if err != nil || outcome != OutcomePaused {
		t.Fatalf("取消应返回暂停: %v %v", outcome, err)
	}
	if _, err := os.Stat(dest + ".swiftdl"); err != nil {
		t.Fatal("暂停后应留有断点文件")
	}
	if fi, err := os.Stat(partPath(dest)); err != nil || fi.Size() != int64(len(data)) {
		t.Fatalf(".part 应已预留全大小: %v %v", fi, err)
	}

	// 重新运行:按断点文件只补缺的部分。
	outcome, err = run(context.Background(), client, req, &Progress{}, NewSpeedLimiter(0))
	if err != nil || outcome != OutcomeCompleted {
		t.Fatalf("续传应完成: %v %v", outcome, err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, data) {
		t.Fatal("续传结果应逐字节一致")
	}
}

func TestSingleStreamWhenNoRanges(t *testing.T) {
	data := []byte("no range support here, just one stream of bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="plain.txt"`)
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		w.WriteHeader(http.StatusOK) // 忽略 Range
		w.Write(data)
	}))
	t.Cleanup(srv.Close)

	dest := filepath.Join(t.TempDir(), "plain.txt")
	client, _ := buildClient("")
	outcome, err := run(context.Background(), client, &Request{URL: srv.URL, Dest: dest, Connections: 4}, &Progress{}, NewSpeedLimiter(0))
	if err != nil || outcome != OutcomeCompleted {
		t.Fatalf("单流应完成: %v %v", outcome, err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, data) {
		t.Fatal("单流结果应一致")
	}
}

func TestMirrorsShareAndWrongSizeIsSkipped(t *testing.T) {
	data := make([]byte, 1_500_000)
	rand.Read(data)
	good1, s1 := newRangeServer(t, data, "a.bin")
	good2, s2 := newRangeServer(t, data, "a.bin")
	// 大小撒谎的镜像:探测可通过吗?不会——它声称的 Content-Range 总长不同。
	wrong := &rangeServer{data: data}
	wrongSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-0/%d", len(data)+5))
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte{data[0]})
	}))
	t.Cleanup(func() { s1.Close(); s2.Close(); wrongSrv.Close() })
	_ = wrong

	dest := filepath.Join(t.TempDir(), "a.bin")
	client, _ := buildClient("")
	req := &Request{
		URL: s1.URL + "/a.bin", Dest: dest, Connections: 4,
		Mirrors: []string{wrongSrv.URL, s2.URL + "/a.bin"},
	}
	outcome, err := run(context.Background(), client, req, &Progress{}, NewSpeedLimiter(0))
	if err != nil || outcome != OutcomeCompleted {
		t.Fatalf("镜像应共同完成: %v %v", outcome, err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, data) {
		t.Fatal("镜像结果应逐字节一致")
	}
	if good1.hits.Load() <= 1 || good2.hits.Load() <= 1 {
		t.Fatalf("两个好镜像都应承载数据: %d / %d", good1.hits.Load(), good2.hits.Load())
	}
}

func TestProbeParsesNameAndSize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-0/123456")
		w.Header().Set("ETag", `"abc"`)
		w.Header().Set("Last-Modified", "Wed, 21 Oct 2026 07:28:00 GMT")
		w.Header().Set("Content-Disposition", "attachment; filename=x.bin; filename*=UTF-8''r%C3%A9sum%C3%A9.pdf")
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte("x"))
	}))
	t.Cleanup(srv.Close)
	client, _ := buildClient("")
	rf, err := Probe(context.Background(), client, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rf.Filename != "résumé.pdf" || rf.Size != 123456 || !rf.Resumable || rf.Etag != `"abc"` {
		t.Fatalf("探测结果不对: %+v", rf)
	}
}

func TestProbeRejectsHTMLPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte("<html>sign in</html>"))
	}))
	t.Cleanup(srv.Close)
	client, _ := buildClient("")
	if _, err := Probe(context.Background(), client, srv.URL, nil); err == nil {
		t.Fatal("网页应报错而不是存成文件")
	}
}

func TestResolveDestAvoidsCollision(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.zip"), []byte("old"), 0o644)
	// 已有同名文件且无 .part:让位加序号。
	if got := ResolveDest(dir, "a.zip"); got != filepath.Join(dir, "a (1).zip") {
		t.Fatalf("重名应加序号: %s", got)
	}
	// 旁边有未完成的 .part:复用原路径以便续传。
	os.WriteFile(partPath(filepath.Join(dir, "a.zip")), []byte("p"), 0o644)
	if got := ResolveDest(dir, "a.zip"); got != filepath.Join(dir, "a.zip") {
		t.Fatalf("有 .part 应复用以便续传: %s", got)
	}
}

func TestSavedStateRoundTrip(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "f.bin")
	s := &savedState{URL: "https://x/f?token=old", Size: 100, Etag: "e", Segments: [][2]int64{{40, 100}}}
	if err := s.save(statePath(dest)); err != nil {
		t.Fatal(err)
	}
	if s.matches("https://x/f?token=new", 100, "e", "") {
		t.Fatal("换了地址应从头开始(先 rebase 才能续传)")
	}
	if !RebaseSaved(dest, "https://x/f?token=new") {
		t.Fatal("rebase 应成功")
	}
	got := loadSavedState(statePath(dest))
	if got == nil || got.URL != "https://x/f?token=new" || len(got.Segments) != 1 {
		t.Fatalf("rebase 后状态不对: %+v", got)
	}
	if !got.matches("https://x/f?token=new", 100, "e", "") {
		t.Fatal("rebase 后同大小同 etag 应可续传")
	}
	if got.matches("https://x/f?token=new", 101, "e", "") {
		t.Fatal("大小变了应从头开始")
	}
	if len(SavedRemaining(dest)) != 1 {
		t.Fatal("SavedRemaining 应读到缺失区间")
	}
}

func TestLimiterUnlimitedIsFree(t *testing.T) {
	l := NewSpeedLimiter(0)
	start := time.Now()
	l.Take(1 << 20)
	if time.Since(start) > 50*time.Millisecond {
		t.Fatal("不限速时不应等待")
	}
}

// 并行分片写最终文件,确认不串位。
func TestParallelSegmentWrites(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "par")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	data := make([]byte, 1_000_000)
	rand.Read(data)
	f.Truncate(int64(len(data)))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			seg := newSegment(int64(i)*125_000, int64(i+1)*125_000)
			for buf := data[i*125_000 : (i+1)*125_000]; len(buf) > 0; {
				n := min(len(buf), 32*1024)
				if _, err := seg.write(f, buf[:n]); err != nil {
					t.Errorf("写入失败: %v", err)
					return
				}
				buf = buf[n:]
			}
		}(i)
	}
	wg.Wait()
	got, _ := io.ReadAll(io.NewSectionReader(f, 0, int64(len(data))))
	if !bytes.Equal(got, data) {
		t.Fatal("并行分段写入应逐字节一致")
	}
}

// 原版引擎 http1_only():强制每条 Range 走独立 TCP 连接,否则所有分段会
// 复用一条 HTTP/2 连接、共享一个拥塞窗口。对着支持 h2 的本地 TLS 服务验证
// 我们的客户端拿到的仍是 HTTP/1.1;先确认对照客户端真能协商出 h2,
// 否则本测试分辨不出好坏,跳过。
func TestBuildClientForcesHTTP11(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-2/3")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("abc"))
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	trusted, _ := srv.Client().Transport.(*http.Transport).TLSClientConfig, true
	if cfg := srv.Client().Transport.(*http.Transport); cfg != nil {
		if tc := cfg.TLSClientConfig; tc != nil {
			trusted = tc
		}
	}
	if trusted == nil {
		t.Skip("拿不到测试服务的信任证书配置")
	}

	ctrl := &http.Client{Transport: &http.Transport{ForceAttemptHTTP2: true, TLSClientConfig: trusted.Clone()}}
	cr, err := ctrl.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	cr.Body.Close()
	if cr.Proto != "HTTP/2.0" {
		t.Skipf("对照客户端协商到 %s,环境不支持 h2,测不出差异", cr.Proto)
	}

	cl, err := buildClient("")
	if err != nil {
		t.Fatal(err)
	}
	tr := cl.Transport.(*http.Transport).Clone()
	tr.TLSClientConfig = trusted.Clone()
	resp, err := (&http.Client{Transport: tr}).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Proto != "HTTP/1.1" {
		t.Fatalf("应为 HTTP/1.1,得到 %s:HTTP/2 没关掉,分段下载会共用一条连接", resp.Proto)
	}
}
