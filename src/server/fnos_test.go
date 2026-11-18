package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 授权目录模型:下载目录只允许落在用户授权给应用的文件夹里。

func TestMergeRoots(t *testing.T) {
	got := mergeRoots(
		[]string{"/vol1/1000/x", "/vol2/share", " ", "/vol1/1000/x"},
		[]string{"/vol3/media", "/vol2/share"},
	)
	want := []string{"/vol1/1000/x", "/vol2/share", "/vol3/media"}
	if len(got) != len(want) {
		t.Fatalf("mergeRoots = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mergeRoots[%d] = %q, want %q(应去重且有序)", i, got[i], want[i])
		}
	}
}

// startFakeApiscope 在临时 Unix socket 上假扮 fnOS apiscope 服务,
// 把 apiscopeSocketPath 指过去并注入假 TRIM_API_TOKEN,测完恢复。
func startFakeApiscope(t *testing.T, handle func(req string, data json.RawMessage) (int, string)) {
	t.Helper()
	// macOS 的 t.TempDir() 路径超过 unix socket 104 字节上限,用短前缀另开。
	sockPath := fmt.Sprintf("/tmp/sdl-api-%d.sock", time.Now().UnixNano())
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/trimapp", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Req     string          `json:"req"`
			AppName string          `json:"appName"`
			Data    json.RawMessage `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Errorf("bad request body: %v", err)
		}
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Errorf("缺少 Bearer 授权头: %q", got)
		}
		if in.AppName != "swiftdl" {
			t.Errorf("appName = %q, want swiftdl", in.AppName)
		}
		code, body := handle(in.Req, in.Data)
		w.Write([]byte(`{"reqId":"x","code":` + jsonInt(code) +
			`,"msg":"","data":` + body + `}`))
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	old := apiscopeSocketPath
	apiscopeSocketPath = ln.Addr().String()
	t.Setenv("TRIM_API_TOKEN", "test-token")
	t.Cleanup(func() {
		apiscopeSocketPath = old
		srv.Close()
		os.Remove(sockPath)
	})
}

func jsonInt(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestAuthorizedFoldersUnionsAdminAndUserGrants(t *testing.T) {
	startFakeApiscope(t, func(req string, data json.RawMessage) (int, string) {
		switch req {
		case "trim.file.getUserAccessibleFolders":
			if !strings.Contains(string(data), `"uid":7`) {
				t.Errorf("user 查询应带 uid 7: %s", data)
			}
			return 0, `{"paths":["/vol1/1000/我的下载"]}`
		case "trim.file.getSharedAccessibleFolders":
			return 0, `{"paths":["/vol3/影视","/vol1/1000/我的下载"]}`
		default:
			t.Errorf("意外的 req: %s", req)
			return 1, `{}`
		}
	})
	roots, err := authorizedFolders(7)
	if err != nil {
		t.Fatalf("authorizedFolders: %v", err)
	}
	want := "/vol1/1000/我的下载|/vol3/影视"
	if got := strings.Join(roots, "|"); got != want {
		t.Fatalf("授权目录 = %q, want %q(管理员+用户并集去重)", got, want)
	}
}

func TestAuthorizedFoldersSingleSideFailureStillUsable(t *testing.T) {
	startFakeApiscope(t, func(req string, _ json.RawMessage) (int, string) {
		if req == "trim.file.getUserAccessibleFolders" {
			return 500, `{}` // 个人授权查询挂了
		}
		return 0, `{"paths":["/vol3/影视"]}`
	})
	roots, err := authorizedFolders(7)
	if err != nil {
		t.Fatalf("单边失败不该整体报错: %v", err)
	}
	if len(roots) != 1 || roots[0] != "/vol3/影视" {
		t.Fatalf("应回退到管理员授权列表: %v", roots)
	}
}

func TestAuthorizedFoldersBothSidesFail(t *testing.T) {
	startFakeApiscope(t, func(string, json.RawMessage) (int, string) {
		return 1, `{}`
	})
	if _, err := authorizedFolders(7); err == nil {
		t.Fatal("两边都失败应报错(fail-closed)")
	}
}

func TestUnderAuthorized(t *testing.T) {
	roots := []string{"/vol1/1000/downloads", "/vol2/share"}
	cases := []struct {
		p    string
		want bool
	}{
		{"/vol1/1000/downloads", true},
		{"/vol1/1000/downloads/视频", true},
		{"/vol1/1000/downloads2", false}, // 前缀相似但不是子路径
		{"/vol1/1000", false},
		{"/vol2/share", true},
		{"/etc", false},
	}
	for _, c := range cases {
		if got := underAuthorized(c.p, roots); got != c.want {
			t.Errorf("underAuthorized(%q) = %v, want %v", c.p, got, c.want)
		}
	}
}

func TestListDirsRequiresAuthorization(t *testing.T) {
	m, _ := newTestManager(t)
	mux := setupTestMux(&apiServer{m: m, base: "/app/swiftdl"})

	t.Setenv("TRIM_API_TOKEN", "") // 本地:走 SWIFTDL_DEV_FOLDERS
	root := t.TempDir()
	t.Setenv("SWIFTDL_DEV_FOLDERS", root)

	// 根视图 = 授权文件夹本身。
	out := invokeOK(t, mux, "list_dirs", map[string]any{"path": ""}).(map[string]any)
	entries := out["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("根视图应只列授权根: %v", out)
	}
	e := entries[0].(map[string]any)
	if e["path"] != filepath.Clean(root) {
		t.Fatalf("根路径 = %v", e["path"])
	}
	if out["path"] != "" {
		t.Fatalf("根视图 path 应为空串: %v", out["path"])
	}

	// 授权根之内:可浏览,且没有「上一级」。
	out = invokeOK(t, mux, "list_dirs", map[string]any{"path": root}).(map[string]any)
	if out["parent"] != "" {
		t.Fatalf("授权根之上不该有上级: %v", out["parent"])
	}

	// 授权根之外:拒绝。
	invokeErr(t, mux, "list_dirs", map[string]any{"path": "/etc"})
}

func TestAddDownloadNeedsDir(t *testing.T) {
	m, _ := newTestManager(t)
	mux := setupTestMux(&apiServer{m: m, base: "/app/swiftdl"})

	// 模拟全新安装:用户还没选过下载目录。
	s := m.settings_()
	s.DownloadDir = ""
	m.setSettings(s)

	// 没有任务自带目录、设置里也没选过下载目录 → 拒绝。
	invokeErr(t, mux, "add_download", map[string]any{
		"req": map[string]any{"url": "https://example.com/a.bin", "filename": "a.bin", "dir": "", "start": false},
	})

	// 错误文案应引导授权。
	body := invoke(t, mux, "add_download", map[string]any{
		"req": map[string]any{"url": "https://example.com/a.bin", "filename": "a.bin", "dir": "", "start": false},
	})
	if msg, _ := body["error"].(string); !strings.Contains(msg, "下载目录") {
		t.Fatalf("错误应提到下载目录: %v", body["error"])
	}
}

func TestAuthorizedDirsFailClosed(t *testing.T) {
	m, _ := newTestManager(t)
	mux := setupTestMux(&apiServer{m: m, base: "/app/swiftdl"})

	t.Setenv("TRIM_API_TOKEN", "")
	t.Setenv("SWIFTDL_DEV_FOLDERS", "") // 无 fnOS 也无开发回退 → fail-closed

	out := invokeOK(t, mux, "authorized_dirs", map[string]any{}).(map[string]any)
	if dirs := out["dirs"].([]any); len(dirs) != 0 {
		t.Fatalf("失败时应回空列表: %v", dirs)
	}
	if out["error"] == "" {
		t.Fatal("失败时应带 error 文案")
	}
}
