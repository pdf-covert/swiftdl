// ed2k://(电驴)链接,走 goed2k(纯 Go 的 eDonkey/eMule 客户端库,MIT)。
//
// 与 BT 同一套任务模型:链接里就带文件名、大小和 MD4 哈希,所以不用探测;
// 数据先落到数据目录的临时目录里(引擎按链接里的文件名写),完成后搬去
// 任务的目标路径(允许改名/防重名)。暂停 = 句柄断开但留在会话里,
// 分片进度不丢;恢复 = 同一链接再加一次(按哈希幂等)后 Resume。
// 应用重启后引擎里的分片表没了,续传会重头覆盖写同一份临时文件。

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/monkeyWie/goed2k"
)

// 是不是 ed2k 链接(协议头不区分大小写,解析器本身只认小写)。
func isED2KURL(url string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(url)), "ed2k://")
}

// 解析 ed2k 文件链接:文件名、大小。非文件链接(服务器/节点表)报错。
func parseED2KLink(url string) (name string, size int64, err error) {
	u := strings.TrimSpace(url)
	// 协议头不区分大小写,解析器只认小写,统一换掉。
	if strings.HasPrefix(strings.ToLower(u), "ed2k://") {
		u = "ed2k://" + u[len("ed2k://"):]
	}
	link, perr := goed2k.ParseEMuleLink(u)
	if perr != nil {
		return "", 0, fmt.Errorf("这不是有效的 ed2k 链接: %w", perr)
	}
	if link.Type != goed2k.LinkFile {
		return "", 0, errors.New("只支持 ed2k 文件链接")
	}
	return link.StringValue, link.NumberValue, nil
}

// 公共 ed2k 服务器。社区列表一直在变,所以这里只是种子,启动后还会
// 尽力拉一份在线 server.met 补充;可用 SWIFTDL_ED2K_SERVERS 覆盖。
var ed2kSeedServers = []string{
	"176.123.5.89:4725",
	"85.17.116.222:6082",
	"91.208.162.87:4232",
}

// 全应用一个 ed2k 会话,首次使用时启动。
var ed2k struct {
	once   sync.Once
	client *goed2k.Client
	err    error
}

func ed2kSession(dataDir string) (*goed2k.Client, error) {
	ed2k.once.Do(func() {
		st := goed2k.NewSettings()
		st.ClientName = "SwiftDL"
		st.ReconnectToServer = true
		st.EnableDHT = true // KAD 找源,服务器连不上时尤其重要
		// 避开 eMule 的默认端口,减少和 NAS 上其他电驴应用的冲突。
		st.ListenPort = 15466
		st.UDPPort = 15467
		c := goed2k.NewClient(st)
		if err := c.Start(); err != nil {
			ed2k.err = fmt.Errorf("电驴引擎启动失败: %w", err)
			return
		}
		go ed2kConnect(c)
		ed2k.client = c
	})
	return ed2k.client, ed2k.err
}

// 连服务器、拉在线列表,全是尽力而为:连不上只影响找源速度,任务照常排队等。
// KAD 不再自动引导:公共 nodes.dat 引导源(2026-11 实测)已全部死亡——
// nodes.emule-project.org 证书已不匹配该域名,peerates/emule-security 的
// nodes.dat 要么 401 要么变成 HTML 页,没有节点表 KAD 起不来;且 KAD 是
// 纯 UDP,不少家庭网络对 UDP 按目标丢弃。找源主力是服务器(TCP),够用。
// 想启用 KAD 的可用 SWIFTDL_ED2K_KAD_NODES=ip:端口[,ip:端口…] 自备引导节点。
func ed2kConnect(c *goed2k.Client) {
	servers := ed2kSeedServers
	if env := strings.TrimSpace(os.Getenv("SWIFTDL_ED2K_SERVERS")); env != "" {
		servers = strings.Split(env, ",")
	}
	for _, s := range servers {
		if s = strings.TrimSpace(s); s != "" {
			_ = c.ConnectServers(s)
		}
	}
	_ = c.ConnectServerMet("https://gruk.org/server.met")
	if env := strings.TrimSpace(os.Getenv("SWIFTDL_ED2K_KAD_NODES")); env != "" {
		for _, n := range strings.Split(env, ",") {
			if n = strings.TrimSpace(n); n != "" {
				_ = c.AddDHTBootstrapNodes(n)
			}
		}
	}
}

// 每个任务的临时目录(按任务 id,同链接的重复任务各下各的)。
func ed2kTempDir(dataDir string, id uint64) string {
	return filepath.Join(dataDir, "ed2k-tmp", strconv.FormatUint(id, 10))
}

// 把任务从引擎里摘掉(删除任务/收尾时用);引擎还没启动就算了。
func ed2kForget(url string) {
	if ed2k.client == nil {
		return
	}
	u := strings.TrimSpace(url)
	if strings.HasPrefix(strings.ToLower(u), "ed2k://") {
		u = "ed2k://" + u[len("ed2k://"):]
	}
	if link, err := goed2k.ParseEMuleLink(u); err == nil && link.Type == goed2k.LinkFile {
		// Abort 会关文件句柄;与收尾路径并发时库内部有锁,再兜一层底。
		defer func() { recover() }()
		_ = ed2k.client.RemoveTransfer(link.Hash, false)
	}
}

type ed2kJob struct {
	URL     string
	Dest    string // 完成后搬到这里(已按防重名规则挑好)
	DataDir string
	TempDir string // 引擎写数据的位置
}

func ed2kRun(ctx context.Context, job ed2kJob, progress *Progress, note *Note) (Outcome, error) {
	name, size, err := parseED2KLink(job.URL)
	if err != nil {
		return OutcomePaused, err
	}
	note.Set("正在连接 eDonkey 网络")
	c, err := ed2kSession(job.DataDir)
	if err != nil {
		note.Set("")
		return OutcomePaused, err
	}
	if err := os.MkdirAll(job.TempDir, 0o755); err != nil {
		note.Set("")
		return OutcomePaused, err
	}
	handle, _, err := c.AddLink(job.URL, job.TempDir)
	if err != nil {
		note.Set("")
		return OutcomePaused, fmt.Errorf("加入电驴任务失败: %w", err)
	}
	// 同一哈希的旧句柄可能带着暂停态(暂停过/删过又加),先确保在跑。
	handle.Resume()

	progress.Total.Store(size)
	progress.Resumable.Store(true)

	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	started := time.Now()
	for {
		handle = c.FindTransfer(handle.GetHash())
		if !handle.IsValid() {
			// 被删除任务等路径摘掉了:按暂停收场,别挂着假进度。
			note.Set("")
			progress.Connections.Store(0)
			return OutcomePaused, nil
		}
		st := handle.GetStatus()
		progress.Downloaded.Store(st.TotalDone)
		progress.Connections.Store(int64(st.NumPeers))
		switch {
		case st.TotalWanted > 0 && st.TotalDone >= st.TotalWanted:
			// 完成:摘掉句柄(关文件)后把文件搬到目标位置。
			note.Set("正在收尾")
			progress.Connections.Store(0)
			progress.Downloaded.Store(size)
			ed2kForget(job.URL)
			if err := moveIntoPlace(filepath.Join(job.TempDir, name), job.Dest); err != nil {
				note.Set("")
				return OutcomePaused, fmt.Errorf("下载完成,但归置文件失败: %w", err)
			}
			os.RemoveAll(job.TempDir)
			note.Set("")
			return OutcomeCompleted, nil
		case st.NumPeers == 0:
			// 找了半天还零来源:电驴网络上没人分享就是没人分享
			//(服务器已连上、搜索都正常,2016 年的旧文件常见这种),
			// 把话挑明,别让用户以为卡死了。
			if time.Since(started) > 5*time.Minute {
				note.Set("正在寻找来源 · 暂无来源,该文件可能已无人分享")
			} else {
				note.Set("正在寻找来源")
			}
		case st.NumPeers == 1:
			note.Set("正在从 1 个来源下载")
		default:
			note.Set(fmt.Sprintf("正在从 %d 个来源下载", st.NumPeers))
		}
		select {
		case <-ctx.Done():
			// 句柄留在会话里,分片进度保住;文件在临时目录等下次。
			handle.Pause()
			progress.Connections.Store(0)
			note.Set("")
			return OutcomePaused, nil
		case <-tick.C:
		}
	}
}

// 引擎写完的文件搬到任务目标位置:同盘改名,跨盘退化成复制。
func moveIntoPlace(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}
