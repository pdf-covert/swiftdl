// manager/api 层测试:任务从添加到完成的整条链路、暂停恢复、
// 轮询版本号,以及 BT 相关的纯逻辑。

package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestManager(t *testing.T) (*manager, string) {
	t.Helper()
	data := t.TempDir()
	downloads := filepath.Join(data, "downloads")
	if err := os.MkdirAll(downloads, 0o755); err != nil {
		t.Fatal(err)
	}
	m, err := newManager(data, downloads)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.save() })
	return m, downloads
}

func waitStatus(t *testing.T, m *manager, id uint64, want Status) *Item {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if it := m.item(id); it != nil && it.Status == want {
			return it
		}
		time.Sleep(50 * time.Millisecond)
	}
	it := m.item(id)
	if it == nil {
		t.Fatalf("任务 %d 不见了", id)
	}
	t.Fatalf("等 %s 超时,现在 %s (err=%v)", want, it.Status, it.Error)
	return nil
}

func TestManagerRunsDownloadToCompletion(t *testing.T) {
	data := make([]byte, 500_000)
	rand.Read(data)
	name := "managed.bin"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
		if r.Header.Get("Range") == "bytes=0-0" {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-0/%d", len(data)))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(data[:1])
			return
		}
		var a int
		fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-", &a)
		if a >= len(data) {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", a, len(data)-1, len(data)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(data[a:])
	}))
	t.Cleanup(srv.Close)

	m, downloads := newTestManager(t)
	m.startTicker()
	id := m.add(AddRequest{URL: srv.URL + "/f.bin", Filename: name, Dir: downloads, Start: true})
	it := waitStatus(t, m, id, StatusCompleted)
	if it.Size == nil || *it.Size != int64(len(data)) {
		t.Fatalf("完成后应有大小: %+v", it)
	}
	if it.Downloaded != int64(len(data)) || it.Headers != nil || it.Remaining != nil {
		t.Fatalf("完成态不对: %+v", it)
	}
	got, err := os.ReadFile(filepath.Join(downloads, name))
	if err != nil || string(got) != string(data) {
		t.Fatalf("文件应完好: %v", err)
	}
	// 持久化:重开一个 manager 还能看到它。
	m.save()
	m2, err := newManager(filepath.Dir(downloads), downloads)
	if err != nil {
		t.Fatal(err)
	}
	if it2 := m2.item(id); it2 == nil || it2.Status != StatusCompleted {
		t.Fatalf("重载后任务应还在且已完成: %+v", it2)
	}
}

func TestManagerPauseAndResume(t *testing.T) {
	// 一个一直回 500 的本地服务器:任务会报错而不是成功。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	m, downloads := newTestManager(t)
	m.startTicker()
	// Start:false 直接进暂停态,不启动。
	id := m.add(AddRequest{URL: srv.URL + "/x.bin", Filename: "x.bin", Dir: downloads, Start: false})
	if it := m.item(id); it.Status != StatusPaused {
		t.Fatalf("不自动开始应为暂停: %s", it.Status)
	}
	m.pause([]uint64{id}) // 已暂停,幂等
	m.resume([]uint64{id})
	it := waitStatus(t, m, id, StatusError)
	if it.Error == nil || *it.Error == "" {
		t.Fatal("服务器 500 应报错")
	}
	m.resume([]uint64{id}) // 出错可重试
	if it := m.item(id); it.Status != StatusQueued && it.Status != StatusDownloading {
		t.Fatalf("恢复后应重新排队: %s", it.Status)
	}
}

func TestManagerRemoveCleansUp(t *testing.T) {
	m, downloads := newTestManager(t)
	id := m.add(AddRequest{URL: "https://example.com/y.bin", Filename: "y.bin", Dir: downloads, Start: false})
	path := m.item(id).Path
	os.WriteFile(partPath(path), []byte("partial"), 0o644)
	os.WriteFile(statePath(path), []byte("{}"), 0o644)
	os.WriteFile(path, []byte("half"), 0o644)
	m.remove([]uint64{id}, false)
	if _, err := os.Stat(partPath(path)); !os.IsNotExist(err) {
		t.Fatal("删除任务应清掉 .part")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("没勾删除文件,半份文件应保留")
	}
	if m.item(id) != nil {
		t.Fatal("任务应已删除")
	}

	// 勾了「同时删除本地文件」:暂停/排队等非运行中任务的本地文件也删掉。
	id2 := m.add(AddRequest{URL: "https://example.com/z.bin", Filename: "z.bin", Dir: downloads, Start: false})
	path2 := m.item(id2).Path
	os.WriteFile(path2, []byte("half2"), 0o644)
	m.remove([]uint64{id2}, true)
	if _, err := os.Stat(path2); !os.IsNotExist(err) {
		t.Fatal("勾了删除文件,半份文件应一并删除")
	}
}

func TestStatePollingSkipsUnchanged(t *testing.T) {
	m, _ := newTestManager(t)
	v0 := m.version.Load()
	state := m.stateJSON()
	if state["v"].(uint64) != v0 {
		t.Fatalf("state 应带回版本号")
	}
	// 没变化时版本号不动。
	if m.version.Load() != v0 {
		t.Fatal("无事发生版本号不应变")
	}
	m.changed()
	if m.version.Load() == v0 {
		t.Fatal("变化后版本号应自增")
	}
	// state JSON 能完整序列化。
	b, err := json.Marshal(m.stateJSON())
	if err != nil || len(b) < 10 {
		t.Fatalf("state 应可序列化: %v", err)
	}
}

func TestAPIDispatch(t *testing.T) {
	m, downloads := newTestManager(t)
	s := &apiServer{m: m, base: "/app/swiftdl"}
	mux := setupTestMux(s)

	// 轮询态:未变时只回 same。
	st := getState(t, mux)
	st2 := getSameVersion(t, mux, st)
	if !st2["same"].(bool) {
		t.Fatal("版本没变时应回 same")
	}

	// 加任务(不启动)→ 版本号变。
	id := invokeOK(t, mux, "add_download", map[string]any{
		"req": AddRequest{URL: "https://example.com/a.bin", Filename: "a.bin", Dir: downloads, Start: false},
	}).(float64)
	if id == 0 {
		t.Fatal("应返回任务 id")
	}
	// 拒绝不支持的来源。
	invokeErr(t, mux, "add_download", map[string]any{
		"req": AddRequest{URL: "file:///etc/passwd", Filename: "p", Dir: downloads, Start: false},
	})
	// get_item / default_dir / is_torrent_url。
	it := invokeOK(t, mux, "get_item", map[string]any{"id": id}).(map[string]any)
	if it["status"].(string) != string(StatusPaused) {
		t.Fatalf("get_item 状态不对: %v", it["status"])
	}
	if d := invokeOK(t, mux, "default_dir", map[string]any{"filename": "song.mp3"}).(string); filepath.Base(d) != "Music" {
		t.Fatalf("应按类型给 Music 目录: %s", d)
	}
	if !invokeOK(t, mux, "is_torrent_url", map[string]any{"url": "magnet:?xt=urn:btih:abc"}).(bool) {
		t.Fatal("磁力链接应识别为 BT")
	}
	if invokeOK(t, mux, "is_media_url", map[string]any{"url": "https://x/v.mp4"}).(bool) {
		t.Fatal("媒体探测在 NAS 版恒为 false")
	}
	// 桩。
	if v := invokeOK(t, mux, "check_update", map[string]any{}); v != nil {
		t.Fatal("check_update 应为 null 桩")
	}
}

func TestParseSeedPolicies(t *testing.T) {
	if p, r := parseSeed("stop"); p != seedStop || r != 0 {
		t.Fatal("stop")
	}
	if p, r := parseSeed("2"); p != seedRatio || r != 2 {
		t.Fatal("分享率 2")
	}
	if p, _ := parseSeed("forever"); p != seedForever {
		t.Fatal("forever")
	}
	if p, _ := parseSeed("胡说"); p != seedStop {
		t.Fatal("不认识的一律 stop")
	}
}

func TestIsTorrentURL(t *testing.T) {
	cases := []struct {
		url, filename string
		want          bool
	}{
		{"magnet:?xt=urn:btih:dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c", "", true},
		{"https://example.com/files/ubuntu.iso.torrent?key=1", "", true},
		{"/tmp/x/film.TORRENT", "", true},
		{"https://tracker.example/download.php?id=42", "film.torrent", true},
		{"https://example.com/ubuntu.iso", "", false},
	}
	for _, c := range cases {
		if got := isTorrentURL(c.url, c.filename); got != c.want {
			t.Fatalf("isTorrentURL(%q, %q) = %v", c.url, c.filename, got)
		}
	}
}

// ---- 测试用的小 HTTP 环境 ----

func setupTestMux(s *apiServer) *http.ServeMux {
	mux := http.NewServeMux()
	s.register(mux)
	serveStatic(mux, s.base, t_DistDir())
	return mux
}

// 前端还没构建:给一个空目录,静态路由不该被这些测试碰到。
func t_DistDir() string { return os.TempDir() }

func getState(t *testing.T, mux *http.ServeMux) map[string]any {
	t.Helper()
	resp := httptest.NewRecorder()
	mux.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/app/swiftdl/api/state", nil))
	var st map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &st); err != nil {
		t.Fatalf("state 应答损坏: %v", err)
	}
	return st
}

func getSameVersion(t *testing.T, mux *http.ServeMux, st map[string]any) map[string]any {
	t.Helper()
	v := fmt.Sprintf("%d", uint64(st["v"].(float64)))
	resp := httptest.NewRecorder()
	mux.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/app/swiftdl/api/state?v="+v, nil))
	var out map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func invoke(t *testing.T, mux *http.ServeMux, cmd string, args map[string]any) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"cmd": cmd, "args": args})
	resp := httptest.NewRecorder()
	mux.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/app/swiftdl/api/invoke", bytes.NewReader(body)))
	var out map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatalf("invoke %s 应答损坏: %s", cmd, resp.Body.String())
	}
	return out
}

func invokeOK(t *testing.T, mux *http.ServeMux, cmd string, args map[string]any) any {
	t.Helper()
	out := invoke(t, mux, cmd, args)
	if out["ok"] != true {
		t.Fatalf("invoke %s 应成功,得 %v", cmd, out)
	}
	return out["data"]
}

func invokeErr(t *testing.T, mux *http.ServeMux, cmd string, args map[string]any) {
	t.Helper()
	out := invoke(t, mux, cmd, args)
	if out["ok"] == true {
		t.Fatalf("invoke %s 应失败,得 %v", cmd, out)
	}
}
