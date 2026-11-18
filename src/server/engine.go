// 探测:一个极小的请求,问出文件多大、服务器是否支持分段(Range)、该叫什么名字。
// 上游 crates/engine/src/probe.rs 的 Go 移植。

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

type RemoteFile struct {
	// 跟随重定向后的最终地址;分段请求直接打到这里。
	URL          string
	Size         int64 // 未知时为 0
	Resumable    bool  // 服务器支持 Range 且报了总大小
	Filename     string
	Etag         string
	LastModified string
}

// Google Drive 的普通分享页会返回"无法扫描病毒"网页而非文件,这个地址直接给文件。
func driveDirectURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host != "drive.google.com" {
		return raw
	}
	id := u.Query().Get("id")
	if id == "" {
		segs := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(segs) >= 3 && segs[0] == "file" && segs[1] == "d" {
			id = segs[2]
		}
	}
	if id == "" {
		return raw
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '-' || c == '_') {
			return raw
		}
	}
	return "https://drive.usercontent.google.com/download?id=" + id + "&export=download&confirm=t"
}

// 只请求第 0 个字节:206 + Content-Range 证明支持 Range 并给出大小;
// 200 说明服务器忽略了 Range,只能单流下载。
func Probe(ctx context.Context, client *http.Client, rawURL string, headers http.Header) (*RemoteFile, error) {
	rawURL = driveDirectURL(rawURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("地址无效: %w", err)
	}
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("Range", "bytes=0-0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连不上 %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("服务器返回 %s", resp.Status)
	}

	rf := &RemoteFile{URL: resp.Request.URL.String()}
	if resp.StatusCode == http.StatusPartialContent {
		// Content-Range: bytes 0-0/N
		if cr := resp.Header.Get("Content-Range"); cr != "" {
			if total, ok := strings.CutPrefix(cr, "bytes "); ok {
				if _, after, ok := strings.Cut(total, "/"); ok {
					if n, err := strconv.ParseInt(strings.TrimSpace(after), 10, 64); err == nil {
						rf.Size, rf.Resumable = n, true
					}
				}
			}
		}
	} else if n := resp.ContentLength; n > 0 {
		rf.Size = n
	}

	rf.Etag = resp.Header.Get("ETag")
	rf.LastModified = resp.Header.Get("Last-Modified")
	rf.Filename = "download.bin"
	if name := filenameFromDisposition(resp.Header.Get("Content-Disposition")); name != "" {
		rf.Filename = sanitizeName(name)
	} else if last := path.Base(resp.Request.URL.Path); last != "" && last != "/" && last != "." {
		if dec, err := url.PathUnescape(last); err == nil {
			rf.Filename = sanitizeName(dec)
		} else {
			rf.Filename = sanitizeName(last)
		}
	}
	if rf.Filename == "" {
		rf.Filename = "download.bin"
	}

	// 该出文件的地方给了网页:多半是要登录或要点确认。存下来也只是一个小的坏文件。
	ctype := strings.ToLower(resp.Header.Get("Content-Type"))
	disposition := strings.ToLower(resp.Header.Get("Content-Disposition"))
	wantsHTML := strings.HasSuffix(strings.ToLower(rf.Filename), ".html") || strings.HasSuffix(strings.ToLower(rf.Filename), ".htm") || strings.HasSuffix(strings.ToLower(rf.Filename), ".xhtml")
	if strings.HasPrefix(ctype, "text/html") && !strings.Contains(disposition, "attachment") && !wantsHTML {
		return nil, errors.New("该链接打开的是网页而不是文件,站点可能需要先登录或确认")
	}
	return rf, nil
}

// 解析 Content-Disposition,兼容 `filename*=UTF-8”na%20me.zip`(RFC 6266/5987)
// 与 `filename="name.zip"`,优先编码形式。
func filenameFromDisposition(v string) string {
	if v == "" {
		return ""
	}
	for _, part := range strings.Split(v, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(strings.ToLower(part), "filename*=") {
			raw := part[len("filename*="):]
			if _, _, rest, ok := splitN3(raw, '\''); ok {
				raw = rest
			}
			raw = strings.Trim(raw, `"`)
			if dec, err := url.QueryUnescape(raw); err == nil {
				return dec
			}
			return strings.Trim(raw, `"`)
		}
	}
	for _, part := range strings.Split(v, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(strings.ToLower(part), "filename=") {
			return strings.Trim(part[len("filename="):], `"`)
		}
	}
	return ""
}

// splitN3 按 sep 把 s 切成三段(a,b,rest),用于 filename* 的 "charset'lang'value"。
func splitN3(s string, sep byte) (string, string, string, bool) {
	i := strings.IndexByte(s, sep)
	if i < 0 {
		return "", "", "", false
	}
	rest := s[i+1:]
	j := strings.IndexByte(rest, sep)
	if j < 0 {
		return "", "", "", false
	}
	return s[:i], rest[:j], rest[j+1:], true
}

// 去掉文件名里的路径部分与 Windows 禁用字符。
func sanitizeName(name string) string {
	name = name[max(strings.LastIndexByte(name, '/'), strings.LastIndexByte(name, '\\'))+1:]
	var b strings.Builder
	for _, r := range name {
		if strings.ContainsRune(`<>:"|?*`, r) || r < 0x20 {
			b.WriteByte('_')
		} else {
			b.WriteRune(r)
		}
	}
	return strings.TrimRight(strings.TrimSpace(b.String()), ".")
}
