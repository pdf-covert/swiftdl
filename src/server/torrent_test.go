package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 最小可解析的种子:bencode 字典,info 里 name/length/piece length/pieces 齐全。
const tinyTorrent = "d4:infod6:lengthi123e4:name3:abc12:piece lengthi16384e6:pieces20:AAAAAAAAAAAAAAAAAAAAee"

func TestSaveTorrentFile(t *testing.T) {
	dir := t.TempDir()

	if _, err := saveTorrentFile(dir, "x.torrent", base64.StdEncoding.EncodeToString([]byte("hello"))); err == nil {
		t.Fatal("非 bencode 内容应当报错")
	}
	if _, err := saveTorrentFile(dir, "x.torrent", base64.StdEncoding.EncodeToString([]byte(strings.Replace(tinyTorrent, "infod", "infoX", 1)))); err == nil {
		t.Fatal("无法解析的种子应当报错")
	}

	p, err := saveTorrentFile(dir, "../../etc/passwd.torrent", base64.StdEncoding.EncodeToString([]byte(tinyTorrent)))
	if err != nil {
		t.Fatal(err)
	}
	if base := filepath.Base(p); !strings.HasSuffix(base, "_passwd.torrent") {
		t.Fatalf("文件名未净化: %s", base)
	}
	if !strings.HasPrefix(filepath.Dir(p), dir) {
		t.Fatalf("文件应保存在数据目录下: %s", p)
	}
	data, err := os.ReadFile(p)
	if err != nil || len(data) == 0 || data[0] != 'd' {
		t.Fatalf("保存的文件不对: %v %d", err, len(data))
	}

	// 中文名与空名兜底。
	if _, err := saveTorrentFile(dir, "电影.torrent", base64.StdEncoding.EncodeToString([]byte(tinyTorrent))); err != nil {
		t.Fatal(err)
	}
	p2, err := saveTorrentFile(dir, "..", base64.StdEncoding.EncodeToString([]byte(tinyTorrent)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(filepath.Base(p2), "torrent.torrent") {
		t.Fatalf("空名应兜底为 torrent: %s", p2)
	}
}

// DoH 应答只挑 A 记录(type=1)且必须是合法 IP,别的记录(域名 CNAME 等)不进。
func TestParseDoHAnswer(t *testing.T) {
	got := parseDoHAnswer([]byte(`{"Answer":[{"Type":1,"data":"67.215.246.10"},{"type":1,"data":"not-an-ip"},{"type":5,"data":"router.example.com"}]}`))
	if len(got) != 1 || got[0] != "67.215.246.10" {
		t.Fatalf("应只取出 67.215.246.10,得到 %v", got)
	}
	if parseDoHAnswer([]byte(`not json`)) != nil {
		t.Fatal("坏 JSON 应返回 nil")
	}
	if parseDoHAnswer([]byte(`{}`)) != nil {
		t.Fatal("空应答应返回 nil")
	}
}

// BEP 15 connect 包:魔数 + action=0 + 事务号;应答校验 action 与事务号。
func TestTrackerConnectPacket(t *testing.T) {
	tx := [4]byte{0xde, 0xad, 0xbe, 0xef}
	req := buildTrackerConnect(tx)
	if len(req) != 16 {
		t.Fatalf("请求应 16 字节,得到 %d", len(req))
	}
	var resp [16]byte
	copy(resp[:], req[8:]) // action+txid 原样当应答头
	if err := parseTrackerConnectResp(resp, tx); err != nil {
		t.Fatalf("事务号一致的应答应通过: %v", err)
	}
	resp[7] ^= 0xff
	if err := parseTrackerConnectResp(resp, tx); err == nil {
		t.Fatal("事务号不一致应报错")
	}
	resp[0] = 1 // action=1(错误)
	if err := parseTrackerConnectResp(resp, tx); err == nil {
		t.Fatal("错误码应报错")
	}
}
