// Shared by the loader scripts. Each script is bundled on its own, so
// this module is inlined into each and they share no state at runtime.

// The page's loader config, written by the runtime's head injection
// before the module scripts:
//
//	<script type="application/json" id="lyr-loader">{"dev":false,"lazy":{"<ID>":["<url>",…]}}</script>
//
// "lazy" is left out on pages without lazy entries. A single string value
// is accepted too.
export interface Config {
  dev?: boolean;
  lazy?: Record<string, string | string[]>;
}

export const config = (): Config =>
  JSON.parse(document.getElementById("lyr-loader")?.textContent || "{}");

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
    // only on pages that have lazy entries.
    lyr__lazy?: (nodes: Node[]) => void;
  }
}
