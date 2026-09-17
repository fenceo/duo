package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func scanNetworksFixture() ([]LANNetwork, error) {
	return []LANNetwork{{Name: "LAN", Address: "192.168.50.10", CIDR: "192.168.50.0/24", Suggested: "192.168.50.0/24"}}, nil
}
func TestDiscoveryBounds(t *testing.T) {
	networks, _ := scanNetworksFixture()
	for _, cidr := range []string{"8.8.8.8/32", "100.95.19.0/24", "127.0.0.1/32", "192.168.50.0/23", "192.168.51.0/24", "192.168.50.1", "::1/128"} {
		if _, _, err := scanTargets(cidr, nil, networks); err == nil {
			t.Fatal("accepted", cidr)
		}
	}
	for _, ports := range [][]int{{0}, {65536}, {-1}, {1, 2, 3, 4, 5}} {
		if _, _, err := scanTargets("192.168.50.0/24", ports, networks); err == nil {
			t.Fatal(ports)
		}
	}
	addresses, ports, err := scanTargets("192.168.50.10/24", []int{22, 22, 2222}, networks)
	if err != nil || len(addresses) != 254 || addresses[0].String() != "192.168.50.1" || len(ports) != 2 {
		t.Fatal(addresses, ports, err)
	}
	addresses, ports, err = scanTargets("192.168.50.1/32", nil, networks)
	if err != nil || len(addresses) != 1 || len(ports) != 2 {
		t.Fatal(addresses, ports, err)
	}
}
func TestDiscoveryCancellationConcurrencyAndOwnership(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	d := a.discovery
	d.networks = scanNetworksFixture
	var active, peak atomic.Int32
	d.probe = func(ctx context.Context, host string, port int) *SSHEndpoint {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		<-ctx.Done()
		return nil
	}
	first, err := d.start(a.ctx, "owner", "192.168.50.0/24", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return active.Load() == 24 })
	if _, err := d.start(a.ctx, "second", "192.168.50.1/32", nil); err == nil {
		t.Fatal("parallel scan accepted")
	}
	d.cancelOwner("other")
	d.mu.Lock()
	status := d.scan.Status
	d.mu.Unlock()
	if status != "running" {
		t.Fatal(status)
	}
	req := toolsClient(t, a)
	req("/api/ssh-discovery/scans/"+first.ID, "GET", nil, 404)
	req("/api/ssh-discovery/scans/"+first.ID, "DELETE", nil, 404)
	d.cancelOwner("owner")
	waitUntil(t, func() bool { d.mu.Lock(); defer d.mu.Unlock(); return d.scan.Status == "cancelled" })
	if peak.Load() > 24 || active.Load() != 0 {
		t.Fatal(peak.Load(), active.Load())
	}
	d.probe = func(ctx context.Context, host string, port int) *SSHEndpoint {
		return &SSHEndpoint{Host: host, Port: port, SSH: true, Banner: "SSH-2.0-fixture"}
	}
	raw := req("/api/ssh-discovery/scans", "POST", map[string]any{"cidr": "192.168.50.1/32", "ports": []int{22}}, 202)
	var scan SSHScan
	_ = json.Unmarshal(raw, &scan)
	waitUntil(t, func() bool { d.mu.Lock(); defer d.mu.Unlock(); return d.scan.Status == "done" })
	raw = req("/api/ssh-discovery/scans/"+scan.ID, "GET", nil, 200)
	_ = json.Unmarshal(raw, &scan)
	if scan.Completed != 1 || len(scan.Results) != 1 || !scan.Results[0].SSH || strings.Contains(string(raw), "owner") {
		t.Fatal(string(raw))
	}
	req("/api/ssh-discovery/scans", "POST", map[string]any{"cidr": "8.8.8.8/32"}, 400)
}
func TestProbeReadsSSHBannerWithoutSendingCredentials(t *testing.T) {
	for _, banner := range []string{"notice\r\nSSH-2.0-TestServer\r\n", "HTTP/1.1 200 OK\r\n"} {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		sent := make(chan int, 1)
		go func() {
			conn, err := listener.Accept()
			if err != nil {
				sent <- -1
				return
			}
			defer conn.Close()
			_, _ = io.WriteString(conn, banner)
			_ = conn.SetReadDeadline(time.Now().Add(1100 * time.Millisecond))
			b := make([]byte, 256)
			n, _ := conn.Read(b)
			sent <- n
		}()
		host, p, _ := net.SplitHostPort(listener.Addr().String())
		port, _ := strconv.Atoi(p)
		result := probeSSH(context.Background(), host, port)
		listener.Close()
		if result == nil || result.SSH != strings.Contains(banner, "SSH-2.0-") {
			t.Fatal(result)
		}
		if n := <-sent; n != 0 {
			t.Fatalf("scanner sent %d bytes", n)
		}
	}
}
