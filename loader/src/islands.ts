// The islands loader (server:defer, PLAN.md §5 "The loader").
//
// Every <lyr-island src="…" props="<token>"> on the page is fetched in
// parallel; on a 2xx response the placeholder is replaced by the fragment,
// otherwise its fallback stays. Placeholders added to the page later (a
// fragment, or HTML the page inserts itself) are picked up by a
// MutationObserver, which also hands every added node to the lazy loader
// through window.lyr__lazy.

import { config, find } from "./shared";

// Above this many characters, the GET request-target (src?p=token) is
// replaced by a POST with the token as the body: it fits nginx's default
// 8 KB request-line buffer.
export const MAX_URL = 8000;

export function islands(): void {
  const dev = config().dev;
  const started = new WeakSet<Element>();
  const warned = new Set<string>();

  const load = async (el: Element): Promise<void> => {
    const src = el.getAttribute("src") || "";
    const token = el.getAttribute("props") || "";
    let url = src + "?p=" + encodeURIComponent(token);
    const init: RequestInit = { redirect: "manual" };
    if (url.length > MAX_URL) {
      url = src;
      init.method = "POST";
      init.body = token;
      init.headers = { "content-type": "text/plain" };
      if (dev && !warned.has(src)) {
        warned.add(src);
        console.warn(
          `lyr: ${src}: props too big for a GET URL, so POST is used, which isn't cacheable; pass an ID, not a large object`,
        );
      }
    }
    try {
      const res = await fetch(url, init);
      const html = await res.text();
      if (!res.ok) {
        if (dev) console.error(`lyr: ${src}: ${res.status}`, html);
        return;
      }
      if (!el.parentNode) return;
      const range = document.createRange();
      range.selectNode(el);
      // createContextualFragment (unlike innerHTML) makes inline scripts
      // run when the fragment is inserted.
      el.replaceWith(range.createContextualFragment(html));
    } catch (e) {
      if (dev) console.error(`lyr: ${src}:`, e);
    }
  };

  const scan = (nodes: Node[]): void => {
    for (const el of find(nodes, "lyr-island")) {
      if (!started.has(el)) {
        started.add(el);
        load(el);
      }
    }
  };

  new MutationObserver((records) => {
    for (const r of records) {
      const nodes = Array.from(r.addedNodes);
      scan(nodes);
      window.lyr__lazy?.(nodes);
    }
  }).observe(document, { childList: true, subtree: true });
  scan([document]);
}
