package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	exe, _ := os.Executable()
	dir := flag.String("data", filepath.Join(filepath.Dir(exe), "data"), "独立数据目录")
	initPassword := flag.Bool("init-password-stdin", false, "从标准输入设置登录密码并退出")
	listen := flag.String("listen", "", "覆盖监听地址")
	portableInit := flag.Bool("portable-init", false, "从 stdin 初始化全新便携数据目录")
	managed := flag.Bool("managed", false, "由托盘通过 stdin 管理生命周期")
	showVersion := flag.Bool("version", false, "显示版本")
	discover := flag.Bool("detect-environments", false, "只检测本机环境并输出 JSON，不创建数据目录")
	updateHelper := flag.Bool("update-helper", false, "执行已准备的便携版替换并退出")
	updateRoot := flag.String("update-root", "", "自动更新目标程序目录")
	updateStage := flag.String("update-stage", "", "自动更新暂存目录")
	updateVersion := flag.String("update-version", "", "自动更新版本")
	launcherPID := flag.Int("launcher-pid", 0, "等待退出的便携版启动器进程")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	if *discover {
		if e := printDetectedEnvironments(); e != nil {
			log.Fatal(e)
		}
		return
	}
	if *updateHelper {
		if e := runUpdateHelper(*dir, *updateRoot, *updateStage, *updateVersion, *launcherPID); e != nil {
			log.Fatal(e)
		}
		return
	}
	if e := os.MkdirAll(*dir, 0700); e != nil {
		log.Fatal(e)
	}
	release, e := lockData(*dir)
	if e != nil {
		log.Fatal(e)
	}
	defer release()
	if *portableInit {
		if e = initializePortable(*dir, os.Stdin); e != nil {
			log.Fatal(e)
		}
		return
	}
	c, e := loadConfig(*dir)
	if e != nil {
		log.Fatal(e)
	}
	address := c.get().Listen
	if *listen != "" {
		address = *listen
	}
	var listener net.Listener
	if !*initPassword {
		listener, e = net.Listen("tcp", address)
		if e != nil {
			log.Fatal(e)
		}
		defer listener.Close()
	}
	s, e := openStore(*dir)
	if e != nil {
		log.Fatal(e)
	}
	defer s.Close()
	logFile, logErr := os.OpenFile(filepath.Join(*dir, "service.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if logErr == nil {
		defer logFile.Close()
		log.SetOutput(logFile)
	}
	if *initPassword {
		b, e := io.ReadAll(io.LimitReader(os.Stdin, 1024))
		if e != nil {
			log.Fatal(e)
		}
		password := strings.TrimSpace(string(b))
		if len(password) < 6 || len(password) > 72 {
			log.Fatal("密码长度须为 6–72 字节")
		}
		encoded, e := bcrypt.GenerateFromPassword([]byte(password), 12)
		if e != nil {
			log.Fatal(e)
		}
		if e = s.set("password_hash", string(encoded)); e != nil {
			log.Fatal(e)
		}
		_, _ = s.Exec("DELETE FROM sessions")
		fmt.Println("登录密码已设置（仅保存哈希）")
		return
	}
	if s.setting("password_hash") == "" {
		log.Fatal("请先运行 --init-password-stdin 设置登录密码")
	}
	if e = s.pinLegacyEnvironment(c.get()); e != nil {
		log.Fatal(e)
	}
	a := newApp(s, c, CodexRunner{})
	a.hardwareAddress = listener.Addr().String()
	f := newFeishu(a)
	f.restart()
	a.wg.Add(1)
	go func() { defer a.wg.Done(); f.outboxLoop() }()
	a.wg.Add(1)
	go func() { defer a.wg.Done(); f.runCardsLoop() }()
	done := make(chan os.Signal, 1)
	handler := &Server{
		app:         a,
		updateRoot:  filepath.Dir(exe),
		launcherPID: func() int {
			if *managed {
				return os.Getppid()
			}
			return 0
		}(),
		shutdown: func() {
			select {
			case done <- os.Interrupt:
			default:
			}
		},
	}
	server := &http.Server{Addr: address, Handler: handler.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	signal.Notify(done, os.Interrupt)
	if *managed {
		go func() {
			scanner := bufio.NewScanner(os.Stdin)
			for scanner.Scan() {
				if scanner.Text() == "stop" {
					break
				}
			}
			done <- os.Interrupt
		}()
	}
	go func() {
		<-done
		a.cancel()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	log.Printf("简作 %s：http://%s", version, address)
	e = server.Serve(listener)
	a.close()
	if e != nil && e != http.ErrServerClosed {
		log.Fatal(e)
	}
}
