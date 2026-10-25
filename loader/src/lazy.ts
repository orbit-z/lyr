// The lazy loader (client:*, PLAN.md §5 "The loader").
//
// Each <orbit-lazy component="<ID>" client="idle|visible|media" opts="…">
// whose ID is in the config's lazy map is observed with its strategy. When
// the first instance of an ID fires, the ID's entries are imported, once.
// Markers nested inside a lazy marker whose ID hasn't fired yet wait: they
// are observed once the outer ID fires. IDs missing from the map (the
// component's scripts are eager on this page) are ignored, and their
// subtree is observed right away.

import { config, find } from "./shared";

export type Importer = (url: string) => Promise<unknown>;

export function lazy(importer: Importer): void {
  const { dev, lazy: entries = {} } = config();
  const seen = new WeakSet<Element>();
  const fired = new Set<string>();
  const imported = new Set<string>();
  const cleanups: Record<string, (() => void)[]> = {};

  const pending = (id: string | null): boolean => !!id && id in entries && !fired.has(id);

  const fire = (id: string): void => {
    if (fired.has(id)) return;
    fired.add(id);
    for (const f of cleanups[id] || []) f();
    for (const url of ([] as string[]).concat(entries[id]!)) {
      if (!imported.has(url)) {
        imported.add(url);
        importer(url).catch((e) => dev && console.error(`orbit: ${url}:`, e));
      }
    }
    // Markers nested in this ID's instances can be observed now.
    scan([document]);
  };

  const observe = (el: Element, id: string): void => {
    const opts = el.getAttribute("opts") || undefined;
    const go = () => fire(id);
    const undo = (cleanups[id] ||= []);
    switch (el.getAttribute("client")) {
      case "idle":
        // Safari has no requestIdleCallback.
        if ("requestIdleCallback" in window) requestIdleCallback(go);
        else setTimeout(go, 200);
        break;
      case "visible": {
        // The marker is display:contents and has no box: observe its
        // element children.
        const io = new IntersectionObserver(
          (es) => es.some((e) => e.isIntersecting) && go(),
          { rootMargin: opts },
        );
        for (const c of Array.from(el.children)) io.observe(c);
        undo.push(() => io.disconnect());
        break;
      }
      case "media": {
        const mq = matchMedia(opts || "");
        if (mq.matches) go();
        else {
          const h = (e: MediaQueryListEvent) => e.matches && go();
          mq.addEventListener("change", h);
          undo.push(() => mq.removeEventListener("change", h));
        }
        break;
      }
      default:
        go();
    }
  };

  const scan = (nodes: Node[]): void => {
    for (const el of find(nodes, "orbit-lazy")) {
      if (seen.has(el)) continue;
      // Wait while an enclosing marker hasn't fired.
      let p = el.parentElement;
      while (p && !(p.localName == "orbit-lazy" && pending(p.getAttribute("component")))) p = p.parentElement;
      if (p) continue;
      seen.add(el);
      const id = el.getAttribute("component")!;
      if (pending(id)) observe(el, id);
    }
  };

  window.orbit__lazy = scan;
  scan([document]);
}
