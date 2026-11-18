// 网络自检:BT 找源有三条腿——DHT(先要把引导域名解析成 IP)、UDP tracker、
// TCP(http/https)tracker。下载慢时用设置页的「网络诊断」逐条点亮,哪条
// 断了一目了然;顺带把限速/代理这类常见误配置摆出来。

package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"
)

// OK: nil = 纯信息行;true/false = 通过/失败。
type DiagRow struct {
	Name   string `json:"name"`
	OK     *bool  `json:"ok"`
	Detail string `json:"detail"`
}

var diagUDPTrackers = []string{
	"tracker.opentrackr.org:1337",
	"open.demonii.com:1337",
	"open.stealth.si:80",
}

// TCP 探测目标选实测长期在线的;挑不通的会让健康网络也满屏红灯。
var diagTCPAddrs = []string{
	"open.tracker.cl:1337",
	"tracker.opentrackr.org:1337",
}

func runNetDiag(m *manager) []DiagRow {
	var rows []DiagRow
	row := func(name string, ok *bool, format string, a ...any) {
		rows = append(rows, DiagRow{Name: name, OK: ok, Detail: fmt.Sprintf(format, a...)})
	}
	boolp := func(b bool) *bool { return &b }

	// 1) 系统 DNS + 2) DoH:两条一起判。被污染时系统 DNS 也会"解析成功",
	// 但给的是假 IP——和 DoH 的结果完全对不上时就明说。
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	ips, err := net.DefaultResolver.LookupHost(ctx, "router.bittorrent.com")
	cancel()
	ctx, cancel = context.WithTimeout(context.Background(), 4*time.Second)
	doh := resolveA(ctx, "router.bittorrent.com")
	cancel()
	if len(doh) > 0 {
		row("AliDNS DoH 解析", boolp(true), "→ %s", strings.Join(firstIPs(doh, 3), ", "))
	} else {
		row("AliDNS DoH 解析", boolp(false), "连不上或应答异常")
	}
	if err != nil {
		row("系统 DNS 解析", boolp(false), "%v", err)
	} else if len(doh) > 0 && disjoint(ips, doh) {
		// 这个域名的 IP 本身会轮换,不一致≠污染;DHT 播种走的是 DoH 结果。
		row("系统 DNS 解析", nil, "→ %s(与 DoH 不同,可能污染也可能轮换;DHT 已用 DoH 结果)", strings.Join(firstIPs(ips, 2), ", "))
	} else {
		row("系统 DNS 解析", boolp(true), "router.bittorrent.com → %s", strings.Join(firstIPs(ips, 3), ", "))
	}

	// 3) DHT 引导注入:seedDHT 实际喂进引擎的节点数。
	if n := bt.dhtSeeded.Load(); n > 0 {
		row("DHT 引导节点", boolp(true), "已注入 %d 个 IP", n)
	} else if bt.everStarted.Load() {
		row("DHT 引导节点", boolp(false), "解析全部失败,DHT 可能起不来")
	} else {
		row("DHT 引导节点", nil, "BT 尚未使用,加一个种子后再测")
	}

	// 4) UDP tracker 握手(BEP 15):UDP 出站的直接证据。
	for _, a := range diagUDPTrackers {
		rtt, err := udpTrackerPing(a, 3*time.Second)
		if err != nil {
			row("UDP tracker "+a, boolp(false), "%v", err)
		} else {
			row("UDP tracker "+a, boolp(true), "握手 %dms", rtt.Milliseconds())
		}
	}

	// 5) TCP 出站:UDP 被拦时 http(s) tracker 还能撑住找源。
	for _, a := range diagTCPAddrs {
		start := time.Now()
		conn, err := net.DialTimeout("tcp", a, 3*time.Second)
		if err != nil {
			row("TCP 连接 "+a, boolp(false), "%v", err)
		} else {
			conn.Close()
			row("TCP 连接 "+a, boolp(true), "连上 %dms", time.Since(start).Milliseconds())
		}
	}

	// 6) 常见误配置摆出来。
	s := m.settings_()
	if s.SpeedLimitKib > 0 {
		row("全局限速", boolp(false), "开着:%d KiB/s——除非有意限速,建议关掉", s.SpeedLimitKib)
	} else {
		row("全局限速", nil, "关闭")
	}
	if p := strings.TrimSpace(s.Proxy); p != "" {
		row("代理", nil, "%s(重启后生效)", p)
	} else {
		row("代理", nil, "直连")
	}
	return rows
}

// disjoint:两组地址没有一个相同。
func disjoint(a, b []string) bool {
	set := make(map[string]struct{}, len(b))
	for _, s := range b {
		set[s] = struct{}{}
	}
	for _, s := range a {
		if _, ok := set[s]; ok {
			return false
		}
	}
	return true
}

func firstIPs(ips []string, n int) []string {
	sort.Strings(ips)
	if len(ips) > n {
		ips = ips[:n]
	}
	return ips
}

// udpTrackerPing 按 BEP 15 发一个 connect,收到合法 connection_id 即通。
func udpTrackerPing(addr string, timeout time.Duration) (time.Duration, error) {
	conn, err := net.DialTimeout("udp", addr, timeout)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return 0, err
	}
	var tx [4]byte
	if _, err := rand.Read(tx[:]); err != nil {
		return 0, err
	}
	start := time.Now()
	if _, err := conn.Write(buildTrackerConnect(tx)); err != nil {
		return 0, err
	}
	var resp [16]byte
	if _, err := readFull(conn, resp[:]); err != nil {
		return 0, err
	}
	rtt := time.Since(start)
	if err := parseTrackerConnectResp(resp, tx); err != nil {
		return 0, err
	}
	return rtt, nil
}

func buildTrackerConnect(tx [4]byte) []byte {
	var b [16]byte
	binary.BigEndian.PutUint64(b[0:], 0x41727101980) // 协议魔数
	binary.BigEndian.PutUint32(b[8:], 0)             // action: connect
	copy(b[12:], tx[:])
	return b[:]
}

func parseTrackerConnectResp(resp [16]byte, tx [4]byte) error {
	if binary.BigEndian.Uint32(resp[0:]) != 0 {
		return errors.New("tracker 回了错误码")
	}
	if resp[4] != tx[0] || resp[5] != tx[1] || resp[6] != tx[2] || resp[7] != tx[3] {
		return errors.New("应答的事务号对不上")
	}
	return nil
}

func readFull(conn net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := conn.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
