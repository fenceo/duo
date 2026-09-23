package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// This installer-only check must run before loadConfig/openStore: those paths
// initialize files, migrate schemas and interrupt persisted runs. The caller
// holds the data-directory guard throughout selection and installation.
func validateExistingData(dir string) error {
	if !filepath.IsAbs(dir) {
		return errors.New("数据目录需要本机绝对路径")
	}
	dir = filepath.Clean(dir)
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		// Some restricted Windows runners deny the final-handle query used by
		// EvalSymlinks even for an ordinary directory. Fall back to Lstat for
		// every component; a reparse/symlink is still rejected.
		if !errors.Is(err, os.ErrPermission) || dataPathHasReparse(dir) {
			return errors.New("数据目录不存在或包含符号链接，请使用实际本机目录")
		}
	} else if !strings.EqualFold(resolved, dir) {
		return errors.New("数据目录不存在或包含符号链接，请使用实际本机目录")
	}
	configPath, databasePath := filepath.Join(dir, "config.json"), filepath.Join(dir, "jianzuo.db")
	for _, path := range []string{configPath, databasePath} {
		st, err := os.Lstat(path)
		if err != nil || !st.Mode().IsRegular() {
			return errors.New("此目录缺少有效的 config.json 或 jianzuo.db，不是可载入的 Duo 数据")
		}
		if st.Mode().Perm()&0200 == 0 {
			return errors.New("数据配置或数据库带有只读属性，启动后无法保存；请先恢复可写的数据副本再载入")
		}
	}
	st, _ := os.Stat(configPath)
	if st.Size() > 4<<20 {
		return errors.New("数据配置过大，未载入")
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return errors.New("无法读取数据配置")
	}
	var config Config
	if json.Unmarshal(raw, &config) != nil {
		return errors.New("config.json 不是有效的 Duo 配置")
	}
	_, port, err := net.SplitHostPort(config.Listen)
	n, numberErr := strconv.Atoi(port)
	if err != nil || numberErr != nil || n < 1 || n > 65535 {
		return errors.New("数据配置的监听地址无效，请先在原安装中修复")
	}
	if normalizeAccess(&config.Access) != nil || normalizeEnvironments(&config) != nil {
		return errors.New("数据配置的执行环境或访问地址无效，请先在原安装中修复")
	}
	if config.Feishu.Enabled && (config.Feishu.AppID == "" || config.Feishu.Secret == "") {
		return errors.New("数据配置中的飞书设置不完整，请先在原安装中修复")
	}
	// immutable avoids even creating SQLite -shm files. Never ignore WAL or a
	// recovery journal: require the source app to checkpoint by exiting first.
	for _, suffix := range []string{"-wal", "-journal"} {
		st, err := os.Lstat(databasePath + suffix)
		if err == nil && (!st.Mode().IsRegular() || st.Size() > 0) {
			return errors.New("数据尚未完整落盘，请先正常退出原 Duo 后再载入；不要删除 WAL 或日志文件")
		}
		if err != nil && !os.IsNotExist(err) {
			return errors.New("无法检查数据库日志，未载入")
		}
	}
	path := filepath.ToSlash(databasePath)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&immutable=1"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return errors.New("无法只读打开 Duo 数据库")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check(1)").Scan(&integrity); err != nil || integrity != "ok" {
		return errors.New("数据库损坏或校验超时，未载入；请保留原数据进行恢复")
	}
	required := map[string][]string{
		"settings": {"key", "value"},
		"tasks":    {"id", "title", "workspace", "model", "session", "status", "created", "updated"},
		"runs":     {"id", "task_id", "input", "kind", "source", "status", "result", "error", "created", "finished"},
		"events":   {"seq", "task_id", "run_id", "kind", "text", "created"},
	}
	for table, columns := range required {
		var kind string
		if err := db.QueryRowContext(ctx, "SELECT type FROM sqlite_master WHERE name=?", table).Scan(&kind); err != nil || kind != "table" {
			return errors.New("数据库不是受支持的 Duo 数据结构，未载入")
		}
		rows, err := db.QueryContext(ctx, "SELECT "+strings.Join(columns, ",")+" FROM "+table+" LIMIT 0")
		if err != nil {
			return errors.New("Duo 数据库缺少必要字段，未载入")
		}
		rows.Close()
	}
	var passwordHash string
	if err := db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='password_hash'").Scan(&passwordHash); err != nil {
		return errors.New("数据目录未完成密码初始化，不能作为已有数据载入")
	}
	if _, err := bcrypt.Cost([]byte(passwordHash)); err != nil {
		return errors.New("已有登录密码记录无效，未载入")
	}
	for _, table := range []string{"tasks", "runs"} {
		var active int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE status IN ('running','queued')").Scan(&active); err != nil || active != 0 {
			return fmt.Errorf("已有数据仍有未结束的任务，请在原 Duo 中处理后正常退出再载入（%s）", table)
		}
	}
	return nil
}

func dataPathHasReparse(dir string) bool {
	for current := filepath.Clean(dir); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false
		}
	}
}
