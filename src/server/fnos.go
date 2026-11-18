// fnos.go — fnOS 开放平台后端 API(apiscope)客户端。
//
// 下载目录不落在应用私有目录,而由用户在 fnOS 里把共享文件夹授权给本应用
// (config/resource 的 api-scope:userAccess → getUserAccessibleFolders)。
// 服务端经 fnOS 注入的专用 Unix socket 直达开放平台;凭证为环境变量
// TRIM_API_TOKEN,每次调用现读(fnOS 可能轮换),不持久化、不写日志。
// 授权可能被随时收回:所有判定都实时查询,不缓存。
//
// 失败一律 fail-closed:授权列表返回空,前端提示去 fnOS 应用设置授权。
// 本地开发(无 fnOS)可用 SWIFTDL_DEV_FOLDERS=/a:/b 指定假授权目录,
// 以及 SWIFTDL_DEV_UID 指定用户 id。

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	apiscopeSocket = "/var/run/trim_open_gateway_apiscope.socket"
	apiscopePath   = "/api/v1/trimapp"
	apiscopeMax    = 1 << 20
)

// socket 路径走包级变量:测试指向假服务即刻生效。
var apiscopeSocketPath = apiscopeSocket

// fnosClient 走 apiscope socket 的 HTTP 客户端(Transport 固定拨到 Unix socket)。
func fnosClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", apiscopeSocketPath)
			},
		},
	}
}

type apiscopeRequest struct {
	ReqID   string `json:"reqId"`
	Req     string `json:"req"`
	AppName string `json:"appName"`
	Data    any    `json:"data"`
}

type apiscopeEnvelope struct {
	ReqID string          `json:"reqId"`
	Code  int             `json:"code"`
	Msg   string          `json:"msg"`
	Data  json.RawMessage `json:"data"`
}

// apiscopeCall 走一遍统一请求/响应链;dst 为响应 data 的解析目标(nil 不解析)。
func apiscopeCall(req string, payload, dst any) error {
	token := os.Getenv("TRIM_API_TOKEN") // 每次现读:fnOS 可能轮换 token
	if token == "" {
		return errors.New("缺少 TRIM_API_TOKEN(应用未获得 fnOS API 授权)")
	}
	body, err := json.Marshal(apiscopeRequest{
		ReqID:   "sdl-" + strconv.FormatInt(time.Now().UnixNano(), 36),
		Req:     req,
		AppName: "swiftdl",
		Data:    payload,
	})
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequest(http.MethodPost, "http://unix"+apiscopePath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)

	resp, err := fnosClient().Do(httpReq)
	if err != nil {
		return fmt.Errorf("fnos api %s: %w", req, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, apiscopeMax+1))
	if err != nil {
		return fmt.Errorf("fnos api %s: 读取响应失败: %w", req, err)
	}
	if len(raw) > apiscopeMax {
		return fmt.Errorf("fnos api %s: 响应超过上限", req)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fnos api %s: http %d", req, resp.StatusCode)
	}
	var env apiscopeEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("fnos api %s: 解析响应失败: %w", req, err)
	}
	if env.Code != 0 {
		return fmt.Errorf("fnos api %s: code %d: %s", req, env.Code, env.Msg)
	}
	if dst == nil {
		return nil
	}
	return json.Unmarshal(env.Data, dst)
}

// userAccessibleFolders uid 用户经 fnOS 系统选择器授权给本应用的目录。
func userAccessibleFolders(uid int64) ([]string, error) {
	var out struct {
		Paths []string `json:"paths"`
	}
	if err := apiscopeCall("trim.file.getUserAccessibleFolders", map[string]int64{"uid": uid}, &out); err != nil {
		return nil, err
	}
	return out.Paths, nil
}

// sharedAccessibleFolders 管理员在 fnOS 应用设置「允许访问以下文件夹」里授权给
// 本应用的目录(全应用同一份,与具体用户无关)。req 数据须为空对象 {}(不能省略)。
func sharedAccessibleFolders() ([]string, error) {
	var out struct {
		Paths []string `json:"paths"`
	}
	if err := apiscopeCall("trim.file.getSharedAccessibleFolders", struct{}{}, &out); err != nil {
		return nil, err
	}
	return out.Paths, nil
}

// mergeRoots 两路授权目录并集,去重、按路径排序(根视图顺序稳定)。
func mergeRoots(user, shared []string) []string {
	seen := make(map[string]bool, len(user)+len(shared))
	out := make([]string, 0, len(user)+len(shared))
	for _, p := range append(append([]string{}, shared...), user...) {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// authorizedFolders 当前用户可用的授权目录 = 管理员授权(应用级)∪ 用户个人授权,
// 实时查询不缓存;两边都失败才报错(单边失败用另一边结果并记日志,fail-closed
// 语义保留:仅当至少一路查询成功时才给出列表)。本地开发没有 fnOS 时退回
// SWIFTDL_DEV_FOLDERS(冒号分隔)便于联调。
func authorizedFolders(uid int64) ([]string, error) {
	if os.Getenv("TRIM_API_TOKEN") == "" {
		if dev := strings.TrimSpace(os.Getenv("SWIFTDL_DEV_FOLDERS")); dev != "" {
			return strings.Split(dev, ":"), nil
		}
		return nil, errors.New("尚未连接 fnOS 授权服务")
	}
	user, uerr := userAccessibleFolders(uid)
	shared, serr := sharedAccessibleFolders()
	if uerr != nil && serr != nil {
		return nil, fmt.Errorf("查询授权目录失败(个人:%v;管理员:%v)", uerr, serr)
	}
	if uerr != nil {
		log.Printf("查询用户授权目录失败(只用管理员授权列表): %v", uerr)
	}
	if serr != nil {
		log.Printf("查询管理员授权目录失败(只用用户授权列表): %v", serr)
	}
	return mergeRoots(user, shared), nil
}

// uidFromRequest 当前使用用户的 fnOS uid:生产经网关注入的 X-Trim-Userid 头;
// 本地开发回退 SWIFTDL_DEV_UID;再缺失为 0。
func uidFromRequest(r *http.Request) int64 {
	if v := strings.TrimSpace(r.Header.Get("X-Trim-Userid")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	if v := strings.TrimSpace(os.Getenv("SWIFTDL_DEV_UID")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return 0
}

// underAuthorized p 是否在 roots 的某个授权文件夹里(含相等)。
func underAuthorized(p string, roots []string) bool {
	for _, r := range roots {
		r = filepath.Clean("/" + strings.TrimSpace(r))
		if r == "" || r == "." {
			continue
		}
		if p == r || strings.HasPrefix(p, r+"/") {
			return true
		}
	}
	return false
}
