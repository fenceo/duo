package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type LANNetwork struct {
	Name      string `json:"name"`
	Address   string `json:"address"`
	CIDR      string `json:"cidr"`
	Suggested string `json:"suggested"`
}

func lanNetworks() ([]LANNetwork, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := []LANNetwork{}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagPointToPoint != 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			p, err := netip.ParsePrefix(address.String())
			if err != nil || !p.Addr().Is4() || !p.Addr().IsPrivate() {
				continue
			}
			suggested := netip.PrefixFrom(p.Addr(), max(24, p.Bits())).Masked()
			out = append(out, LANNetwork{Name: iface.Name, Address: p.Addr().String(), CIDR: p.Masked().String(), Suggested: suggested.String()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out, nil
}

func scanTargets(cidr string, ports []int, networks []LANNetwork) ([]netip.Addr, []int, error) {
	p, err := netip.ParsePrefix(strings.TrimSpace(cidr))
	if err != nil || !p.Addr().Is4() || p.Bits() < 24 || !p.Addr().IsPrivate() {
		return nil, nil, errors.New("请输入本机所连局域网的 IPv4 网段，范围最多 /24（256 个地址）")
	}
	p = p.Masked()
	allowed := false
	for _, network := range networks {
		local, e := netip.ParsePrefix(network.CIDR)
		if e == nil && p.Bits() >= local.Bits() && local.Contains(p.Addr()) {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, nil, errors.New("只能扫描简作所在电脑直接连接的局域网网段")
	}
	if len(ports) == 0 {
		ports = []int{22, 2222}
	}
	if len(ports) > 4 {
		return nil, nil, errors.New("每次最多扫描 4 个端口")
	}
	seen := map[int]bool{}
	unique := []int{}
	for _, port := range ports {
		if port < 1 || port > 65535 {
			return nil, nil, errors.New("端口必须在 1–65535 之间")
		}
		if !seen[port] {
			unique = append(unique, port)
			seen[port] = true
		}
	}
	count := 1 << uint(32-p.Bits())
	targets := []netip.Addr{}
	for address, i := p.Addr(), 0; i < count; i, address = i+1, address.Next() {
		if count > 2 && (i == 0 || i == count-1) {
			continue
		}
		targets = append(targets, address)
	}
	return targets, unique, nil
}

type SSHEndpoint struct {
	Host   string `json:"host"`
	Port   int    `json:"port"`
	SSH    bool   `json:"ssh"`
	Banner string `json:"banner"`
}
type SSHScan struct {
	ID        string        `json:"id"`
	CIDR      string        `json:"cidr"`
	Status    string        `json:"status"`
	Completed int           `json:"completed"`
	Total     int           `json:"total"`
	Results   []SSHEndpoint `json:"results"`
	Owner     string        `json:"-"`
	cancel    context.CancelFunc
}
type SSHDiscovery struct {
	mu       sync.Mutex
	scan     *SSHScan
	closed   bool
	wg       sync.WaitGroup
	networks func() ([]LANNetwork, error)
	probe    func(context.Context, string, int) *SSHEndpoint
}

func newSSHDiscovery() *SSHDiscovery { return &SSHDiscovery{networks: lanNetworks, probe: probeSSH} }
func (d *SSHDiscovery) close() {
	d.mu.Lock()
	d.closed = true
	if d.scan != nil {
		d.scan.cancel()
	}
	d.mu.Unlock()
	d.wg.Wait()
}
func (d *SSHDiscovery) cancelOwner(owner string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.scan != nil && d.scan.Owner == owner {
		d.scan.cancel()
	}
}
func copyScan(s *SSHScan) SSHScan {
	out := *s
	out.Results = append([]SSHEndpoint{}, s.Results...)
	sort.Slice(out.Results, func(i, j int) bool {
		a, _ := netip.ParseAddr(out.Results[i].Host)
		b, _ := netip.ParseAddr(out.Results[j].Host)
		if a == b {
			return out.Results[i].Port < out.Results[j].Port
		}
		return a.Less(b)
	})
	return out
}
func (d *SSHDiscovery) start(parent context.Context, owner, cidr string, ports []int) (SSHScan, error) {
	networks, err := d.networks()
	if err != nil {
		return SSHScan{}, err
	}
	targets, ports, err := scanTargets(cidr, ports, networks)
	if err != nil {
		return SSHScan{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.scan != nil && d.scan.Status == "running" {
		return SSHScan{}, errors.New("已有扫描正在进行，请等待完成或停止后再试")
	}
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	scan := &SSHScan{ID: uid(), CIDR: strings.TrimSpace(cidr), Status: "running", Total: len(targets) * len(ports), Results: []SSHEndpoint{}, Owner: owner, cancel: cancel}
	d.scan = scan
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer cancel()
		type target struct {
			host string
			port int
		}
		jobs := make(chan target)
		var workers sync.WaitGroup
		for i := 0; i < min(24, scan.Total); i++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for task := range jobs {
					if ctx.Err() != nil {
						return
					}
					result := d.probe(ctx, task.host, task.port)
					d.mu.Lock()
					scan.Completed++
					if result != nil {
						scan.Results = append(scan.Results, *result)
					}
					d.mu.Unlock()
				}
			}()
		}
	loop:
		for _, address := range targets {
			for _, port := range ports {
				select {
				case jobs <- target{address.String(), port}:
				case <-ctx.Done():
					break loop
				}
			}
		}
		close(jobs)
		workers.Wait()
		d.mu.Lock()
		scan.Status = "done"
		if ctx.Err() != nil {
			scan.Status = "cancelled"
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				scan.Status = "timeout"
			}
		}
		d.mu.Unlock()
	}()
	return copyScan(scan), nil
}

func probeSSH(ctx context.Context, host string, port int) *SSHEndpoint {
	dialer := net.Dialer{Timeout: 650 * time.Millisecond}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	_ = conn.SetReadDeadline(time.Now().Add(900 * time.Millisecond))
	result := &SSHEndpoint{Host: host, Port: port}
	// Read only a bounded server identification string. No username, credential,
	// key exchange or remote command is sent, including to non-SSH listeners.
	reader := bufio.NewReader(io.LimitReader(conn, 2048))
	for i := 0; i < 12; i++ {
		line, err := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "SSH-2.0-") || strings.HasPrefix(line, "SSH-1.99-") {
			result.SSH = true
			for _, r := range line {
				if r >= 32 && r < 127 {
					result.Banner += string(r)
				}
				if len(result.Banner) >= 160 {
					break
				}
			}
			break
		}
		if err != nil {
			break
		}
	}
	return result
}

func (s *Server) discoveryRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/ssh-discovery/networks", s.secure(func(w http.ResponseWriter, r *http.Request) {
		items, err := s.app.discovery.networks()
		if err != nil {
			fail(w, 500, "无法读取本机网络接口")
			return
		}
		jsonOut(w, 200, items)
	}))
	m.HandleFunc("POST /api/ssh-discovery/scans", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			CIDR  string `json:"cidr"`
			Ports []int  `json:"ports"`
		}
		if !body(w, r, &v) {
			return
		}
		owner, _ := s.identity(r)
		scan, err := s.app.discovery.start(s.app.ctx, owner, v.CIDR, v.Ports)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		jsonOut(w, 202, scan)
	}))
	for _, method := range []string{"GET", "DELETE"} {
		m.HandleFunc(method+" /api/ssh-discovery/scans/{id}", s.secure(func(w http.ResponseWriter, r *http.Request) {
			owner, _ := s.identity(r)
			d := s.app.discovery
			d.mu.Lock()
			defer d.mu.Unlock()
			if d.scan == nil || d.scan.ID != r.PathValue("id") || d.scan.Owner != owner {
				fail(w, 404, "扫描已过期，请重新开始")
				return
			}
			if r.Method == "DELETE" {
				d.scan.cancel()
			}
			jsonOut(w, 200, copyScan(d.scan))
		}))
	}
}
