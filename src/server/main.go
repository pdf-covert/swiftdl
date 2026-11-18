// swiftdl-server:fnOS 上的下载服务。监听 Unix 套接字,fnOS 网关把
// /app/swiftdl 转发进来;前端是同一进程供出的静态单页应用。

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	sock := flag.String("sock", "", "Unix 套接字路径(fnOS 网关用它转发)")
	www := flag.String("www", "www", "前端静态文件目录")
	data := flag.String("data", "", "数据目录(任务列表、设置、BT 状态)")
	base := flag.String("base", "/app/swiftdl", "网关前缀")
	downloads := flag.String("downloads", "", "默认下载目录")
	flag.Parse()

	if *data == "" {
		log.Fatal("要用 -data 指定数据目录")
	}
	// -downloads 只作为开发便利的默认值;fnOS 上不传,
	// 下载目录由用户在授权文件夹里主动选定。

	mgr, err := newManager(*data, *downloads)
	if err != nil {
		log.Fatalf("启动失败: %v", err)
	}
	if dir := mgr.settings_().DownloadDir; dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Printf("提示:下载目录暂不可用(%v),用户可在设置里重选", err)
		}
	}

	mux := http.NewServeMux()
	api := &apiServer{m: mgr, base: *base}
	api.register(mux)
	serveStatic(mux, *base, *www)

	srv := &http.Server{Handler: mux}
	ln, err := listen(*sock)
	if err != nil {
		log.Fatalf("监听失败: %v", err)
	}

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Fatalf("服务出错: %v", err)
		}
	}()
	mgr.startTicker()
	log.Printf("swiftdl %s 就绪(套接字 %s,数据 %s)", appVersion, *sock, *data)

	// fnOS 停应用发 SIGTERM:收尾存盘再退出。
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	<-stop
	log.Print("正在退出,保存状态…")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	mgr.save()
}

func listen(sock string) (net.Listener, error) {
	if sock == "" {
		// 本机调试:直接监听端口。
		return net.Listen("tcp", "127.0.0.1:8642")
	}
	if err := os.Remove(sock); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("清不掉旧套接字: %w", err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(sock, 0o666); err != nil {
		log.Printf("套接字权限没放开(网关可能连不上): %v", err)
	}
	return ln, nil
}
