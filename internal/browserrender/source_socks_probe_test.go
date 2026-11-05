//go:build browser_source_proxy_probe

package browserrender

import (
	"context"
	"fmt"
	"github.com/chromedp/chromedp"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// Evidence-only execution of the paired wrapper's raw native proxy arguments.
// The regular target suite does not treat this source observation as a pass.
func TestPairedNativeSocksSchemeSourceProbe(t *testing.T) {
	config := ownedBrowser(t)
	var ip string
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range interfaces {
		addresses, _ := iface.Addrs()
		for _, address := range addresses {
			host, _, _ := net.ParseCIDR(address.String())
			if host != nil && host.To4() != nil && !host.IsLoopback() {
				ip = host.String()
				break
			}
		}
		if ip != "" {
			break
		}
	}
	if ip == "" {
		t.Fatal("owned nonloopback interface required to observe native proxy route")
	}
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "<html>owned-direct-source</html>") }))
	target.Listener, err = net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	target.Start()
	defer target.Close()
	url := "http://" + net.JoinHostPort(ip, strconv.Itoa(target.Listener.Addr().(*net.TCPAddr).Port))
	for _, scheme := range []string{"socks5", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			var count atomic.Int32
			go func() {
				for {
					conn, err := listener.Accept()
					if err != nil {
						return
					}
					go func() {
						defer conn.Close()
						conn.SetDeadline(time.Now().Add(3 * time.Second))
						header := make([]byte, 2)
						if _, err := io.ReadFull(conn, header); err != nil {
							return
						}
						count.Add(1)
						if header[0] != 5 {
							return
						}
						if _, err := io.CopyN(io.Discard, conn, int64(header[1])); err != nil {
							return
						}
						conn.Write([]byte{5, 0})
						io.ReadFull(conn, make([]byte, 4))
						conn.Write([]byte{5, 2, 0, 1, 0, 0, 0, 0, 0, 0})
					}()
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
			defer cancel()
			opts := append(sourceLaunchOptions(), chromedp.ExecPath(config.Executable), chromedp.UserDataDir(t.TempDir()), chromedp.WSURLReadTimeout(3*time.Minute), chromedp.NoSandbox, chromedp.Flag("headless", true), chromedp.ProxyServer(scheme+"://"+listener.Addr().String()))
			allocator, closeAllocator := chromedp.NewExecAllocator(ctx, opts...)
			defer closeAllocator()
			page, closePage := chromedp.NewContext(allocator)
			defer closePage()
			err = chromedp.Run(page, chromedp.Navigate(url))
			t.Logf("SOURCE_NATIVE scheme=%s proxy_handshakes=%d navigate_error=%v", scheme, count.Load(), err)
			if scheme == "socks5" && (count.Load() == 0 || err == nil) {
				t.Fatal("source control did not exercise real SOCKS5")
			}
		})
	}
}
