package browserrender

import (
	"context"
	"crypto/rand"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

func (c Config) native(ctx context.Context, request Request) (source *string, outcome error) {
	if err := guard(ctx, request); err != nil {
		return nil, err
	}
	path, err := filepath.Abs(c.Executable)
	if err != nil || c.Executable == "" {
		return nil, &Error{Code: "browser_executable_unavailable"}
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Mode()&0111 == 0 {
		return nil, &Error{Code: "browser_executable_unavailable"}
	}
	// Ordinary humanized goto only invalidates an internal isolated-world
	// cache, but the source still validates its interaction preset on launch.
	if c.Humanize && c.HumanPreset != "default" && c.HumanPreset != "careful" {
		return nil, &Error{Code: "browser_human_preset_invalid"}
	}
	profile, err := c.tempProfile()
	if err != nil {
		return nil, &Error{Code: "browser_profile_failed"}
	}
	defer func() {
		// The source retains an already captured document after a final close
		// error. Preserve it and the first failure, especially revoked scope.
		outcome = browserProfileCleanupOutcome(outcome, removeBrowserProfile(profile, os.RemoveAll))
	}()
	opts := sourceLaunchOptions()
	// Paired Playwright 1.62 allows 180s for launch, separately from goto's
	// 30s default and the caller's networkidle setting. CDP defaults to 20s.
	opts = append(opts, chromedp.ExecPath(path), chromedp.UserDataDir(profile), chromedp.WSURLReadTimeout(3*time.Minute), chromedp.Flag("headless", request.Headless), chromedp.Flag("enable-automation", false), chromedp.Flag("enable-unsafe-swiftshader", false), chromedp.NoSandbox)
	seed, err := rand.Int(rand.Reader, big.NewInt(90000))
	if err != nil {
		return nil, &Error{Code: "browser_fingerprint_failed"}
	}
	platform := "windows"
	if runtime.GOOS == "darwin" {
		platform = "macos"
	}
	opts = append(opts, chromedp.Flag("fingerprint", strconv.FormatInt(seed.Int64()+10000, 10)), chromedp.Flag("fingerprint-platform", platform))
	if request.Headless {
		opts = append(opts, chromedp.WindowSize(1920, 947), chromedp.Flag("hide-scrollbars", true), chromedp.Flag("mute-audio", true))
	} else {
		opts = append(opts, chromedp.Flag("ignore-gpu-blocklist", true))
		display, closeDisplay, err := nativeDisplay(ctx)
		if err != nil {
			return nil, err
		}
		defer closeDisplay()
		if display != "" {
			opts = append(opts, chromedp.Env("DISPLAY="+display))
		}
	}
	rawProxy := request.Proxy
	if request.SourceProxy != nil {
		rawProxy, err = request.SourceProxy.nativeURL()
		if err != nil {
			return nil, err
		}
	}
	if rawProxy != "" {
		address, cleanup, err := browserProxy(ctx, rawProxy, request.Guard)
		if err != nil {
			return nil, err
		}
		defer cleanup()
		opts = append(opts, chromedp.ProxyServer(address))
		if request.SourceProxy == nil || !request.SourceProxy.inlineOnNativeRuntime() {
			opts = append(opts, chromedp.Flag("proxy-bypass-list", "<-loopback>"))
		}
	}
	allocator, closeAllocator := chromedp.NewExecAllocator(ctx, opts...)
	defer closeAllocator()
	page, closePage := chromedp.NewContext(allocator, chromedp.WithErrorf(func(string, ...any) {}), chromedp.WithLogf(func(string, ...any) {}))
	defer closePage()
	var mu sync.Mutex
	active := map[network.RequestID]bool{}
	last := time.Now()
	var rejected error
	var mainFrame cdp.FrameID
	origin, _ := url.Parse(request.URL)
	chromedp.ListenTarget(page, func(event any) {
		switch event := event.(type) {
		case *network.EventRequestWillBeSent:
			mu.Lock()
			active[event.RequestID] = true
			last = time.Now()
			mu.Unlock()
		case *network.EventLoadingFinished:
			mu.Lock()
			delete(active, event.RequestID)
			last = time.Now()
			mu.Unlock()
		case *network.EventLoadingFailed:
			mu.Lock()
			delete(active, event.RequestID)
			last = time.Now()
			mu.Unlock()
		case *fetch.EventRequestPaused:
			go func() {
				err := guard(ctx, request)
				target, parseErr := url.Parse(event.Request.URL)
				if err == nil && (parseErr != nil || target == nil || target.Scheme != "http" && target.Scheme != "https" && target.Scheme != "data" && target.Scheme != "about") {
					err = &Error{Code: "browser_resource_scheme_denied"}
				}
				if err == nil && event.FrameID == mainFrame && event.ResourceType == network.ResourceTypeDocument && !sameOrigin(target, origin) {
					err = &Error{Code: "browser_navigation_origin_denied"}
				}
				if err != nil {
					mu.Lock()
					if rejected == nil {
						rejected = err
					}
					mu.Unlock()
					_ = chromedp.Run(page, fetch.FailRequest(event.RequestID, network.ErrorReasonBlockedByClient))
					return
				}
				headers := []*fetch.HeaderEntry{}
				for name, value := range event.Request.Headers {
					if strings.EqualFold(name, "Cookie") && (request.Cookie != "" || !sameOrigin(target, origin)) || strings.EqualFold(name, "Authorization") && !sameOrigin(target, origin) {
						continue
					}
					if value, ok := value.(string); ok {
						headers = append(headers, &fetch.HeaderEntry{Name: name, Value: value})
					}
				}
				if sameOrigin(target, origin) && request.Cookie != "" {
					headers = append(headers, &fetch.HeaderEntry{Name: "Cookie", Value: request.Cookie})
				}
				_ = chromedp.Run(page, fetch.ContinueRequest(event.RequestID).WithHeaders(headers))
			}()
		}
	})
	actions := chromedp.Tasks{network.Enable(), chromedp.ActionFunc(func(ctx context.Context) error {
		tree, err := cdppage.GetFrameTree().Do(ctx)
		if err == nil {
			mainFrame = tree.Frame.ID
		}
		return err
	}), fetch.Enable()}
	if request.Headless {
		actions = append(actions, emulation.SetDeviceMetricsOverride(1920, 947, 1, false))
	}
	if request.UserAgent != "" {
		actions = append(actions, emulation.SetUserAgentOverride(request.UserAgent))
	}
	if err = guard(ctx, request); err != nil {
		return nil, err
	}
	if err = chromedp.Run(page, actions); err != nil {
		return nil, &Error{Code: "browser_launch_failed"}
	}
	// page.goto has its own 30 second default; the supplied timeout applies to
	// the following networkidle wait, as in the fixed helper.
	navigation, cancelNavigation := context.WithTimeout(page, 30*time.Second)
	err = chromedp.Run(navigation, chromedp.Navigate(request.URL))
	cancelNavigation()
	if err != nil {
		mu.Lock()
		cause := rejected
		mu.Unlock()
		if cause != nil {
			return nil, cause
		}
		return nil, &Error{Code: "browser_navigation_failed"}
	}
	wait := page
	if request.Timeout > 0 {
		var cancel context.CancelFunc
		wait, cancel = context.WithTimeout(page, request.Timeout)
		defer cancel()
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if wait.Err() != nil {
			return nil, &Error{Code: "browser_networkidle_failed"}
		}
		if err = guard(ctx, request); err != nil {
			return nil, err
		}
		mu.Lock()
		quiet, cause := len(active) == 0 && time.Since(last) >= 500*time.Millisecond, rejected
		mu.Unlock()
		if cause != nil {
			return nil, cause
		}
		if quiet {
			break
		}
		select {
		case <-wait.Done():
			return nil, &Error{Code: "browser_networkidle_failed"}
		case <-ticker.C:
		}
	}
	var html string
	// page.content serializes the doctype and document element, not only body.
	err = chromedp.Run(page, chromedp.Evaluate(`(() => {let s='';for(const n of document.childNodes){if(n.nodeType===Node.DOCUMENT_TYPE_NODE)s+=new XMLSerializer().serializeToString(n);else if(n.nodeType===Node.ELEMENT_NODE)s+=n.outerHTML;}return s;})()`, &html))
	if err != nil {
		return nil, &Error{Code: "browser_content_failed"}
	}
	if len(html) > maxHTMLBytes {
		return nil, &Error{Code: "browser_content_too_large"}
	}
	if err = guard(ctx, request); err != nil {
		return nil, err
	}
	return &html, nil
}
