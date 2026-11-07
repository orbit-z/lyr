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
// warning in dev). A second copy of the loader (one a page partial
// brought, PLAN.md §4k) merges the partial's config into the installed one
// and rescans.

import { config, find, ran, when } from "./shared";

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
  // Idempotent (PLAN.md §4k): a second copy, one a partial brought, merges
  // the partial's config into the installed loader and rescans.
  if (window.lyr__lazy) return window.lyr__lazy([document], true);
  let { dev, lazy: entries = {}, ids = {} } = config();
  const seen = new WeakSet<Element>();
  const fired = new Set<string>();
  const imported = new Set<string>();
  const warned = new Set<string>();
  const cleanups: Record<string, (() => void)[]> = {};

  const pending = (id: string | null): boolean => !!id && id in entries && !fired.has(id);

  // load imports the ID's entries not imported yet, skipping a partial's
  // entry whose component the document already ran (ids, lyr__ran).
  const load = (id: string): void => {
    for (const url of ([] as string[]).concat(entries[id] || [])) {
      const c = ids[url];
      if (imported.has(url) || (c && ran().has(c))) continue;
      if (c) ran().add(c);
      imported.add(url);
      importer(url).catch((e) => dev && console.error(`lyr: ${url}:`, e));
    }
  };

  const fire = (id: string): void => {
    if (fired.has(id)) return;
    fired.add(id);
    for (const f of cleanups[id] || []) f();
    load(id);
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
      const id = idOf(el);
      // A marker whose ID isn't in the map yet is looked at again: a
      // partial's config may add it.
      if (pending(id)) {
        seen.add(el);
        observe(el, id!);
      }
    }
  };

  window.lyr__lazy = (nodes, merge) => {
    if (merge) {
      ({ dev, lazy: entries = {}, ids = {} } = config());
      // An ID that fired gets the entries the new config lists for it.
      for (const id of fired) load(id);
    }
    scan(nodes);
  };
  scan([document]);
}
