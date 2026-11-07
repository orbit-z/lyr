// The lazy loader (client:*, PLAN.md §5 "The loader").
//
// Each <lyr-lazy component="<ID>" client="idle|visible|media" opts="…">
// whose ID is in the config's lazy map is observed with its strategy. When
// the first instance of an ID fires, the ID's entries are imported, once.
// Markers nested inside a lazy marker whose ID hasn't fired yet wait: they
// are observed once the outer ID fires. IDs missing from the map (the
// component's scripts are eager on this page) are ignored, and their
// subtree is observed right away. A delayed island's placeholder,
// <lyr-island src="…/<ID>" client="…" opts="…">, is a marker too.
// client:visible with no element child to observe loads at once (with a
// warning in dev).

import { config, find, when } from "./shared";

// The markers: <lyr-lazy>, and a delayed island's placeholder (server:defer
// with client:*), whose deferred subtree's scripts load on the same trigger
// as its fetch (PLAN.md §3.4 "Delayed island fetch").
const MARKERS = "lyr-lazy,lyr-island[client]";

// idOf is a marker's component ID: its component attribute, or the last
// segment of a placeholder's src (/_lyr/island/<build>/<ID>).
const idOf = (el: Element): string | null =>
  el.localName == "lyr-lazy" ? el.getAttribute("component") : (el.getAttribute("src") || "").split("/").pop()!;

export type Importer = (url: string) => Promise<unknown>;

export function lazy(importer: Importer): void {
  const { dev, lazy: entries = {} } = config();
  const seen = new WeakSet<Element>();
  const fired = new Set<string>();
  const imported = new Set<string>();
  const warned = new Set<string>();
  const cleanups: Record<string, (() => void)[]> = {};

  const pending = (id: string | null): boolean => !!id && id in entries && !fired.has(id);

  const fire = (id: string): void => {
    if (fired.has(id)) return;
    fired.add(id);
    for (const f of cleanups[id] || []) f();
    for (const url of ([] as string[]).concat(entries[id]!)) {
      if (!imported.has(url)) {
        imported.add(url);
        importer(url).catch((e) => dev && console.error(`lyr: ${url}:`, e));
      }
    }
    // Markers nested in this ID's instances can be observed now.
    scan([document]);
  };

  const observe = (el: Element, id: string): void => {
    (cleanups[id] ||= []).push(
      when(
        el,
        () => fire(id),
        // The islands loader warns for a delayed island.
        () => {
          if (dev && el.localName == "lyr-lazy" && !warned.has(id)) {
            warned.add(id);
            console.warn(`lyr: ${id}: client:visible has no element to observe, so it was loaded at once`);
          }
        },
      ),
    );
  };

  const scan = (nodes: Node[]): void => {
    for (const el of find(nodes, MARKERS)) {
      if (seen.has(el)) continue;
      // Wait while an enclosing marker hasn't fired.
      let p = el.parentElement;
      while (p && !(p.matches(MARKERS) && pending(idOf(p)))) p = p.parentElement;
      if (p) continue;
      seen.add(el);
      const id = idOf(el);
      if (pending(id)) observe(el, id!);
    }
  };

  window.lyr__lazy = scan;
  scan([document]);
}
