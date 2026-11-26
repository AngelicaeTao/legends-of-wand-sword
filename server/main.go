// 杖剑传说初跑版 · 服务端权威逻辑。
// 两种运行模式：
//
//	standalone：独立 HTTP 服务，同源托管前端 + API；
//	nakama：作为 Nakama Go RPC 插件（需 nakama-common），由 Nakama 加载。
//
// 两种模式共用 internal/game 与 internal/store，逻辑唯一。
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"zjcs/server/internal/net"
)

func main() {
	addr := flag.String("addr", "", "HTTP 监听地址（默认读取 PORT 环境变量，未设置则 :8080）")
	cfgDir := flag.String("config", "configs", "配置目录")
	webFile := flag.String("web", "", "前端单文件页面路径（非空则由 / 同源托管）")
	resDir := flag.String("res", "", "静态资源目录（非空则由 /res/ 托管）")
	flag.Parse()

	listen := *addr
	if listen == "" {
		if p := os.Getenv("PORT"); p != "" {
			listen = ":" + p
		} else {
			listen = ":8080"
		}
	}
	if _, err := os.Stat(*cfgDir); os.IsNotExist(err) {
		log.Fatalf("配置目录不存在: %s（请用 -config 指向 configs/）", *cfgDir)
	}
	log.Printf("权威服务启动 addr=%s config=%s web=%q res=%q", listen, *cfgDir, *webFile, *resDir)
	srv := &http.Server{Addr: listen, Handler: net.HandlerWithWeb(*cfgDir, *webFile, *resDir)}
	fmt.Printf("listening on %s\n", listen)
	// v1.9.41 优雅关闭：退出前把异步去抖落盘的存档全部刷盘（防丢最近 400ms 变更）
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		log.Println("收到退出信号，刷新存档…")
		if err := net.FlushSaves(); err != nil {
			log.Printf("刷盘异常: %v", err)
		}
		srv.Close()
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
	log.Println("服务已退出")
}
