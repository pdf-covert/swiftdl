// 整站抓取的第一步:读一个网页,列出它链接的文件。上游 grabber.rs 的移植。

package main

import (
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

var errBadHex = errors.New("bad hex")

type Found struct {
	URL  string `json:"url"`
	Name string `json:"name"` // 地址末尾的文件名
}

// 按页面顺序取出 href、src、data-src、data-url、data-href 的值。
// 手写扫描而不是用 html 解析器:属性出现在任何标签里都算,而页面常常
// 本身就不是合法 HTML。
func attributeValues(html string) []string {
	lower := strings.ToLower(html)
	type hit struct {
		at int
		v  string
	}
	var hits []hit
	for _, attr := range []string{"href", "src", "data-src", "data-url", "data-href"} {
		from := 0
		for {
			i := strings.Index(lower[from:], attr)
			if i < 0 {
				break
			}
			at := from + i
			from = at + len(attr)
			// 必须是完整的属性名,不是 "xhref" 这类的尾部。
			if at > 0 {
				switch lower[at-1] {
				case ' ', '\n', '\t', '\r', '"', '\'', '<':
				default:
					continue
				}
			}
			rest := strings.TrimLeft(html[from:], " \t\n\r\f\v")
			if !strings.HasPrefix(rest, "=") {
				continue
			}
			rest = strings.TrimLeft(rest[1:], " \t\n\r\f\v")
			var value string
			if rest != "" && (rest[0] == '"' || rest[0] == '\'') {
				q := rest[0]
				body := rest[1:]
				if j := strings.IndexByte(body, q); j >= 0 {
					value = body[:j]
				} else {
					value = body
				}
			} else {
				j := 0
				for j < len(rest) && !strings.ContainsRune(" \t\n\r\f\v", rune(rest[j])) && rest[j] != '>' {
					j++
				}
				value = rest[:j]
			}
			if v := strings.TrimSpace(value); v != "" {
				hits = append(hits, hit{at, v})
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].at < hits[j].at })
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.v
	}
	return out
}

// 页面链接到的所有 http(s) 地址:补全成绝对地址、去掉重复,至多 2000 条。
func findLinks(html string, page *url.URL) []Found {
	seen := map[string]bool{}
	var found []Found
	for _, raw := range attributeValues(html) {
		raw = strings.ReplaceAll(raw, "&amp;", "&")
		if strings.HasPrefix(raw, "#") || strings.HasPrefix(raw, "javascript:") ||
			strings.HasPrefix(raw, "mailto:") || strings.HasPrefix(raw, "data:") {
			continue
		}
		u, err := page.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			continue
		}
		u.Fragment = ""
		s := u.String()
		if seen[s] {
			continue
		}
		seen[s] = true
		name := ""
		segs := strings.Split(u.Path, "/")
		for i := len(segs) - 1; i >= 0; i-- {
			if segs[i] != "" {
				name = percentDecode(segs[i])
				break
			}
		}
		found = append(found, Found{URL: s, Name: name})
		if len(found) >= 2000 {
			break
		}
	}
	return found
}

func percentDecode(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == '%' && i+3 <= len(s) {
			if v, err := strconvParseByte(s[i+1 : i+3]); err == nil {
				b.WriteByte(v)
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func strconvParseByte(hex string) (byte, error) {
	v, err := strconv.ParseUint(hex, 16, 8)
	if err != nil {
		return 0, errBadHex
	}
	return byte(v), nil
}
