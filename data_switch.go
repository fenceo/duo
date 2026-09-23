package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type dataSwitchRequest struct {
	Protocol    int    `json:"protocol"`
	Token       string `json:"token"`
	OldData     string `json:"old_data"`
	NewData     string `json:"new_data"`
	Listen      string `json:"listen"`
	CreatedUnix int64  `json:"created_unix"`
}

func localDataPath(value string) (string, error) {
	path := strings.TrimSpace(value)
	if path == "" || !filepath.IsAbs(path) {
		return "", errors.New("数据目录需要本机绝对路径")
	}
	path = filepath.Clean(path)
	if strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, `//`) {
		return "", errors.New("暂不支持网络共享路径，请使用本机磁盘目录")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		if !os.IsPermission(err) || dataPathHasReparse(path) {
			return "", errors.New("数据目录不存在或包含符号链接，请先完整复制到本机目录")
		}
	} else if !strings.EqualFold(filepath.Clean(resolved), path) {
		return "", errors.New("数据目录不存在或包含符号链接，请先完整复制到本机目录")
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", errors.New("目标数据目录不存在或不是文件夹")
	}
	return path, nil
}

func dataListen(dir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return "", err
	}
	var config Config
	if err := json.Unmarshal(raw, &config); err != nil {
		return "", err
	}
	return config.Listen, nil
}

func newSwitchToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func writeDataSwitchRequest(path string, request dataSwitchRequest) error {
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%s.tmp", path, request.Token)
	if err := os.WriteFile(tmp, raw, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (s *Server) switchDataDirectory(w http.ResponseWriter, r *http.Request) {
	if s.launcherPID <= 0 {
		fail(w, 409, "请通过 Duo.exe 启动后再切换数据目录；直接运行服务不支持此操作")
		return
	}
	var input struct {
		DataDir         string `json:"data_dir"`
		ExpectedCurrent string `json:"expected_current"`
		Confirm         bool   `json:"confirm"`
	}
	if !body(w, r, &input) {
		return
	}
	current, err := localDataPath(s.dataDir)
	if err != nil {
		fail(w, 500, "当前数据目录不可用："+err.Error())
		return
	}
	if expected := strings.TrimSpace(input.ExpectedCurrent); expected != "" && !strings.EqualFold(filepath.Clean(expected), current) {
		fail(w, 409, "当前数据目录已变化，请刷新设置后重试")
		return
	}
	if !input.Confirm {
		fail(w, 400, "请确认切换到已有数据目录；不会复制或删除任何数据")
		return
	}
	target, err := localDataPath(input.DataDir)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if strings.EqualFold(target, current) {
		fail(w, 400, "目标目录已经是当前数据目录")
		return
	}
	if pathWithin(s.updateRoot, target) || pathWithin(current, target) || pathWithin(target, current) {
		fail(w, 400, "目标数据目录不能位于程序目录或当前数据目录内部")
		return
	}
	if !s.modelProbeMu.TryLock() {
		fail(w, http.StatusConflict, "已有模型读取或测试正在进行，请稍后重试")
		return
	}
	defer s.modelProbeMu.Unlock()
	if !s.updateMu.TryLock() {
		fail(w, http.StatusConflict, "已有更新操作正在进行，请稍后重试")
		return
	}
	defer s.updateMu.Unlock()
	if err := validateExistingData(target); err != nil {
		fail(w, 400, err.Error())
		return
	}
	targetListen, err := dataListen(target)
	if err != nil {
		fail(w, 400, "目标数据配置无法读取："+err.Error())
		return
	}
	if targetListen != s.app.config.get().Listen {
		fail(w, 400, "目标数据目录的监听地址必须与当前相同；请先修改目标 config.json 的 listen")
		return
	}
	targetRelease, err := lockData(target)
	if err != nil {
		fail(w, 409, "目标数据目录正在被另一份 Duo 使用，请先退出后重试")
		return
	}
	// Keep the target lock until this service exits; the launcher will start
	// the replacement only after the old process has released it.
	release, err := s.app.beginUpdate()
	if err != nil {
		targetRelease()
		fail(w, 409, err.Error())
		return
	}
	request := dataSwitchRequest{Protocol: 1, Token: newSwitchToken(), OldData: current, NewData: target, Listen: s.app.config.get().Listen, CreatedUnix: time.Now().Unix()}
	if err := writeDataSwitchRequest(s.switchRequest, request); err != nil {
		targetRelease()
		release()
		fail(w, 500, "无法准备数据目录切换："+err.Error())
		return
	}
	if s.shutdown == nil {
		_ = os.Remove(s.switchRequest)
		release()
		targetRelease()
		fail(w, 500, "当前启动方式没有可用的重启入口")
		return
	}
	// The launcher owns the stop/restart transaction. Keep the update gate
	// closed while the service exits so no new task can enter the old store.
	fmt.Println("JIANZUO_SWITCH")
	jsonOut(w, 202, map[string]any{"state": "scheduled", "message": "已校验目标目录，Duo 将安全重启并载入它；原数据不会删除"})
	go func() {
		time.Sleep(150 * time.Millisecond)
		s.shutdown()
	}()
}
