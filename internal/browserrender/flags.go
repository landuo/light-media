package browserrender

import "github.com/chromedp/chromedp"

// Ordinary Chromium launch flags from the NAS-paired Playwright 1.62.0
// chromiumSwitches.ts (Apache-2.0), with CloakBrowser's two ignored flags
// omitted. CDP transport/profile/headless options are supplied by native.go.
// https://github.com/microsoft/playwright/blob/v1.62.0/packages/playwright-core/src/server/chromium/chromiumSwitches.ts
func sourceLaunchOptions() []chromedp.ExecAllocatorOption {
	options := []chromedp.ExecAllocatorOption{}
	for _, flag := range []string{
		"disable-field-trial-config", "disable-background-networking", "disable-background-timer-throttling", "disable-backgrounding-occluded-windows", "disable-back-forward-cache", "disable-breakpad", "disable-client-side-phishing-detection", "disable-component-extensions-with-background-pages", "disable-component-update", "no-default-browser-check", "disable-default-apps", "disable-dev-shm-usage", "disable-edgeupdater", "disable-extensions", "allow-pre-commit-input", "disable-hang-monitor", "disable-ipc-flooding-protection", "disable-popup-blocking", "disable-prompt-on-repost", "disable-renderer-backgrounding", "disable-updater-scheduler", "metrics-recording-only", "no-first-run", "no-service-autorun", "export-tagged-pdf", "disable-search-engine-choice-screen", "unsafely-disable-devtools-self-xss-warnings", "edge-skip-compat-layer-relaunch", "disable-infobars", "disable-sync",
	} {
		options = append(options, chromedp.Flag(flag, true))
	}
	return append(options,
		chromedp.Flag("disable-features", "AvoidUnnecessaryBeforeUnloadCheckSync,BoundaryEventDispatchTracksNodeRemoval,DestroyProfileOnBrowserClose,DialMediaRouteProvider,GlobalMediaControls,HttpsUpgrades,LensOverlay,MediaRouter,PaintHolding,ThirdPartyStoragePartitioning,BlockOriginHeaderModificationOnRedirect,Translate,AutoDeElevate,OptimizationHints,msForceBrowserSignIn,msEdgeUpdateLaunchServicesPreferredVersion"),
		chromedp.Flag("enable-features", "CDPScreenshotNewSurface"),
		chromedp.Flag("force-color-profile", "srgb"),
		chromedp.Flag("password-store", "basic"),
		chromedp.Flag("use-mock-keychain", true),
	)
}
