// BT 与磁力链接,走 anacrolix/torrent(纯 Go 的 BitTorrent 引擎)。
//
// 种子先“只列清单”:加进会话等元数据、读出文件列表后立刻退出,不动数据。
// 正式下载时每个种子用自己目录上的文件存储,分片完成记录共享一个
// Bolt 库,暂停 = 丢弃种子句柄,恢复 = 重新加入并跳过已完成的分片。

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// Note 是任务上的一句实时状态(“正在从 5 个节点下载”),由引擎写、tick 读。
type Note struct {
	mu sync.Mutex
	s  string
}

func (n *Note) Set(s string) {
	n.mu.Lock()
	n.s = s
	n.mu.Unlock()
}

func (n *Note) Get() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.s
}

// 是不是 BT 源:磁力链接,或指向 .torrent 的地址/文件名。
func isTorrentURL(url, filename string) bool {
	u := strings.TrimSpace(url)
	path := strings.ToLower(strings.SplitN(u, "?", 2)[0])
	path = strings.SplitN(path, "#", 2)[0]
	return strings.HasPrefix(u, "magnet:") ||
		strings.HasSuffix(path, ".torrent") ||
		strings.HasSuffix(strings.ToLower(filename), ".torrent")
}

type TorrentFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type TorrentInfo struct {
	Name  string        `json:"name"`
	Size  int64         `json:"size"`
	Files []TorrentFile `json:"files"`
}

// 全应用一个 BT 会话,首次使用时启动。
var bt struct {
	once       sync.Once
	client     *torrent.Client
	completion storage.PieceCompletion
	err        error
	// 网络诊断要读的状态(原子量,避开与会话初始化的竞争):
	everStarted atomic.Bool
	dhtSeeded   atomic.Int32
}

func btSession(dataDir string) (*torrent.Client, storage.PieceCompletion, error) {
	bt.once.Do(func() {
		bt.everStarted.Store(true)
		dir := filepath.Join(dataDir, "torrents")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			bt.err = err
			return
		}
		completion, err := storage.NewBoltPieceCompletion(dir)
		if err != nil {
			bt.err = err
			return
		}
		cfg := torrent.NewDefaultClientConfig()
		cfg.DataDir = dir
		cfg.Seed = true // 做种到策略允许的程度,由各任务自己 Drop
		// 加密策略保持引擎默认(优先 MSE 头混淆、对方不支持时回退明文)。
		// 试过强制加密(RequirePreferred)反制运营商 DPI:实测国产影视
		// Swarm 里大量节点(迅雷系/老客户端)只说明文,强制后一个节点都
		// 连不上,速度归零——得不偿失,已回退。
		// 热门种子的速度主要看同时连着的节点数,默认 50 偏保守;
		// 哈希校验并发也放大些,别让校验拖住写入。
		cfg.EstablishedConnsPerTorrent = 100
		cfg.PieceHashersPerTorrent = 4
		cfg.TorrentPeersLowWater = 100
		cfg.DefaultStorage = storage.NewFileWithCompletion(dir, completion)
		cl, err := torrent.NewClient(cfg)
		if err != nil {
			bt.err = err
			return
		}
		seedDHT(cl)
		bt.client = cl
		bt.completion = completion
	})
	return bt.client, bt.completion, bt.err
}

// 把来源变成种子描述:磁力链接原样;http(s) 按页面 Cookie 取回
// (.torrent 站点多数只给登录浏览器);本地路径直接读。
func resolveTorrentSpec(ctx context.Context, client *http.Client, url string, headers http.Header) (*torrent.TorrentSpec, error) {
	u := strings.TrimSpace(url)
	if strings.HasPrefix(u, "magnet:") {
		return torrent.TorrentSpecFromMagnetUri(u)
	}
	var data []byte
	if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		for k, vs := range headers {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("服务器返回 %s", resp.Status)
		}
		data, err = io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if err != nil {
			return nil, err
		}
	} else {
		p := strings.TrimPrefix(u, "file://")
		var err error
		if data, err = os.ReadFile(p); err != nil {
			return nil, fmt.Errorf("无法读取 %s: %w", p, err)
		}
	}
	if len(data) == 0 || data[0] != 'd' {
		return nil, errors.New("这个链接返回的不是 .torrent 文件")
	}
	mi, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("无法解析种子文件: %w", err)
	}
	return torrent.TorrentSpecFromMetaInfo(mi), nil
}

// saveTorrentFile 收前端上传的 .torrent(base64):校验是合法种子后落盘到
// 数据目录,返回服务器端路径。任务记下这个路径,重启后续传仍能读到;
// 后续 probe_torrent / add_download 直接把它当本地路径用。
func saveTorrentFile(dataDir, name, contentB64 string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(contentB64))
	if err != nil {
		return "", fmt.Errorf("文件内容解码失败: %w", err)
	}
	if len(data) == 0 {
		return "", errors.New("种子文件是空的")
	}
	if len(data) > 16<<20 {
		return "", errors.New("种子文件太大(超过 16MB)")
	}
	if data[0] != 'd' {
		return "", errors.New("不是有效的种子文件")
	}
	if _, err := metainfo.Load(bytes.NewReader(data)); err != nil {
		return "", fmt.Errorf("无法解析种子文件: %w", err)
	}

	// 文件名净化:去掉路径部分与控制字符,保留中文,超长截断。
	name = strings.ReplaceAll(strings.TrimSpace(name), "\\", "/")
	name = strings.TrimSuffix(filepath.Base(name), ".torrent")
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || r == '/' || r == 0x7f {
			continue
		}
		b.WriteRune(r)
		if b.Len() > 160 {
			break
		}
	}
	if cleaned := strings.Trim(b.String(), ". \t"); cleaned == "" {
		b.Reset()
		b.WriteString("torrent")
	} else {
		b.Reset()
		b.WriteString(cleaned)
	}

	dir := filepath.Join(dataDir, "torrent-files")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, fmt.Sprintf("%d_%s.torrent", time.Now().UnixMilli(), b.String()))
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return "", err
	}
	return p, nil
}

// 读出种子的名字与文件列表,不下载任何数据。磁力链接要先向节点要元数据,
// 可能要等一会儿。
func probeTorrent(ctx context.Context, client *http.Client, dataDir, url string, headers http.Header) (*TorrentInfo, error) {
	cl, _, err := btSession(dataDir)
	if err != nil {
		return nil, err
	}
	spec, err := resolveTorrentSpec(ctx, client, url, headers)
	if err != nil {
		return nil, err
	}
	spec = withPublicTrackers(spec, strings.HasPrefix(strings.TrimSpace(url), "magnet:"))
	// 探测不指定 Storage,用会话默认目录;没标记下载就不会写数据。
	// 同一 infohash 会拿到会话里已有的种子(比如正在下载的那个):
	// new=false 说明句柄不是我们的,读完信息绝不能 Drop——否则详情面板
	// 每次探测都会把运行中任务的节点全断掉。
	t, added, err := cl.AddTorrentSpec(spec)
	if err != nil {
		return nil, err
	}
	dropIfNew := func() {
		if added {
			dropTorrent(t)
		}
	}
	timeout := time.NewTimer(90 * time.Second)
	defer timeout.Stop()
	select {
	case <-t.GotInfo():
	case <-timeout.C:
		dropIfNew()
		return nil, errors.New("找不到分享此种子的人")
	case <-ctx.Done():
		dropIfNew()
		return nil, ctx.Err()
	}
	info := t.Info()
	out := &TorrentInfo{Name: info.Name, Size: t.Length()}
	for _, f := range t.Files() {
		out.Files = append(out.Files, TorrentFile{Path: strings.ReplaceAll(f.Path(), "\\", "/"), Size: f.Length()})
	}
	dropIfNew()
	return out, nil
}

// DHT 引导:引擎默认只向 router.bittorrent.com 等域名的 IP 报到,这些域名
// 在国内常被 DNS 污染,一个都连不上时磁力冷启动就只剩 tracker 一条腿,
// 头一两分钟往往只有十几 KB/s。这里异步把引导域名经 AliDNS 的 DoH 接口
// (国内可达,绕开本地污染)解析成真实 IP 直接喂给各 DHT 服务器;解析
// 失败无害,引擎自己的域名引导照常进行。SWIFTDL_DHT_NODES(逗号分隔的
// IP:端口)可自行追加。
var dhtBootstrapHosts = []string{
	"router.bittorrent.com:6881",
	"router.utorrent.com:6881",
	"dht.transmissionbt.com:6881",
	"dht.libtorrent.org:25401",
	"dht.aelitis.com:6881",
}

func seedDHT(cl *torrent.Client) {
	nodes := strings.Split(strings.TrimSpace(os.Getenv("SWIFTDL_DHT_NODES")), ",")
	go func() {
		defer func() { recover() }() // 引导失败不影响下载,只慢一点
		for _, hp := range dhtBootstrapHosts {
			host, port, err := net.SplitHostPort(hp)
			if err != nil {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			// 先走 DoH 绕污染;DoH 本身连不上(有些网络只放行系统 DNS)
			// 再退回普通解析,解析错了也只是 ping 不通的节点,无害。
			ips := resolveA(ctx, host)
			if len(ips) == 0 {
				ips, _ = net.DefaultResolver.LookupHost(ctx, host)
			}
			cancel()
			for _, ip := range ips {
				if net.ParseIP(ip) != nil {
					nodes = append(nodes, net.JoinHostPort(ip, port))
				}
			}
		}
		if len(nodes) > 0 {
			cl.AddDhtNodes(nodes)
			bt.dhtSeeded.Store(int32(len(nodes)))
		}
	}()
}

// 经国内可达的 DoH 解析 A 记录;应答形如 {"Answer":[{"type":1,"data":"1.2.3.4"}]}。
// 两家一起问、结果合并:实测同一域名两家会给出不同答案(有的真有的假),
// 多一路就多一分拿到真 IP 的机会。
var dohEndpoints = []string{
	"https://dns.alidns.com/resolve?name=%s&type=A",
	"https://doh.pub/dns-query?name=%s&type=A",
}

func resolveA(ctx context.Context, host string) []string {
	var out []string
	seen := map[string]bool{}
	for _, ep := range dohEndpoints {
		u := fmt.Sprintf(ep, url.QueryEscape(host))
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			continue
		}
		req.Header.Set("Accept", "application/dns-json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			continue
		}
		for _, ip := range parseDoHAnswer(data) {
			if !seen[ip] {
				seen[ip] = true
				out = append(out, ip)
			}
		}
	}
	return out
}

func parseDoHAnswer(data []byte) []string {
	var out struct {
		Answer []struct {
			Type int    `json:"type"`
			Data string `json:"data"`
		} `json:"Answer"`
	}
	if json.Unmarshal(data, &out) != nil {
		return nil
	}
	var ips []string
	for _, a := range out.Answer {
		if a.Type == 1 && net.ParseIP(a.Data) != nil {
			ips = append(ips, a.Data)
		}
	}
	return ips
}

// 公共 tracker:磁力链接常常一个 tracker 都不带,纯靠 DHT 找源起步很慢。
// 给磁力任务统一附一份常见公共 tracker,让引擎一开始就有地方要节点。
// 可用 SWIFTDL_EXTRA_TRACKERS(逗号分隔)替换。
var publicTrackers = []string{
	// TCP(http/https)tracker 排前面:不少家用网络会丢 UDP(DHT 和 UDP
	// tracker 全哑),TCP tracker 是这种环境下唯一还能找源的腿;以下均按
	// 局域网实测可达筛过。
	"http://tracker.opentrackr.org:1337/announce",
	"http://open.tracker.cl:1337/announce",
	"http://tracker.dler.org:6969/announce",
	"http://tracker2.dler.org:6969/announce",
	"http://tracker.qu.ax:6969/announce",
	"https://tracker.qu.ax:443/announce",
	"https://tr.burnabyhighstar.com:443/announce",
	"https://tr.ready4.icu:443/announce",
	// UDP tracker:网络放行 UDP 时更快(DHT/PEX 之外的补充来源)。
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://open.demonii.com:1337/announce",
	"udp://open.stealth.si:80/announce",
	"udp://tracker.torrent.eu.org:451/announce",
	"udp://exodus.desync.com:6969/announce",
	"udp://tracker.dler.org:6969/announce",
	"udp://explodie.org:6969/announce",
	"udp://tracker.tiny-vps.com:6969/announce",
	"udp://tracker.moeking.me:6969/announce",
	"udp://tracker.theoks.net:6969/announce",
	"udp://tracker.bittor.pw:1337/announce",
	"udp://tracker.hydrocode.eu:1337/announce",
	"udp://tracker.zerobytes.xyz:1337/announce",
	"udp://tracker.leech.ie:1337/announce",
	"udp://bt1.archive.org:6969/announce",
	"udp://bt2.archive.org:6969/announce",
}

// 磁力链接的任务附加公共 tracker(种子文件自带的不动,私有不碰)。
func withPublicTrackers(spec *torrent.TorrentSpec, magnet bool) *torrent.TorrentSpec {
	if !magnet {
		return spec
	}
	list := publicTrackers
	if env := strings.TrimSpace(os.Getenv("SWIFTDL_EXTRA_TRACKERS")); env != "" {
		list = strings.Split(env, ",")
	}
	spec.Trackers = append(spec.Trackers, list)
	return spec
}

// dropTorrent 安全丢弃:anacrolix 的 Torrent.Drop 在 torrent 已被丢弃(比如
// 做种协程先到一步)或客户端已关闭时会 panic("already closed"),把整个后端
// 带崩。丢弃后再丢弃一次就是想要的状态,吞掉 panic 即可。
func dropTorrent(t *torrent.Torrent) {
	defer func() { recover() }()
	t.Drop()
}

// 做种策略(设置里的一项):下载完立刻停 / 做到某个分享率 / 一直做。
type seedPolicy int

const (
	seedStop seedPolicy = iota
	seedRatio
	seedForever
)

func parseSeed(s string) (seedPolicy, float64) {
	if s == "forever" {
		return seedForever, 0
	}
	if r, err := strconv.ParseFloat(s, 64); err == nil && r > 0 {
		return seedRatio, r
	}
	return seedStop, 0
}

type torrentJob struct {
	URL     string
	Headers http.Header
	Dir     string // 种子数据落在这里(多文件种子自带名字子目录)
	Files   []int  // 选中的文件下标;空 = 全部
	Seed    string
	DataDir string
	// 重新下载:旧文件在添加时已删,但分片完成库仍记着旧记录。
	Overwrite bool
	// 重校做完的回调(清掉任务上的一次性标志,暂停恢复不再整盘重校)。
	OnOverwriteApplied func()
}

// 下载种子,进度写进 progress,状态短语写进 note。取消会停下并保留
// 已有数据;再跑一次会跳过磁盘上已完整的分片。
func torrentRun(ctx context.Context, client *http.Client, job torrentJob, progress *Progress, note *Note) (Outcome, error) {
	note.Set(pick(strings.HasPrefix(job.URL, "magnet:"), "正在获取种子信息", "正在启动"))
	cl, completion, err := btSession(job.DataDir)
	if err != nil {
		note.Set("")
		return OutcomePaused, err
	}
	if err := os.MkdirAll(job.Dir, 0o755); err != nil {
		note.Set("")
		return OutcomePaused, err
	}
	spec, err := resolveTorrentSpec(ctx, client, job.URL, job.Headers)
	if err != nil {
		note.Set("")
		return OutcomePaused, err
	}
	spec = withPublicTrackers(spec, strings.HasPrefix(strings.TrimSpace(job.URL), "magnet:"))
	spec.Storage = storage.NewFileWithCompletion(job.Dir, completion)
	t, _, err := cl.AddTorrentSpec(spec)
	if err != nil {
		note.Set("")
		return OutcomePaused, err
	}

	// 磁力链接在这里等元数据(任务会一直等,用户可暂停)。
	select {
	case <-t.GotInfo():
	case <-ctx.Done():
		dropTorrent(t)
		progress.Connections.Store(0)
		note.Set("")
		return OutcomePaused, nil
	}

	if job.Overwrite {
		// 分片完成库还记着旧文件的分片(文件已删),不校正的话新任务
		// 看起来 100% 而秒完成。VerifyData 按磁盘实况异步重写记录;文件
		// 不在校验秒失败,扫得很快。等它至少推翻一片或超时——已有数据
		// 完整有效时(用户没删成)超时走人是正确的续传。
		t.VerifyData()
		for i := 0; i < 200 && t.Stats().PiecesComplete >= t.NumPieces() && t.NumPieces() > 0; i++ {
			select {
			case <-ctx.Done():
			case <-time.After(50 * time.Millisecond):
			}
			if ctx.Err() != nil {
				break
			}
		}
		if job.OnOverwriteApplied != nil {
			job.OnOverwriteApplied()
		}
	}

	if len(job.Files) > 0 {
		files := t.Files()
		for i, f := range files {
			if slices.Contains(job.Files, i) {
				f.Download()
			}
		}
	} else {
		t.DownloadAll()
	}

	total := t.Length()
	progress.Total.Store(total)
	progress.Resumable.Store(true)

	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		done := t.BytesCompleted()
		stats := t.Stats()
		progress.Downloaded.Store(done)
		// 收包计数喂给速度显示差分(已完成字节是跳变的,见 Progress.Wire)。
		progress.Wire.Store(stats.BytesReadData.Int64())
		progress.Connections.Store(int64(stats.ActivePeers))
		switch {
		case done >= total:
			// 完成,离开循环处理做种。
		case stats.ActivePeers == 0:
			note.Set("正在寻找节点")
		case stats.ActivePeers == 1:
			note.Set("正在从 1 个节点下载")
		default:
			note.Set(fmt.Sprintf("正在从 %d 个节点下载 · %d 个做种", stats.ActivePeers, stats.ConnectedSeeders))
		}
		if total > 0 && done >= total {
			break
		}
		select {
		case <-ctx.Done():
			// 停止分享、放开文件;已下内容留在盘上,分片记录在 Bolt 库。
			dropTorrent(t)
			progress.Connections.Store(0)
			note.Set("")
			return OutcomePaused, nil
		case <-tick.C:
		}
	}

	progress.Connections.Store(0)
	progress.Downloaded.Store(total)
	note.Set("")
	policy, ratio := parseSeed(job.Seed)
	switch policy {
	case seedStop:
		dropTorrent(t)
	case seedRatio:
		// 下载到此为止;做种在后台,到分享率自动停。
		// torrent 可能已被暂停/删除路径先 Drop,Stats 也会断言,整个协程兜底。
		go func() {
			defer func() { recover() }()
			for {
				time.Sleep(5 * time.Second)
				s := t.Stats()
				if s.ActivePeers+s.TotalPeers == 0 || total == 0 ||
					float64(s.BytesWrittenData.Int64()) >= ratio*float64(total) {
					dropTorrent(t)
					return
				}
			}
		}()
	case seedForever:
		// 留在会话里,直到应用退出或用户暂停/删除任务。
	}
	return OutcomeCompleted, nil
}
