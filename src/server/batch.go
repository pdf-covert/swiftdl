// 把粘贴的一大段文字变成下载链接:每行或空格一个,并展开
// `https://site/file[01-50].zip` 这样的编号模式。上游 batch.rs 的移植。

package main

import (
	"fmt"
	"strconv"
	"strings"
)

const maxLinks = 500

// `https://x/a[01-03].zip` -> a01, a02, a03。补零宽度跟第一个数一致。
// 不是模式(没有 []、解析不了、跨度太大)时 ok 为 false。
func expandPattern(token string) ([]string, bool) {
	open := strings.IndexByte(token, '[')
	if open < 0 {
		return nil, false
	}
	rel := strings.IndexByte(token[open:], ']')
	if rel < 0 {
		return nil, false
	}
	close := open + rel
	loS, hiS, found := strings.Cut(token[open+1:close], "-")
	if !found {
		return nil, false
	}
	lo, err1 := strconv.ParseUint(loS, 10, 32)
	hi, err2 := strconv.ParseUint(hiS, 10, 32)
	if err1 != nil || err2 != nil || hi < lo || hi-lo >= maxLinks {
		return nil, false
	}
	width := 0
	if len(loS) > 1 && loS[0] == '0' {
		width = len(loS)
	}
	out := make([]string, 0, hi-lo+1)
	for n := lo; n <= hi; n++ {
		if width > 0 {
			out = append(out, fmt.Sprintf("%s%0*d%s", token[:open], width, n, token[close+1:]))
		} else {
			out = append(out, fmt.Sprintf("%s%d%s", token[:open], n, token[close+1:]))
		}
	}
	return out, true
}

// text 里的全部 http(s) 和 ed2k 链接,模式已展开,至多 500 条。
func expandLinks(text string) []string {
	var out []string
	for _, token := range strings.Fields(text) {
		lower := strings.ToLower(token)
		if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") &&
			!strings.HasPrefix(lower, "ed2k://|file|") {
			continue
		}
		if list, ok := expandPattern(token); ok {
			out = append(out, list...)
		} else {
			out = append(out, token)
		}
		if len(out) >= maxLinks {
			return out[:maxLinks:maxLinks]
		}
	}
	return out
}
