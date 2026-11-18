// Metalink 文件(.meta4 / .metalink):一个文件、多个镜像,可能带校验和。
// Linux 发行版和不少项目用它发布。上游 crates/engine/src/metalink.rs 的移植,
// 用标准库 encoding/xml 逐个记号走。

package main

import (
	"encoding/xml"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
)

type Metalink struct {
	Name string
	Size *int64
	// 文件列出的 SHA-256(十六进制,小写),没有则空。
	SHA256 string
	// 最好的地址在前。
	URLs []string
}

func isMetallink(u string) bool {
	path := strings.ToLower(u)
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	return strings.HasSuffix(path, ".meta4") || strings.HasSuffix(path, ".metalink")
}

type mlURL struct {
	rank int64
	u    string
}

// 解析出第一个 <file>:名字、大小、sha-256、按优先级排序的地址。
// v4 用 priority(1 最好),v3 用 preference(100 最好)。
func parseMetalink(doc string) (*Metalink, error) {
	dec := xml.NewDecoder(strings.NewReader(doc))
	dec.Strict = false
	dec.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }

	var (
		m         *Metalink
		urls      []mlURL
		depth     int
		fileDepth = -1
		pending   struct {
			kind string
			buf  strings.Builder
			attr string
		}
	)
	apply := func() {
		text := strings.TrimSpace(pending.buf.String())
		switch pending.kind {
		case "size":
			if n, err := strconv.ParseInt(text, 10, 64); err == nil {
				m.Size = &n
			}
		case "hash":
			t := strings.ToLower(pending.attr)
			if t == "sha-256" || t == "sha256" {
				m.SHA256 = strings.ToLower(text)
			}
		case "url":
			if !strings.HasPrefix(text, "http://") && !strings.HasPrefix(text, "https://") {
				break
			}
			rank := int64(999_999)
			if p, err := strconv.ParseInt(field(pending.attr, 0), 10, 64); err == nil {
				rank = p
			} else if pref, err := strconv.ParseInt(field(pending.attr, 1), 10, 64); err == nil {
				rank = 1000 - pref
			}
			urls = append(urls, mlURL{rank, text})
		}
		pending.kind = ""
		pending.buf.Reset()
	}

	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, errors.New("不是有效的 Metalink 文件")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			name := strings.ToLower(t.Name.Local)
			attr := func(k string) string {
				for _, a := range t.Attr {
					if strings.EqualFold(a.Name.Local, k) {
						return a.Value
					}
				}
				return ""
			}
			if m == nil {
				if name == "file" {
					n := attr("name")
					if n == "" {
						return nil, errors.New("Metalink 文件没有文件名")
					}
					m = &Metalink{Name: n}
					fileDepth = depth
				}
				continue
			}
			if depth > fileDepth {
				switch name {
				case "size":
					pending.kind, pending.attr = "size", ""
				case "hash":
					pending.kind, pending.attr = "hash", attr("type")
				case "url":
					pending.kind, pending.attr = "url", attr("priority")+"\x00"+attr("preference")
				default:
					pending.kind, pending.attr = "", ""
				}
			}
		case xml.CharData:
			if pending.kind != "" {
				pending.buf.Write(t)
			}
		case xml.EndElement:
			if pending.kind == strings.ToLower(t.Name.Local) {
				apply()
			}
			depth--
			if fileDepth > 0 && depth < fileDepth {
				break // 第一个 </file> 结束,后面的不看
			}
		}
	}
	if m == nil {
		return nil, errors.New("Metalink 没有列出文件")
	}
	if len(urls) == 0 {
		return nil, errors.New("Metalink 里没有可用的下载地址")
	}
	sort.SliceStable(urls, func(i, j int) bool { return urls[i].rank < urls[j].rank })
	for _, u := range urls {
		m.URLs = append(m.URLs, u.u)
	}
	return m, nil
}

// field 取 "priority\x00preference" 的一段。
func field(s string, i int) string {
	parts := strings.SplitN(s, "\x00", 2)
	if i >= len(parts) {
		return ""
	}
	return parts[i]
}
