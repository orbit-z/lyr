// Page partials (PLAN.md §4k): the runner's run-once guard, and the loaders
// a partial brings, which must be idempotent: the host has one already,
// the host has none, or two partials bring one each.

import { afterEach, describe, expect, test } from "bun:test";
import { CLAIM_MS, islands } from "../src/islands";
import { lazy } from "../src/lazy";
import { page, restore, settle } from "./dom";

afterEach(restore);

const g = globalThis as any;

const html = (body: string) => new Response(body, { status: 200, headers: { "content-type": "text/html" } });

// runner returns the runner the runtime writes at the end of a partial
// (partialTail in loader.go, whose test asserts the same text), as a
// function, with import() replaced by the page's importer.
const runner = (pairs: [string, string][]) => {
  const src = `let s=globalThis.lyr__ran??=new Set;for(let[i,u]of${JSON.stringify(pairs)})s.has(i)||(s.add(i),import(u))`;
  return (importer: (u: string) => unknown) => new Function("lyr__import", src.replace("import(u)", "lyr__import(u)"))(importer);
};

// partialConfig is a partial's loader config element.
const partialConfig = (cfg: object) => `<script type="application/json" data-lyr-loader>${JSON.stringify(cfg)}</script>`;

// swap inserts a partial's HTML into #target, as htmx does.
const swap = (doc: Document, fragment: string) => {
  doc.getElementById("target")!.innerHTML = fragment;
};

describe("runner", () => {
  test("imports an entry once however many times the partial is swapped in", () => {
    const p = page(`<div id="target"></div>`);
    const run = runner([
      ["List-1", "/_lyr/List-A.js"],
      ["Card-2", "/_lyr/Card-B.js"],
    ]);
    run(p.importer);
    run(p.importer);
    expect(p.imports).toEqual(["/_lyr/List-A.js", "/_lyr/Card-B.js"]);
  });

  test("skips a component the page bundle marked", () => {
    const p = page(`<div id="target"></div>`);
    // What a page bundle's mark module does.
    (g.lyr__ran ??= new Set()).add("List-1");
    runner([
      ["List-1", "/_lyr/List-A.js"],
      ["Card-2", "/_lyr/Card-B.js"],
    ])(p.importer);
    expect(p.imports).toEqual(["/_lyr/Card-B.js"]);
  });

  test("two partials sharing a component import it once", () => {
    const p = page(`<div id="target"></div>`);
    runner([["List-1", "/_lyr/List-A.js"]])(p.importer);
    runner([
      ["Other-3", "/_lyr/Other-C.js"],
      ["List-1", "/_lyr/List-A.js"],
    ])(p.importer);
    expect(p.imports).toEqual(["/_lyr/List-A.js", "/_lyr/Other-C.js"]);
  });
});

describe("lazy loader in a partial", () => {
  const marker = `<lyr-lazy component="Tip-1" client="idle"><i>tip</i></lyr-lazy>`;
  const cfg = { dev: false, lazy: { "Tip-1": ["/_lyr/Tip-T.js"] }, ids: { "/_lyr/Tip-T.js": "Tip-1" } };

  test("host has one: the partial's copy merges its map and installs nothing again", async () => {
    const p = page(`<lyr-lazy component="A-1" client="idle"></lyr-lazy><div id="target"></div>`, {
      lazy: { "A-1": ["/_lyr/A.js"] },
    });
    lazy(p.importer);
    const installed = g.lyr__lazy;
    swap(p.document, marker + partialConfig(cfg));
    // The host's islands observer would hand the nodes over first, before
    // the partial's config is read: the marker must still be picked up.
    installed([p.document.getElementById("target")!]);
    lazy(p.importer); // the partial's copy
    expect(g.lyr__lazy).toBe(installed);
    p.runIdle();
    expect(p.imports.sort()).toEqual(["/_lyr/A.js", "/_lyr/Tip-T.js"]);
  });

  test("host lacks one: the partial's copy installs itself", () => {
    const p = page(`<div id="target"></div>`);
    swap(p.document, marker + partialConfig(cfg));
    lazy(p.importer);
    expect(p.idle).toHaveLength(1);
    p.runIdle();
    expect(p.imports).toEqual(["/_lyr/Tip-T.js"]);
  });

  test("two partials: the entry is imported once", () => {
    const p = page(`<div id="target"></div><div id="second"></div>`);
    swap(p.document, marker + partialConfig(cfg));
    lazy(p.importer);
    p.document.getElementById("second")!.innerHTML = marker + partialConfig(cfg);
    lazy(p.importer);
    p.runIdle();
    expect(p.imports).toEqual(["/_lyr/Tip-T.js"]);
  });

  test("skips a component the host already ran", () => {
    const p = page(`<div id="target"></div>`);
    (g.lyr__ran ??= new Set()).add("Tip-1");
    swap(p.document, marker + partialConfig(cfg));
    lazy(p.importer);
    p.runIdle();
    expect(p.imports).toEqual([]);
  });

  test("a lazy load marks the component, so a later runner skips it", () => {
    const p = page(`<div id="target"></div>`);
    swap(p.document, marker + partialConfig(cfg));
    lazy(p.importer);
    p.runIdle();
    runner([["Tip-1", "/_lyr/Tip-T.js"]])(p.importer);
    expect(p.imports).toEqual(["/_lyr/Tip-T.js"]);
  });

  test("an ID that already fired gets the entries a partial adds", () => {
    const p = page(`<lyr-lazy component="Tip-1" client="idle"></lyr-lazy><div id="target"></div>`, {
      lazy: { "Tip-1": ["/_lyr/Tip-T.js"] },
    });
    lazy(p.importer);
    p.runIdle();
    swap(
      p.document,
      partialConfig({ lazy: { "Tip-1": ["/_lyr/Tip-T.js", "/_lyr/Icon-I.js"] }, ids: { "/_lyr/Icon-I.js": "Icon-2" } }),
    );
    lazy(p.importer);
    expect(p.imports).toEqual(["/_lyr/Tip-T.js", "/_lyr/Icon-I.js"]);
  });
});

describe("islands loader in a partial", () => {
  const placeholder = (name: string, attrs = "") => `<lyr-island src="/_lyr/island/b/${name}" props="t"${attrs}><p>wait</p></lyr-island>`;

  test("host has one: the partial's copy does nothing, the host's observer fetches once", async () => {
    const p = page(`<div id="target"></div>`, { respond: () => html("<p>done</p>") });
    islands();
    const installed = g.lyr__islands;
    swap(p.document, placeholder("C-1"));
    await settle();
    islands(); // the partial's copy
    await settle();
    expect(g.lyr__islands).toBe(installed);
    expect(p.calls.map((c) => c.url)).toEqual(["/_lyr/island/b/C-1?p=t"]);
    expect(p.document.getElementById("target")!.innerHTML).toBe("<p>done</p>");
  });

  test("host lacks one: the partial's copy fetches, and later swaps are observed", async () => {
    const p = page(`<div id="target"></div><div id="later"></div>`, { respond: () => html("<p>done</p>") });
    swap(p.document, placeholder("C-1"));
    islands();
    await settle();
    p.document.getElementById("later")!.innerHTML = placeholder("D-2");
    islands(); // a second partial's copy
    await settle();
    expect(p.calls.map((c) => c.url)).toEqual(["/_lyr/island/b/C-1?p=t", "/_lyr/island/b/D-2?p=t"]);
  });

  test("the strategy-reading variant takes over from the plain one", async () => {
    const p = page(`<div id="target"></div>`, { respond: () => html("<p>done</p>") });
    g.LYR_WHEN = false;
    islands(); // the host's plain loader
    swap(p.document, placeholder("Now-1") + placeholder("Late-2", ` client="idle"`));
    await settle();
    // The plain loader fetched the plain placeholder, not the delayed one.
    expect(p.calls.map((c) => c.url)).toEqual(["/_lyr/island/b/Now-1?p=t"]);
    g.LYR_WHEN = true;
    islands(); // the partial's variant
    expect(g.lyr__islands.w).toBe(true);
    await settle();
    expect(p.calls).toHaveLength(1);
    p.runIdle();
    await settle();
    expect(p.calls.map((c) => c.url)).toEqual(["/_lyr/island/b/Now-1?p=t", "/_lyr/island/b/Late-2?p=t"]);
    // Later swaps go through the variant's scan, with one observer.
    p.document.body.insertAdjacentHTML("beforeend", placeholder("Again-3", ` client="idle"`));
    await settle();
    expect(p.calls).toHaveLength(2);
    p.runIdle();
    await settle();
    expect(p.calls.map((c) => c.url)).toEqual([
      "/_lyr/island/b/Now-1?p=t",
      "/_lyr/island/b/Late-2?p=t",
      "/_lyr/island/b/Again-3?p=t",
    ]);
    // The plain loader's fallback timer finds Late-2 claimed: no early fetch.
    for (const t of p.timeouts.splice(0)) t.f();
    await settle();
    expect(p.calls).toHaveLength(3);
    expect(p.warnings).toEqual([]);
  });

  test("the variant claims a delayed placeholder before the plain loader's fallback fires", async () => {
    const p = page(`<div id="target"></div>`, { respond: () => html("<p>done</p>") });
    g.LYR_WHEN = false;
    islands();
    swap(p.document, placeholder("Late-2", ` client="idle"`));
    await settle();
    expect(p.timeouts.map((t) => t.ms)).toEqual([CLAIM_MS]);
    g.LYR_WHEN = true;
    islands(); // the partial's module script runs after the observer
    for (const t of p.timeouts.splice(0)) t.f();
    await settle();
    expect(p.calls).toHaveLength(0); // the strategy is honored
    p.runIdle();
    await settle();
    expect(p.calls.map((c) => c.url)).toEqual(["/_lyr/island/b/Late-2?p=t"]);
  });

  for (const dev of [true, false]) {
    test(`without the variant (innerHTML), the plain loader fetches at once${dev ? ", with a dev warning" : ""}`, async () => {
      const p = page(`<div id="target"></div>`, { respond: () => html("<p>done</p>"), dev });
      g.LYR_WHEN = false;
      islands();
      swap(p.document, placeholder("Late-2", ` client="idle"`) + placeholder("Late-2", ` client="visible"`));
      await settle();
      expect(p.calls).toHaveLength(0);
      for (const t of p.timeouts.splice(0)) t.f();
      await settle();
      expect(p.calls.map((c) => c.url)).toEqual(["/_lyr/island/b/Late-2?p=t", "/_lyr/island/b/Late-2?p=t"]);
      expect(p.warnings).toHaveLength(dev ? 1 : 0); // once per src
      if (dev) expect(String(p.warnings[0]![0])).toContain("its loader never ran");
    });
  }

  test("a placeholder removed before the fallback fires isn't fetched", async () => {
    const p = page(`<div id="target"></div>`, { respond: () => html("<p>done</p>") });
    g.LYR_WHEN = false;
    islands();
    swap(p.document, placeholder("Late-2", ` client="idle"`));
    await settle();
    swap(p.document, "");
    for (const t of p.timeouts.splice(0)) t.f();
    await settle();
    expect(p.calls).toHaveLength(0);
  });

  test("a plain copy after the variant changes nothing", async () => {
    const p = page(`<div id="target"></div>`, { respond: () => html("<p>done</p>"), when: true });
    islands();
    const installed = g.lyr__islands;
    g.LYR_WHEN = false;
    islands();
    expect(g.lyr__islands).toBe(installed);
    // In a browser each copy has its own constant; here it's one global.
    g.LYR_WHEN = true;
    swap(p.document, placeholder("Late-2", ` client="idle"`));
    await settle();
    expect(p.calls).toHaveLength(0);
    p.runIdle();
    await settle();
    expect(p.calls).toHaveLength(1);
    expect(p.timeouts).toEqual([]); // no fallback timer with the variant installed
  });
});
