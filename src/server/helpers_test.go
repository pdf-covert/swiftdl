// metalink / batch / grabber 的测试,对照上游各自 tests 模块的用例。

package main

import (
	"net/url"
	"testing"
)

func metalinkMustParse(t *testing.T, doc string) *Metalink {
	t.Helper()
	m, err := parseMetalink(doc)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMetalinkReadsVersion4(t *testing.T) {
	m := metalinkMustParse(t, `<metalink xmlns="urn:ietf:params:xml:ns:metalink"><file name="x.iso"><size>123</size>
            <hash type="sha-1">aa</hash><hash type="sha-256">ABCD</hash>
            <url priority="2">https://b.example/x.iso</url><url priority="1">https://a.example/x.iso</url><url>ftp://c.example/x.iso</url></file></metalink>`)
	if m.Name != "x.iso" || m.Size == nil || *m.Size != 123 || m.SHA256 != "abcd" {
		t.Fatalf("解析结果不对: %+v", m)
	}
	if len(m.URLs) != 2 || m.URLs[0] != "https://a.example/x.iso" || m.URLs[1] != "https://b.example/x.iso" {
		t.Fatalf("地址排序不对: %v", m.URLs)
	}
}

func TestMetalinkReadsVersion3(t *testing.T) {
	m := metalinkMustParse(t, `<metalink version="3.0" xmlns="http://www.metalinker.org/"><files><file name="y.zip"><size>9</size>
            <verification><hash type="sha256">ff00</hash></verification>
            <resources><url type="http" preference="50">http://slow.example/y.zip</url><url type="https" preference="100">https://fast.example/y.zip</url></resources></file></files></metalink>`)
	if m.URLs[0] != "https://fast.example/y.zip" || m.SHA256 != "ff00" || m.Size == nil || *m.Size != 9 {
		t.Fatalf("v3 解析不对: %+v", m)
	}
}

func TestMetalinkSpotsAndRejects(t *testing.T) {
	for _, u := range []string{"https://x/a.iso.meta4?mirror=1", "https://x/a.METALINK"} {
		if !isMetallink(u) {
			t.Fatalf("应认出 Metalink: %s", u)
		}
	}
	if isMetallink("https://x/a.iso") {
		t.Fatal("普通文件不是 Metalink")
	}
	if _, err := parseMetalink("<metalink/>"); err == nil {
		t.Fatal("没有文件条目应报错")
	}
}

func TestBatchExpandsPatternsAndLists(t *testing.T) {
	eq := func(got []string, want ...string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("得 %v,想要 %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("得 %v,想要 %v", got, want)
			}
		}
	}
	eq(expandLinks("https://x/a[08-10].zip"), "https://x/a08.zip", "https://x/a09.zip", "https://x/a10.zip")
	eq(expandLinks("https://x/p[1-3]"), "https://x/p1", "https://x/p2", "https://x/p3")
	eq(expandLinks("https://a/1\nhttps://b/2 junk  http://c/3"), "https://a/1", "https://b/2", "http://c/3")
	if got := expandLinks("http://[::1]/file"); len(got) != 1 || got[0] != "http://[::1]/file" {
		t.Fatalf("IPv6 地址不应被当作模式: %v", got)
	}
	// 跨度太大:不是模式,原样保留。
	if got := expandLinks("https://x/a[1-9999].zip"); len(got) != 1 || got[0] != "https://x/a[1-9999].zip" {
		t.Fatalf("超大跨度应原样: %v", got)
	}
	if got := expandLinks("这里没有链接"); len(got) != 0 {
		t.Fatalf("应得空: %v", got)
	}
	if got := expandLinks("https://x/a[1-500].zip"); len(got) != 500 {
		t.Fatalf("应恰好 500 条,得 %d", len(got))
	}
}

func TestGrabberFindsAndResolvesLinks(t *testing.T) {
	page, err := url.Parse("https://example.com/docs/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := `<a href="guide.pdf">g</a> <A HREF='/files/My%20Report.docx#p2'>r</A> <img src=pic.png> <a href="#top">t</a>
            <a href="mailto:a@b.c">m</a> <a href="javascript:void(0)">j</a> <a href="guide.pdf">again</a> <a href="https://cdn.example.net/v.mp4?x=1&amp;y=2">v</a>
            <a data-xhref="no.zip">x</a> <script src="/app.js"></script>`
	found := findLinks(html, page)
	want := []struct{ url, name string }{
		{"https://example.com/docs/guide.pdf", "guide.pdf"},
		{"https://example.com/files/My%20Report.docx", "My Report.docx"},
		{"https://example.com/docs/pic.png", "pic.png"},
		{"https://cdn.example.net/v.mp4?x=1&y=2", "v.mp4"},
		{"https://example.com/app.js", "app.js"},
	}
	if len(found) != len(want) {
		t.Fatalf("得 %d 条 %v,想要 %d 条", len(found), found, len(want))
	}
	for i, w := range want {
		if found[i].URL != w.url || found[i].Name != w.name {
			t.Fatalf("第 %d 条得 %+v,想要 %+v", i, found[i], w)
		}
	}
}
