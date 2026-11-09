// The islands loader (server:defer, PLAN.md §5 "The loader").
//
// Every <lyr-island src="…" props="<token>"> on the page is fetched in
// parallel, a delayed one (with client="…", PLAN.md §3.4) when its
// strategy fires; on a 2xx response the placeholder is replaced by the
// fragment, otherwise its fallback stays. Placeholders added to the page
// later (a fragment, a page partial, or HTML the page inserts itself) are
// picked up by a MutationObserver, which also hands every added node to
// the lazy loader through window.lyr__lazy. It installs once per
// document: a later run of the loader script (a partial's) finds it.

import { find, isDev, safe, when } from "./shared";

// delay calls go when a delayed placeholder's strategy fires, unless the
// placeholder has left the page by then.
const delay = (el: Element, go: () => void, none: () => void): void => {
  let undo: (() => void) | undefined;
  undo = when(
    el,
    () => {
      undo?.();
      if (el.isConnected) go();
    },
    none,
  );
};

// Above this many characters, the GET request-target (src?p=token) is
// replaced by a POST with the token as the body: it fits nginx's default
// 8 KB request-line buffer.
export const MAX_URL = 8000;

export function islands(): void {
  if (window.lyr__islands) return;
  const dev = isDev();
  const started = new WeakSet<Element>();
  const warned = new Set<string>();
  const warn = (key: string, msg: string): void => {
    if (dev && !warned.has(key)) {
      warned.add(key);
      console.warn(msg);
    }
  };

  const load = async (el: Element): Promise<void> => {
    const attr = el.getAttribute("src") || "";
    // Only lyr's island endpoint is fetched (see safe).
    const src = safe(attr, "/_lyr/island/");
    if (!src) {
      if (dev) console.error(`lyr: ${attr}: not under /_lyr/island/, not fetched`);
      return;
    }
    const token = el.getAttribute("props") || "";
    let url = src + "?p=" + encodeURIComponent(token);
    const init: RequestInit = { redirect: "manual" };
    if (url.length > MAX_URL) {
      url = src;
      init.method = "POST";
      init.body = token;
      init.headers = { "content-type": "text/plain" };
      warn(
        src,
        `lyr: ${src}: props too big for a GET URL, so POST is used, which isn't cacheable; pass an ID, not a large object`,
      );
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
      if (started.has(el)) continue;
      started.add(el);
      if (!el.hasAttribute("client")) load(el);
      else {
        const src = el.getAttribute("src");
        delay(
          el,
          () => load(el),
          () => warn(" " + src, `lyr: ${src}: client:visible has no fallback element to observe, so it was fetched at once`),
        );
      }
    }
  };

  window.lyr__islands = { w: true };
  new MutationObserver((records) => {
    for (const r of records) {
      const nodes = Array.from(r.addedNodes);
      scan(nodes);
      window.lyr__lazy?.(nodes);
    }
  }).observe(document, { childList: true, subtree: true });
  scan([document]);
}
