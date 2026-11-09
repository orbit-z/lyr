// Package e2e holds end-to-end tests that drive a real headless browser.
// They're behind the e2e build tag (Unix only), and skipped when no
// browser is found:
//
//	go test -tags e2e ./e2e
//
// The browser is $LYR_BROWSER (a path, "firefox" or "chromium";
// $LYR_CHROME is read as a path too), else the first of chromium,
// chromium-browser, google-chrome and firefox on PATH.
package e2e
