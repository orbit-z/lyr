// The loader script (PLAN.md §5 "The loader", §5f): one classic script,
// /_lyr/loader/loader-<hash>.js, for islands, delayed islands, lazy
// components and page partials' component scripts.
//
// A page loads it with <script defer src> after its config, a data block
// (<script type="application/json" id="lyr-loader">). A page partial
// (PLAN.md §4k) ends with <script src … data-lyr-loader='{…}'>: classic,
// so the browser runs it again on every insertion (htmx re-creates the
// scripts it swaps in), while the islands and lazy loaders install once
// per document and later runs merge their config.

import { islands } from "./islands";
import { lazy } from "./lazy";
import { isDev, load, parse, ran, type Importer } from "./shared";

// start runs the loader. me is document.currentScript: a partial's loader
// script carries its config, whose "run" entries are imported unless the
// document already ran their component (lyr__ran). Only the running
// script's own config is read for "run", never another element's.
export function start(me: Element | null, importer: Importer): void {
  if (me?.hasAttribute("data-lyr-loader")) {
    const dev = isDev();
    for (const [id, url] of parse(me).run || []) if (!ran().has(id)) load(importer, url, dev, id);
  }
  lazy(importer);
  islands();
}
