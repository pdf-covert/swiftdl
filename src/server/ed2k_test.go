// ed2k 链接解析与收尾搬移的纯逻辑测试。

package main

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleED2K = "ed2k://|file|cn_windows_10_business_editions_version_1903_x64_dvd_e001dd2c.iso|4815527936|47D4C57E638DF8BF74C59261E2CE702D|/"

func TestParseED2KLink(t *testing.T) {
	name, size, err := parseED2KLink(sampleED2K)
	if err != nil {
		t.Fatalf("应能解析: %v", err)
	}
	if name != "cn_windows_10_business_editions_version_1903_x64_dvd_e001dd2c.iso" || size != 4815527936 {
		t.Fatalf("解析结果不对: %q %d", name, size)
	}
	// 协议头大小写不敏感。
	if _, _, err := parseED2KLink("ED2K://|file|a.iso|100|0123456789ABCDEF0123456789ABCDEF|/"); err != nil {
		t.Fatalf("大写协议头应接受: %v", err)
	}
	// 坏链接:哈希不对、缺尾巴、非文件链接。
	for _, bad := range []string{
		"ed2k://|file|a.iso|100|nothex|/",
		"ed2k://|file|a.iso|100|0123456789ABCDEF0123456789ABCDEF",
		"ed2k://|server|1.2.3.4|4661|/",
		"magnet:?xt=urn:btih:abc",
	} {
		if _, _, err := parseED2KLink(bad); err == nil {
			t.Fatalf("应拒绝 %q", bad)
		}
	}
}

func TestIsED2KURL(t *testing.T) {
	if !isED2KURL(sampleED2K) || !isED2KURL(" ED2K://|file|a|1|hash|/") {
		t.Fatal("ed2k 链接应识别")
	}
	if isED2KURL("https://example.com/a.iso") || isED2KURL("magnet:?xt=urn:btih:x") {
		t.Fatal("别的链接不该当成 ed2k")
	}
}

func TestExpandLinksKeepsED2K(t *testing.T) {
	got := expandLinks("看这个 " + sampleED2K + " 以及 https://a.example/f.zip 结束")
	if len(got) != 2 || got[0] != sampleED2K || got[1] != "https://a.example/f.zip" {
		t.Fatalf("应保留 ed2k 与 http 链接: %v", got)
	}
}

func TestMoveIntoPlace(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "tmp")
	os.MkdirAll(sub, 0o755)
	src := filepath.Join(sub, "a.iso")
	dst := filepath.Join(dir, "out", "b (1).iso")
	os.WriteFile(src, []byte("hello ed2k"), 0o644)
	if err := moveIntoPlace(src, dst); err != nil {
		t.Fatalf("搬移应成功: %v", err)
	}
	data, err := os.ReadFile(dst)
	if err != nil || string(data) != "hello ed2k" {
		t.Fatalf("目标文件内容不对: %v %q", err, data)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("源文件应已不在")
	}
}
