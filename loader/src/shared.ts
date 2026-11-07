// Shared by the loader scripts. Each script is bundled on its own, so
// this module is inlined into each and they share no state at runtime.

// The page's loader config, written by the runtime's head injection
// before the module scripts:
//
//	<script type="application/json" id="lyr-loader">{"dev":false,"lazy":{"<ID>":["<url>",…]}}</script>
//
// "lazy" is left out on pages without lazy entries. A single string value
// is accepted too. A page partial (PLAN.md §4k) that needs a loader brings
// its own config, <script type="application/json" data-lyr-loader>, with
// "ids": the component ID that each of its lazy URLs runs, so a lazy load
// skips a component the document already ran (lyr__ran).
export interface Config {
  dev?: boolean;
  lazy?: Record<string, string | string[]>;
  ids?: Record<string, string>;
}

const CONFIGS = "#lyr-loader,[data-lyr-loader]";

const parse = (el: Element | null): Config => JSON.parse(el?.textContent || "{}");

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
    // The islands → lazy hand-off. The lazy script sets it to its scan
    // function; the islands script calls it with the nodes added to the
    // page (island fragments included), so <lyr-lazy> markers that
    // arrive later are observed too. Optional because the lazy script is
    // only on pages that have lazy entries. With merge, it re-reads the
    // configs first: a second copy of the lazy script (a partial's) calls
    // it so, instead of installing itself again.
    lyr__lazy?: (nodes: Node[], merge?: boolean) => void;
    // The installed islands loader, which a second copy (a partial's)
    // finds: w is set for the variant that reads strategies, s is its scan
    // function (the observer calls the current one), t the placeholders
    // already started.
    lyr__islands?: { w: boolean; s: (nodes: Node[]) => void; t: WeakSet<Element> };
  }
}
