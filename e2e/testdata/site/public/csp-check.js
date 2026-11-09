// Test-only (e2e): counts Content Security Policy violations, and some
// time after load POSTs what the test checks to /__e2e/result (the test's
// proxy, same origin, so connect-src 'self' allows it). Loaded first in
// <head>, as an external classic script (a strict policy allows it).
(() => {
  const violations = [];
  document.addEventListener("securitypolicyviolation", (e) => {
    violations.push(`${e.effectiveDirective} ${e.blockedURI} ${e.sourceFile}:${e.lineNumber} ${e.sample}`);
  });
  // The display of a wrapper still on the page, or "none-found".
  const display = (sel) => {
    const el = document.querySelector(sel);
    return el ? getComputedStyle(el).display : "none-found";
  };
  // After the timed swaps (the last at 1 s), the islands' fetches and the
  // idle callbacks.
  setTimeout(() => {
    fetch("/__e2e/result", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        page: location.pathname,
        violations,
        lazyDisplay: display("lyr-lazy"),
        islandDisplay: display("lyr-island"),
        // The run counters are data-*-runs attributes of <html>.
        html: document.documentElement.outerHTML,
      }),
    });
  }, 4000);
})();
