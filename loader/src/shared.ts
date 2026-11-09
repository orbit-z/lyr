// Shared by the loader scripts. Each script is bundled on its own, so
// this module is inlined into each and they share no state at runtime.

// The page's loader config, written by the runtime's head injection
// before the loader script (PLAN.md §5f):
//
//	<script type="application/json" id="lyr-loader">{"dev":false,"lazy":{"<ID>":["<url>",…]}}</script>
//
// "lazy" is left out on pages without lazy entries. A single string value
// is accepted too. A page partial (PLAN.md §4k) brings its config in the
// data-lyr-loader attribute of its loader script, with "ids": the
// component ID that each of its lazy URLs runs, so a lazy load skips a
// component the document already ran (lyr__ran); and "run": the component
// entries its loader script imports (start, src/loader.ts).
export interface Config {
  dev?: boolean;
  lazy?: Record<string, string | string[]>;
  ids?: Record<string, string>;
  run?: [string, string][];
}

// Only script elements carry a config: an injected element of another
// kind can't add entries (and every URL is checked by safe anyway).
const CONFIGS = "script#lyr-loader,script[data-lyr-loader]";

export const parse = (el: Element | null): Config =>
  JSON.parse((el && (el.getAttribute("data-lyr-loader") || el.textContent)) || "{}");

// isDev reads "dev" from the first config: it's the same in all of them.
export const isDev = (): boolean => !!parse(document.querySelector(CONFIGS)).dev;

// config merges every loader config in the document: the page's own and
// those partials brought.
export const config = (): Config => {
  const c: Config & { lazy: Record<string, string[]>; ids: Record<string, string> } = { lazy: {}, ids: {} };
  for (const el of document.querySelectorAll(CONFIGS)) {
    const x = parse(el);
    c.dev ||= x.dev;
    for (const id in x.lazy) c.lazy[id] = ([] as string[]).concat(c.lazy[id] || [], x.lazy[id]!);
    Object.assign(c.ids, x.ids);
  }
  return c;
};

// safe returns url as a path ("/_lyr/x.js") when it resolves, against the
// page, to this origin under prefix, and "" otherwise. Resolving first
// defeats "/_lyr/../x", "%2e%2e", backslashes and "//host/_lyr/"; a
// path with "%" left after it is refused, since the URL parser keeps
// "%2F" and "%5C" encoded and a server or router that decodes them would
// see another path (Lyr's own URLs never have one). The
// loader reads its URLs from data blocks, which an HTML injection could
// add: without this check, lyr's trusted script would import or fetch
// whatever they name (a "script gadget" under a strict CSP). An option to
// serve /_lyr/ from another origin would have to change this check.
export const safe = (url: string, prefix: string): string => {
  try {
    const u = new URL(url, location.href);
    if (u.origin == location.origin && u.pathname.startsWith(prefix) && !u.pathname.includes("%"))
      return u.pathname + u.search;
  } catch {}
  return "";
};

export type Importer = (url: string) => Promise<unknown>;

// load imports url with importer if safe allows it (under /_lyr/), and
// reports a refused URL or a failed import in dev. With id, the component
// that url runs, it marks id as run (lyr__ran) while the import is
// accepted and pending or done, so a refused or failed import doesn't
// keep a later partial from running it.
export const load = (importer: Importer, url: string, dev?: boolean, id?: string): void => {
  const u = safe(url, "/_lyr/");
  if (!u) {
    if (dev) console.error(`lyr: ${url}: not under /_lyr/, not imported`);
    return;
  }
  if (id) ran().add(id);
  importer(u).catch((e) => {
    if (id) ran().delete(id);
    if (dev) console.error(`lyr: ${url}:`, e);
  });
};

// ran is the document's run-once set of component IDs (PLAN.md §4k): page
// bundles add the IDs of the components they inline that partials use,
// partials' runners add those they import.
export const ran = (): Set<string> => ((globalThis as any).lyr__ran ??= new Set());

// find returns the elements among nodes, and their descendants, that
// match sel. Non-element nodes are looked into when they can be (a
// Document or DocumentFragment) and skipped otherwise (text, comments).
export const find = (nodes: Node[], sel: string): Element[] => {
  const out: Element[] = [];
  for (const n of nodes) {
    if (n.nodeType == 1 && (n as Element).matches(sel)) out.push(n as Element);
    if ((n as ParentNode).querySelectorAll) out.push(...(n as ParentNode).querySelectorAll(sel));
  }
  return out;
};

// when calls go once el's strategy fires: its client attribute ("idle",
// "visible" or "media", any other value fires at once) with its opts (the
// rootMargin or the media query). It returns a function that stops
// waiting. The strategy reader of the lazy loader and of the islands
// loader for delayed islands (PLAN.md §3.4).
//
// The element is display:contents and has no box: visible observes its
// element children, and with none to observe, none is called and go fires
// at once (the content would otherwise never load).
export const when = (el: Element, go: () => void, none: () => void): (() => void) => {
  const opts = el.getAttribute("opts") || undefined;
  switch (el.getAttribute("client")) {
    case "idle":
      // Safari has no requestIdleCallback.
      if ("requestIdleCallback" in window) requestIdleCallback(go);
      else setTimeout(go, 200);
      break;
    case "visible": {
      const kids = Array.from(el.children);
      if (!kids.length) {
        none();
        go();
        break;
      }
      const io = new IntersectionObserver((es) => es.some((e) => e.isIntersecting) && go(), { rootMargin: opts });
      for (const c of kids) io.observe(c);
      return () => io.disconnect();
    }
    case "media": {
      const mq = matchMedia(opts || "");
      if (mq.matches) go();
      else {
        const h = (e: MediaQueryListEvent) => e.matches && go();
        mq.addEventListener("change", h);
        return () => mq.removeEventListener("change", h);
      }
      break;
    }
    default:
      go();
  }
  return () => {};
};

declare global {
  interface Window {
    // The islands → lazy hand-off. The lazy loader sets it to its scan
    // function; the islands loader calls it with the nodes added to the
    // page (island fragments included), so <lyr-lazy> markers that arrive
    // later are observed too. With merge, it re-reads the configs first:
    // a later run of the loader script (a partial's) calls it so.
    lyr__lazy?: (nodes: Node[], merge?: boolean) => void;
    // Set once the islands loader is installed. w stays true so that a
    // v0.2 partial's inline loader copies (deploy skew) install nothing.
    lyr__islands?: { w: boolean };
  }
}
