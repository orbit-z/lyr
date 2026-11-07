// The islands loader (server:defer, PLAN.md §5 "The loader").
//
// Every <lyr-island src="…" props="<token>"> on the page is fetched in
// parallel (a delayed one, with client="…", when its strategy fires, in
// the LYR_WHEN variant); on a 2xx response the placeholder is replaced by the fragment,
// otherwise its fallback stays. Placeholders added to the page later (a
// fragment, or HTML the page inserts itself) are picked up by a
// MutationObserver, which also hands every added node to the lazy loader
// through window.lyr__lazy. A second copy of the loader (one a page
// partial brought, PLAN.md §4k) installs nothing again.

import { find, isDev, when } from "./shared";

// LYR_WHEN is set at build time: true in the variant for pages with a
// delayed island (loaderIslandsWhenJS, PLAN.md §3.4 "Delayed island
// fetch"), where a placeholder with a client attribute is fetched when its
// strategy fires; false in loaderIslandsJS, where esbuild drops that code.
declare const LYR_WHEN: boolean;

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

// CLAIM_MS is how long the plain loader leaves a delayed placeholder to
// the strategy-reading variant a partial brings before fetching it itself.
export const CLAIM_MS = 1000;

export function islands(): void {
  // Idempotent (PLAN.md §4k): a partial may bring a copy to a page that
  // has one. The installed loader's observer already sees the partial's
  // placeholders, so a second copy does nothing, except that the variant
  // that reads strategies takes over from the plain one (whose scan skips
  // delayed placeholders), sharing its started set and observer.
  const prev = window.lyr__islands;
  if (prev && (prev.w || !LYR_WHEN)) return;
  const dev = isDev();
  const started = prev ? prev.t : new WeakSet<Element>();
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
      if (started.has(el)) continue;
      const src = el.getAttribute("src") || "";
      if (!el.hasAttribute("client")) {
        started.add(el);
        load(el);
      } else if (LYR_WHEN) {
        started.add(el);
        delay(
          el,
          () => load(el),
          () => {
            if (dev && !warned.has(" " + src)) {
              warned.add(" " + src);
              console.warn(`lyr: ${src}: client:visible has no fallback element to observe, so it was fetched at once`);
            }
          },
        );
      } else {
        // A delayed placeholder on a page with the plain loader comes from
        // a page partial, which brings the variant that reads strategies
        // (PLAN.md §4k). Its module script runs after this observer, so
        // wait for it to claim the placeholder (the shared started set);
        // if it never runs (an innerHTML swap), fetch at once.
        setTimeout(() => {
          if (started.has(el) || !el.isConnected) return;
          started.add(el);
          if (dev && !warned.has("!" + src)) {
            warned.add("!" + src);
            console.warn(`lyr: ${src}: delayed island fetched at once: its loader never ran`);
          }
          load(el);
        }, CLAIM_MS);
      }
    }
  };

  window.lyr__islands = { w: LYR_WHEN, s: scan, t: started };
  if (!prev)
    new MutationObserver((records) => {
      for (const r of records) {
        const nodes = Array.from(r.addedNodes);
        window.lyr__islands!.s(nodes);
        window.lyr__lazy?.(nodes);
      }
    }).observe(document, { childList: true, subtree: true });
  scan([document]);
}
