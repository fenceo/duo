package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// taskOpenURL can run beside the service: no data lock, migration, password
// changes, model calls or task-state writes. Regular browser login still applies.
func taskOpenURL(directory, address, id string) (string, error) {
	if !safeWorkbenchID(id) {
		return "", errors.New("任务 ID 无效")
	}
	if address == "" {
		var config struct {
			Listen string `json:"listen"`
		}
		raw, err := os.ReadFile(filepath.Join(directory, "config.json"))
		if err != nil || json.Unmarshal(raw, &config) != nil {
			return "", errors.New("无法读取已有 Duo 配置，请检查 --data 数据目录")
		}
		address = config.Listen
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", errors.New("Duo 监听地址无效")
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return "", errors.New("Duo 监听端口无效")
	}
	ip := net.ParseIP(host)
	if host != "" && host != "localhost" && ip == nil {
		return "", errors.New("Duo 监听地址必须为本机 IP 或 localhost")
	}
	if host == "" || ip != nil && ip.IsUnspecified() {
		host = "127.0.0.1"
	}
	path, err := filepath.Abs(filepath.Join(directory, "jianzuo.db"))
	if err != nil {
		return "", err
	}
	if info, e := os.Stat(path); e != nil || !info.Mode().IsRegular() {
		return "", errors.New("Duo 数据库不存在，没有初始化或创建新任务")
	}
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	database := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", database.String())
	if err != nil {
		return "", errors.New("无法只读打开 Duo 数据库")
	}
	defer db.Close()
	var deleted bool
	err = db.QueryRow("SELECT COALESCE(o.deleted,0) FROM tasks t LEFT JOIN task_options o ON o.task_id=t.id WHERE t.id=?", id).Scan(&deleted)
	if err != nil || deleted {
		return "", errors.New("任务不存在或已删除，没有新建、恢复或重置任务")
	}
	u := url.URL{Scheme: "http", Host: net.JoinHostPort(host, port), Path: "/", RawQuery: url.Values{"task": {id}}.Encode()}
	return u.String(), nil
}

func taskOpenCommand(executable, directory, id, address string) string {
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	prefix := "& "
	if runtime.GOOS != "windows" {
		quote = func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
		prefix = ""
	}
	return fmt.Sprintf("%s%s --data %s --open-task %s --listen %s", prefix, quote(executable), quote(directory), quote(id), quote(address))
}
