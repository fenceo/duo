package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go.bug.st/serial"
	"golang.org/x/crypto/ssh"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Connections belong to the service host, never the browser or Codex environment.
type HardwareConfig struct {
	ID          string       `json:"id"`
	Kind        string       `json:"kind"`
	Relay       *RelayConfig `json:"relay,omitempty"`
	Name        string       `json:"name"`
	Protocol    string       `json:"protocol"`
	Device      string       `json:"device"`
	Baud        int          `json:"baud"`
	Host        string       `json:"host"`
	Port        int          `json:"port"`
	User        string       `json:"user"`
	Fingerprint string       `json:"fingerprint"`
	ReadOnly    bool         `json:"read_only"`
	DataBits    int          `json:"data_bits"`
	Parity      string       `json:"parity"`
	StopBits    string       `json:"stop_bits"`
	DTR         *bool        `json:"dtr"`
	RTS         *bool        `json:"rts"`
}
type HardwareEvent struct {
	Seq       int64  `json:"seq"`
	TaskID    string `json:"task_id"`
	Direction string `json:"direction"`
	Text      string `json:"text"`
	Hex       string `json:"hex"`
	Created   int64  `json:"created"`
}
type hardwareLink struct {
	conn       io.ReadWriteCloser
	key        string
	readonly   bool
	writeMu    sync.Mutex
	generation string
	controller string
	relay      *relaySession
}
type Hardware struct {
	mu       sync.Mutex
	links    map[string]*hardwareLink
	store    *Store
	closed   bool
	wg       sync.WaitGroup
	notifyMu sync.Mutex
	notify   chan struct{}
}

func newHardware(s *Store) *Hardware {
	return &Hardware{store: s, links: map[string]*hardwareLink{}, notify: make(chan struct{})}
}
func (h *Hardware) close() {
	h.mu.Lock()
	h.closed = true
	for id, l := range h.links {
		l.conn.Close()
		delete(h.links, id)
	}
	h.mu.Unlock()
	h.wg.Wait()
}
func (h *Hardware) record(id, dir string, data []byte) { h.recordFor(id, dir, data, "") }
func (h *Hardware) recordFor(id, dir string, data []byte, task string) {
	tx, err := h.store.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	result, err := tx.Exec("INSERT INTO hardware_io(hardware_id,direction,data,created) VALUES(?,?,?,?)", id, dir, hex.EncodeToString(data), now())
	if err != nil {
		return
	}
	if task != "" {
		seq, _ := result.LastInsertId()
		if _, err = tx.Exec("INSERT INTO hardware_io_tasks(seq,task_id) VALUES(?,?)", seq, task); err != nil {
			return
		}
	}
	_, err = tx.Exec("DELETE FROM hardware_io WHERE hardware_id=? AND seq < COALESCE((SELECT seq FROM hardware_io WHERE hardware_id=? ORDER BY seq DESC LIMIT 1 OFFSET 1999),0)", id, id)
	if err != nil {
		return
	}
	if tx.Commit() != nil {
		return
	}
	h.notifyMu.Lock()
	close(h.notify)
	h.notify = make(chan struct{})
	h.notifyMu.Unlock()
}
func (h *Hardware) disconnect(id string) {
	h.mu.Lock()
	l := h.links[id]
	delete(h.links, id)
	h.mu.Unlock()
	if l != nil {
		l.conn.Close()
		h.recordFor(id, "status", []byte("已断开"), l.controller)
	}
}
func (h *Hardware) connect(c HardwareConfig, password string) error {
	return h.connectOwned(c, password, "")
}
func (h *Hardware) connectOwned(c HardwareConfig, password, task string) error {
	if err := validateRelay(c); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return errors.New("服务正在关闭")
	}
	if l := h.links[c.ID]; l != nil {
		if l.controller != "" && l.controller != task {
			return errors.New("设备由其他任务控制，请查看状态后接管")
		}
		return nil
	}
	key := c.Protocol + ":" + strings.ToLower(c.Host) + ":" + strconv.Itoa(c.Port)
	if c.Protocol == "serial" {
		key = "serial:" + strings.ToUpper(c.Device)
	}
	for _, l := range h.links {
		if l.key == key {
			return errors.New("该设备已被另一连接占用，请先断开")
		}
	}
	var conn io.ReadWriteCloser
	var err error
	switch c.Protocol {
	case "serial":
		var mode *serial.Mode
		mode, err = serialMode(c)
		if err == nil {
			conn, err = serial.Open(c.Device, mode)
		}
		if err != nil {
			return fmt.Errorf("无法打开 %s：%v。请检查设备是否插入，以及是否被其他串口工具占用", c.Device, err)
		}
	case "tcp", "telnet":
		var n net.Conn
		n, err = net.DialTimeout("tcp", net.JoinHostPort(c.Host, strconv.Itoa(c.Port)), 6*time.Second)
		if err == nil {
			if c.Protocol == "telnet" {
				conn = &telnetStream{Conn: n}
			} else {
				conn = n
			}
		}
	case "ssh":
		conn, err = openHardwareSSH(c, password)
	default:
		return errors.New("协议无效")
	}
	if err != nil {
		return err
	}
	l := &hardwareLink{conn: conn, key: key, readonly: c.ReadOnly, generation: uid(), controller: task}
	if c.Kind == "relay" {
		l.relay = newRelaySession(*c.Relay)
	}
	h.links[c.ID] = l
	h.recordFor(c.ID, "status", []byte("已连接"), task)
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		buf := make([]byte, 4096)
		for {
			n, e := conn.Read(buf)
			if n > 0 {
				if l.relay != nil {
					l.relay.receive(buf[:n])
				}
				h.mu.Lock()
				owner := l.controller
				h.mu.Unlock()
				h.recordFor(c.ID, "rx", buf[:n], owner)
			}
			if e != nil {
				break
			}
			if n == 0 {
				time.Sleep(20 * time.Millisecond)
			}
		}
		conn.Close()
		h.mu.Lock()
		same := h.links[c.ID] == l
		if same {
			delete(h.links, c.ID)
		}
		h.mu.Unlock()
		if same {
			h.recordFor(c.ID, "status", []byte("设备连接已关闭"), task)
		}
	}()
	return nil
}
func (h *Hardware) send(id string, b []byte) error {
	return h.sendConnection(id, "", b)
}
func (h *Hardware) sendConnection(id, generation string, b []byte) error {
	return h.sendOwned(id, generation, "", b)
}
func (h *Hardware) sendOwned(id, generation, task string, b []byte) error {
	h.mu.Lock()
	l := h.links[id]
	if l == nil {
		h.mu.Unlock()
		return errors.New("设备尚未连接")
	}
	l.writeMu.Lock()
	if generation != "" && generation != l.generation {
		l.writeMu.Unlock()
		h.mu.Unlock()
		return errors.New("设备连接或控制权已变化，请重新确认后发送")
	}
	if task != "" && l.controller != task {
		l.writeMu.Unlock()
		h.mu.Unlock()
		return errors.New("当前任务没有控制权")
	}
	owner := l.controller
	h.mu.Unlock()
	defer l.writeMu.Unlock()
	if l.readonly {
		return errors.New("当前连接只读，请断开后修改访问方式")
	}
	if len(b) == 0 || len(b) > 65536 {
		return errors.New("单次发送限 1–65536 字节")
	}
	if c, ok := l.conn.(interface{ SetWriteDeadline(time.Time) error }); ok {
		c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	}
	n, e := l.conn.Write(b)
	if n > 0 {
		h.recordFor(id, "tx", b[:n], owner)
	}
	if e == nil && n != len(b) {
		e = io.ErrShortWrite
	}
	return e
}

type sshStream struct {
	io.Reader
	io.Writer
	session *ssh.Session
	client  *ssh.Client
}

func (s *sshStream) Close() error { s.session.Close(); return s.client.Close() }
func openHardwareSSH(c HardwareConfig, password string) (io.ReadWriteCloser, error) {
	auth := []ssh.AuthMethod{}
	if password != "" {
		auth = append(auth, ssh.Password(password), ssh.KeyboardInteractive(func(_, _ string, q []string, _ []bool) ([]string, error) {
			v := make([]string, len(q))
			for i := range v {
				v[i] = password
			}
			return v, nil
		}))
	}
	home, _ := os.UserHomeDir()
	for _, name := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
		if raw, e := os.ReadFile(filepath.Join(home, ".ssh", name)); e == nil {
			if key, e := ssh.ParsePrivateKey(raw); e == nil {
				auth = append(auth, ssh.PublicKeys(key))
			}
		}
	}
	addr := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	n, e := net.DialTimeout("tcp", addr, 6*time.Second)
	if e != nil {
		return nil, e
	}
	n.SetDeadline(time.Now().Add(10 * time.Second))
	config := &ssh.ClientConfig{User: c.User, Auth: auth, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		fp := ssh.FingerprintSHA256(key)
		if c.Fingerprint != fp {
			return fmt.Errorf("SSH 指纹未确认或不匹配。请核对设备指纹，再填写并保存：%s", fp)
		}
		return nil
	}}
	cc, ch, rq, e := ssh.NewClientConn(n, addr, config)
	if e != nil {
		n.Close()
		return nil, e
	}
	client := ssh.NewClient(cc, ch, rq)
	session, e := client.NewSession()
	if e != nil {
		client.Close()
		return nil, e
	}
	in, e := session.StdinPipe()
	if e != nil {
		client.Close()
		return nil, e
	}
	out, e := session.StdoutPipe()
	if e != nil {
		client.Close()
		return nil, e
	}
	if e = session.RequestPty("dumb", 32, 120, ssh.TerminalModes{ssh.ECHO: 1}); e == nil {
		e = session.Shell()
	}
	if e != nil {
		client.Close()
		return nil, e
	}
	n.SetDeadline(time.Time{})
	return &sshStream{Reader: out, Writer: in, session: session, client: client}, nil
}

// Telnet negotiation is consumed across read boundaries; binary payload is preserved.
type telnetStream struct {
	net.Conn
	state   byte
	command byte
	mu      sync.Mutex
}

func (t *telnetStream) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := []byte{}
	for _, b := range p {
		out = append(out, b)
		if b == 255 {
			out = append(out, b)
		}
	}
	_, e := t.Conn.Write(out)
	if e != nil {
		return 0, e
	}
	return len(p), nil
}
func (t *telnetStream) Read(p []byte) (int, error) {
	buf := make([]byte, len(p))
	for {
		n, e := t.Conn.Read(buf)
		out := 0
		for _, b := range buf[:n] {
			switch t.state {
			case 0:
				if b == 255 {
					t.state = 1
				} else {
					p[out] = b
					out++
				}
			case 1:
				switch b {
				case 255:
					p[out] = b
					out++
					t.state = 0
				case 251, 252, 253, 254:
					t.command = b
					t.state = 2
				case 250:
					t.state = 3
				default:
					t.state = 0
				}
			case 2:
				if t.command == 251 || t.command == 253 {
					reply := byte(254)
					if t.command == 253 {
						reply = 252
					}
					t.mu.Lock()
					t.Conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
					_, _ = t.Conn.Write([]byte{255, reply, b})
					t.mu.Unlock()
				}
				t.state = 0
			case 3:
				if b == 255 {
					t.state = 4
				}
			case 4:
				if b == 240 {
					t.state = 0
				} else {
					t.state = 3
				}
			}
		}
		if out > 0 || e != nil {
			return out, e
		}
	}
}

func (s *Server) hardwareRoutes(m *http.ServeMux) {
	s.hardwareLibraryRoutes(m)
	m.HandleFunc("GET /api/hardware/ports", s.secure(func(w http.ResponseWriter, r *http.Request) {
		p, e := serial.GetPortsList()
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		if p == nil {
			p = []string{}
		}
		jsonOut(w, 200, p)
	}))
	m.HandleFunc("GET /api/tasks/{id}/hardware", s.secure(func(w http.ResponseWriter, r *http.Request) {
		rows, e := s.app.store.Query("SELECT h.config FROM hardware h JOIN task_hardware t ON t.hardware_id=h.id WHERE t.task_id=? ORDER BY h.rowid", r.PathValue("id"))
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		defer rows.Close()
		out := []HardwareConfig{}
		for rows.Next() {
			var raw string
			var c HardwareConfig
			if e = rows.Scan(&raw); e != nil {
				fail(w, 500, e.Error())
				return
			}
			if json.Unmarshal([]byte(raw), &c) == nil {
				out = append(out, c)
			}
		}
		jsonOut(w, 200, out)
	}))
	m.HandleFunc("POST /api/tasks/{id}/hardware", s.secure(s.saveHardware))
	m.HandleFunc("PUT /api/tasks/{id}/hardware/{hid}", s.secure(s.saveHardware))
	m.HandleFunc("POST /api/tasks/{id}/hardware/{hid}/{action}", s.secure(s.hardwareAction))
	m.HandleFunc("DELETE /api/tasks/{id}/hardware/{hid}", s.secure(func(w http.ResponseWriter, r *http.Request) {
		c, e := s.hardwareConfig(r)
		if e != nil {
			fail(w, 404, "连接配置不存在")
			return
		}
		if e = s.app.hardware.detach(r.PathValue("id"), c.ID); e != nil {
			fail(w, 409, e.Error())
			return
		}

		jsonOut(w, 200, map[string]bool{"ok": true})
	}))
	m.HandleFunc("GET /api/tasks/{id}/hardware/{hid}/events", s.secure(s.hardwareEvents))
	m.HandleFunc("GET /api/hardware/serial-ports", s.secure(func(w http.ResponseWriter, r *http.Request) {
		ports, err := s.app.hardware.serialPorts()
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		jsonOut(w, 200, ports)
	}))
}
func (s *Server) hardwareConfig(r *http.Request) (HardwareConfig, error) {
	var raw string
	var c HardwareConfig
	e := s.app.store.QueryRow("SELECT h.config FROM hardware h JOIN task_hardware t ON t.hardware_id=h.id WHERE h.id=? AND t.task_id=?", r.PathValue("hid"), r.PathValue("id")).Scan(&raw)
	if e == nil {
		e = json.Unmarshal([]byte(raw), &c)
	}
	return c, e
}
func (s *Server) saveHardware(w http.ResponseWriter, r *http.Request) {
	var c HardwareConfig
	if !body(w, r, &c) {
		return
	}
	if _, e := s.app.store.task(r.PathValue("id")); e != nil {
		fail(w, 404, "任务不存在")
		return
	}
	if strings.TrimSpace(c.Name) == "" || len(c.Name) > 160 {
		fail(w, 400, "填写连接名称（最多 160 字节）")
		return
	}
	switch c.Protocol {
	case "serial":
		if c.Device == "" || c.Baud < 300 || c.Baud > 4000000 {
			fail(w, 400, "填写串口及有效波特率")
			return
		}
		if _, err := serialMode(c); err != nil {
			fail(w, 400, err.Error())
			return
		}
	case "tcp", "telnet", "ssh":
		if c.Host == "" || c.Port < 1 || c.Port > 65535 || strings.ContainsAny(c.Host, "\x00\r\n/ ") {
			fail(w, 400, "填写有效主机和端口")
			return
		}
		if c.Protocol == "ssh" && c.User == "" {
			fail(w, 400, "SSH 需要用户名")
			return
		}
	default:
		fail(w, 400, "请选择串口、TCP、Telnet 或 SSH")
		return
	}
	if err := validateRelay(c); err != nil {
		fail(w, 400, err.Error())
		return
	}
	h := s.app.hardware
	h.mu.Lock()
	defer h.mu.Unlock()
	c.ID = r.PathValue("hid")
	fresh := c.ID == ""
	if fresh {
		var n int
		s.app.store.QueryRow("SELECT count(*) FROM hardware").Scan(&n)
		if n >= 128 {
			fail(w, 400, "设备库最多 128 个连接")
			return
		}
		c.ID = uid()
	} else {
		if _, e := s.hardwareConfig(r); e != nil {
			fail(w, 404, "配置不存在")
			return
		}
		if h.links[c.ID] != nil {
			fail(w, 409, "请先断开连接再修改配置")
			return
		}
	}
	raw, _ := json.Marshal(c)
	tx, e := s.app.store.Begin()
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	defer tx.Rollback()
	_, e = tx.Exec("INSERT INTO hardware(id,task_id,config) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET config=excluded.config", c.ID, r.PathValue("id"), string(raw))
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	if fresh {
		_, e = tx.Exec("INSERT INTO task_hardware VALUES(?,?)", r.PathValue("id"), c.ID)
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
	}
	if e = tx.Commit(); e != nil {
		fail(w, 500, e.Error())
		return
	}
	jsonOut(w, 200, c)
}
func (s *Server) hardwareAction(w http.ResponseWriter, r *http.Request) {
	c, e := s.hardwareConfig(r)
	if e != nil {
		fail(w, 404, "连接配置不存在")
		return
	}
	var v struct {
		Password     string `json:"password"`
		Data         string `json:"data"`
		Encoding     string `json:"encoding"`
		Newline      string `json:"newline"`
		ConnectionID string `json:"connection_id"`
	}
	if !body(w, r, &v) {
		return
	}
	switch r.PathValue("action") {
	case "connect":
		e = s.app.hardware.connectOwned(c, v.Password, r.PathValue("id"))
	case "disconnect":
		e = s.app.hardware.disconnectOwned(c.ID, r.PathValue("id"), v.ConnectionID)
	case "claim", "release":
		e = s.app.hardware.control(c.ID, r.PathValue("id"), v.ConnectionID, r.PathValue("action") == "claim")
	case "send":
		if c.Kind == "relay" {
			fail(w, 400, "电源控制器请使用专用上下电按钮")
			return
		}
		var raw []byte
		if v.Encoding == "hex" {
			raw, e = hex.DecodeString(strings.Join(strings.Fields(v.Data), ""))
		} else if v.Encoding == "text" {
			raw = []byte(v.Data)
			switch v.Newline {
			case "lf":
				raw = append(raw, '\n')
			case "cr":
				raw = append(raw, '\r')
			case "crlf":
				raw = append(raw, '\r', '\n')
			case "", "none":
			default:
				e = errors.New("换行格式无效")
			}
		} else {
			e = errors.New("编码无效")
		}
		if e == nil {
			e = s.app.hardware.sendOwned(c.ID, v.ConnectionID, r.PathValue("id"), raw)
		}
	default:
		fail(w, 404, "操作不存在")
		return
	}
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	jsonOut(w, 200, map[string]bool{"ok": true})
}
