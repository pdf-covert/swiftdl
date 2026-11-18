// HTTP API:一个 POST 入口按 {cmd, args} 分发(命令名与参数形状沿用上游
// Tauri 命令,前端少改),一个 GET 轮询态。前端与上游不同处:事件推送换成
// 版本号轮询,文件夹选择换成服务端目录浏览(fnOS 里没有本地对话框)。

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const appVersion = "1.0.0"

type apiServer struct {
	m    *manager
	base string // 网关前缀,如 /app/swiftdl
}

func (s *apiServer) register(mux *http.ServeMux) {
	mux.HandleFunc(s.base+"/api/invoke", s.invoke)
	mux.HandleFunc(s.base+"/api/state", s.state)
}

type invokeReq struct {
	Cmd  string                     `json:"cmd"`
	Args map[string]json.RawMessage `json:"args"`
}

func (s *apiServer) invoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req invokeReq
	// 32MB:save_torrent_file 走 base64,16MB 种子 ≈ 21.4MB 文本。
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "请求格式不对: " + err.Error()})
		return
	}
	data, err := s.dispatch(r, &req)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": data})
}

func (s *apiServer) dispatch(r *http.Request, req *invokeReq) (any, error) {
	arg := func(name string) json.RawMessage { return req.Args[name] }
	str := func(name string) string {
		var v string
		_ = json.Unmarshal(arg(name), &v)
		return v
	}
	id := func(name string) uint64 {
		var v uint64
		_ = json.Unmarshal(arg(name), &v)
		return v
	}
	ids := func(name string) []uint64 {
		var v []uint64
		_ = json.Unmarshal(arg(name), &v)
		return v
	}
	boolean := func(name string) bool {
		var v bool
		_ = json.Unmarshal(arg(name), &v)
		return v
	}
	headersArg := func(name string) [][2]string {
		var v [][2]string
		_ = json.Unmarshal(arg(name), &v)
		return v
	}
	ctx, cancel := contextWithTimeout(r)
	defer cancel()

	switch req.Cmd {
	case "get_state":
		return s.m.stateJSON(), nil

	case "probe_url":
		remote, err := Probe(ctx, s.m.client, strings.TrimSpace(str("url")), headerMap(headersArg("headers")))
		if err != nil {
			return nil, err
		}
		category := categoryFor(remote.Filename)
		return map[string]any{
			"dir": s.m.defaultDir(category), "filename": remote.Filename,
			"size": remote.Size, "resumable": remote.Resumable, "category": category,
		}, nil

	case "probe_torrent":
		return probeTorrent(ctx, s.m.client, s.m.dataDir, strings.TrimSpace(str("url")), headerMap(headersArg("headers")))

	case "save_torrent_file":
		return saveTorrentFile(s.m.dataDir, str("name"), str("content"))

	case "is_torrent_url":
		filename := str("filename")
		return isTorrentURL(str("url"), filename), nil

	case "path_exists":
		// 新建下载前的同名检查:目标已存在时前端弹「是否重新下载」确认。
		p := filepath.Join(strings.TrimSpace(str("dir")), filepath.Base(strings.TrimSpace(str("name"))))
		fi, err := os.Stat(p)
		if err != nil {
			return map[string]any{"exists": false}, nil
		}
		return map[string]any{"exists": true, "path": p, "size": fi.Size(), "isDir": fi.IsDir()}, nil

	case "is_media_url":
		return false, nil

	case "default_dir":
		return s.m.defaultDir(categoryFor(str("filename"))), nil

	case "add_download":
		var addReq AddRequest
		if err := json.Unmarshal(arg("req"), &addReq); err != nil {
			return nil, fmt.Errorf("请求格式不对: %w", err)
		}
		if strings.TrimSpace(addReq.Dir) == "" && strings.TrimSpace(s.m.settings_().DownloadDir) == "" {
			return nil, errors.New("还没有下载目录:先在设置里选一个 fnOS 已授权的文件夹")
		}
		if err := checkAdd(&addReq); err != nil {
			return nil, err
		}
		return s.m.add(addReq), nil

	case "add_links":
		return s.m.addLinks(str("text")), nil

	case "add_metalink":
		name, n, err := s.m.addMetalink(ctx, strings.TrimSpace(str("url")))
		if err != nil {
			return nil, err
		}
		return []any{name, n}, nil

	case "grab_page":
		return s.grabPage(ctx, strings.TrimSpace(str("url")))

	case "net_diag":
		return runNetDiag(s.m), nil

	case "pause":
		s.m.pause(ids("ids"))
		return nil, nil

	case "resume":
		s.m.resume(ids("ids"))
		return nil, nil

	case "remove":
		s.m.remove(ids("ids"), boolean("deleteFiles"))
		return nil, nil

	case "set_url":
		return nil, s.m.setURL(id("id"), str("url"))

	case "download_next":
		return s.m.downloadNext(id("id")), nil

	case "get_item":
		return s.m.item(id("id")), nil

	case "set_settings":
		var settings Settings
		if err := json.Unmarshal(arg("settings"), &settings); err != nil {
			return nil, fmt.Errorf("设置格式不对: %w", err)
		}
		s.m.setSettings(settings)
		return nil, nil

	case "list_dirs":
		return s.listDirs(r, str("path"))

	case "authorized_dirs":
		roots, err := authorizedFolders(uidFromRequest(r))
		if err != nil {
			return map[string]any{"dirs": []string{}, "error": err.Error()}, nil
		}
		if roots == nil {
			roots = []string{}
		}
		return map[string]any{"dirs": roots, "error": ""}, nil

	// ---- 桌面版才有的事,给个安静的桩 ----
	case "integration_status":
		return nil, nil
	case "check_update":
		return nil, nil
	case "extension_dir":
		return nil, nil
	case "pick_folder":
		return nil, nil

	default:
		return nil, fmt.Errorf("未知命令: %s", req.Cmd)
	}
}

// GET /api/state?v=N:版本没变就不回传整个快照。
func (s *apiServer) state(w http.ResponseWriter, r *http.Request) {
	var since uint64
	fmt.Sscanf(r.URL.Query().Get("v"), "%d", &since)
	if since != 0 && since == s.m.version.Load() {
		writeJSON(w, http.StatusOK, map[string]any{"same": true, "v": since})
		return
	}
	s.m.tick() // 拿最新数字
	writeJSON(w, http.StatusOK, s.m.stateJSON())
}

// 允许的下载来源:网页链接;BT 还可以是磁力链接或本机上的 .torrent 文件;
// 电驴是 ed2k:// 链接(文件名和大小链接里就有,以此为准)。
func checkAdd(req *AddRequest) error {
	u := strings.TrimSpace(req.URL)
	if isED2KURL(u) {
		name, size, err := parseED2KLink(u)
		if err != nil {
			return err
		}
		req.Filename = name
		req.Size = &size
		return nil
	}
	web := strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
	torrent := req.TorrentFiles != nil && (strings.HasPrefix(u, "magnet:") || fileExists(u))
	if !web && !torrent {
		return fmt.Errorf("只支持网页链接、磁力链接、.torrent 文件和 ed2k 链接")
	}
	if strings.TrimSpace(req.Filename) == "" {
		return fmt.Errorf("文件名为空")
	}
	return nil
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func (s *apiServer) grabPage(ctx context.Context, raw string) ([]Found, error) {
	page, err := url.Parse(raw)
	if err != nil || (page.Scheme != "http" && page.Scheme != "https") {
		return nil, fmt.Errorf("这不是网页链接")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("服务器返回 %s", resp.Status)
	}
	// 网页是文本;超大或二进制的应答是文件,不是给人读的页面。
	if resp.ContentLength > 8_000_000 {
		return nil, fmt.Errorf("这是个文件,不是网页")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8_000_000))
	if err != nil {
		return nil, err
	}
	final, _ := url.Parse(resp.Request.URL.String())
	if final == nil {
		final = page
	}
	return findLinks(string(body), final), nil
}

// ---- fnOS 目录浏览:没有本地对话框,给前端一个服务器端的文件夹列表 ----

type dirEntryInfo struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Writable bool   `json:"writable"`
}

type dirListing struct {
	Path    string         `json:"path"`
	Parent  string         `json:"parent"` // "/" 时为空
	Entries []dirEntryInfo `json:"entries"`
}

// path 为空时列根目录(fnOS 的卷都在 /vol1、/vol2…)。
// listDirs 目录浏览,只允许在用户授权给应用的文件夹里走:
// path 为空 → 列出授权根目录(选择器从这里开始);
// path 非空 → 必须在某个授权根之内,否则报错引导去 fnOS 授权。
func (s *apiServer) listDirs(r *http.Request, raw string) (*dirListing, error) {
	roots, err := authorizedFolders(uidFromRequest(r))
	if err != nil {
		return nil, fmt.Errorf("查不到已授权的文件夹: %w", err)
	}
	clean := func(p string) string { return filepath.Clean("/" + strings.TrimSpace(p)) }

	if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "/" {
		// 根视图:授权文件夹本身即可选(直接选它当下载目录)。
		out := &dirListing{Path: "", Entries: []dirEntryInfo{}}
		for _, rt := range roots {
			c := clean(rt)
			out.Entries = append(out.Entries, dirEntryInfo{
				Name: filepath.Base(c), Path: c, Writable: dirWritable(c),
			})
		}
		sort.Slice(out.Entries, func(i, j int) bool { return out.Entries[i].Name < out.Entries[j].Name })
		return out, nil
	}

	p := clean(raw)
	if !underAuthorized(p, roots) {
		return nil, errors.New("这个目录不在已授权的文件夹里:请先在 fnOS 应用设置里把目标共享文件夹授权给 SwiftDL")
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, fmt.Errorf("读不了目录 %s: %w", p, err)
	}
	out := &dirListing{Path: p, Entries: []dirEntryInfo{}}
	// 上级还在授权范围内才给「上一级」,到授权根就停。
	if parent := filepath.Dir(p); parent != p && underAuthorized(parent, roots) {
		out.Parent = parent
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		full := filepath.Join(p, e.Name())
		out.Entries = append(out.Entries, dirEntryInfo{Name: e.Name(), Path: full, Writable: dirWritable(full)})
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		if out.Entries[i].Writable != out.Entries[j].Writable {
			return out.Entries[i].Writable
		}
		return out.Entries[i].Name < out.Entries[j].Name
	})
	if len(out.Entries) > 500 {
		out.Entries = out.Entries[:500]
	}
	return out, nil
}

// 能否往里写:试着创建再删掉一个临时文件。只读目录(挂载的网盘、系统区)
// 不能当下载目的地。
func dirWritable(dir string) bool {
	probe := filepath.Join(dir, ".swiftdl-write-probe")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(probe)
	return true
}

func contextWithTimeout(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 120*time.Second)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
