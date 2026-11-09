// Page partials (PLAN.md §4k, §5f): a partial ends with the loader script,
// <script src="/_lyr/loader/loader-….js" data-lyr-loader='{…}'>, which the
// browser runs on every insertion (htmx re-creates swapped scripts). Each
// run imports the "run" entries the document hasn't run, and installs the
// islands and lazy loaders unless the host has them, then merges its
// config into them. Here a run is start(script, importer), as the browser
// would call it with document.currentScript. Also: the URL guards.

import { afterEach, describe, expect, test } from "bun:test";
import { start } from "../src/loader";
import { page, restore, settle } from "./dom";

afterEach(restore);

const g = globalThis as any;

const html = (body: string) => new Response(body, { status: 200, headers: { "content-type": "text/html" } });

// script is a partial's loader script with config cfg.
const script = (cfg: object) =>
  `<script src="/_lyr/loader/loader-x.js" data-lyr-loader='${JSON.stringify(cfg).replace(/'/g, "&#39;")}'></script>`;

// swap inserts a partial's HTML into the element with id (target by
// default), as htmx does, and returns its loader script, if any: the
// caller runs it with start.
const swap = (doc: Document, fragment: string, id = "target"): Element | null => {
  const t = doc.getElementById(id)!;
  t.innerHTML = fragment;
  return t.querySelector("script[data-lyr-loader]");
};

describe("run", () => {
  const cfg = {
    run: [
      ["List-1", "/_lyr/List-A.js"],
      ["Card-2", "/_lyr/Card-B.js"],
    ],
  };

  test("imports an entry once however many times the partial is swapped in", () => {
    const p = page(`<div id="target"></div>`);
    start(swap(p.document, script(cfg)), p.importer);
    start(swap(p.document, script(cfg)), p.importer);
    expect(p.imports).toEqual(["/_lyr/List-A.js", "/_lyr/Card-B.js"]);
  });

  test("skips a component the page bundle marked", () => {
    const p = page(`<div id="target"></div>`);
    // What a page bundle's mark module does.
    (g.lyr__ran ??= new Set()).add("List-1");
    start(swap(p.document, script(cfg)), p.importer);
    expect(p.imports).toEqual(["/_lyr/Card-B.js"]);
  });

  test("two partials sharing a component import it once", () => {
    const p = page(`<div id="target"></div><div id="second"></div>`);
    start(swap(p.document, script({ run: [["List-1", "/_lyr/List-A.js"]] })), p.importer);
    start(
      swap(
        p.document,
        script({
          run: [
            ["Other-3", "/_lyr/Other-C.js"],
            ["List-1", "/_lyr/List-A.js"],
          ],
        }),
        "second",
      ),
      p.importer,
    );
    expect(p.imports).toEqual(["/_lyr/List-A.js", "/_lyr/Other-C.js"]);
  });

  test("only the running script's own config runs entries", () => {
    const p = page(`<div id="target"></div>`);
    // An injected data block, or another element with the attribute, is
    // never read for "run".
    swap(
      p.document,
      `<script type="application/json" data-lyr-loader>{"run":[["X-1","/_lyr/X.js"]]}</script>` +
        `<div data-lyr-loader='{"run":[["Y-1","/_lyr/Y.js"]]}'></div>`,
    );
    start(null, p.importer); // a page's loader script: no data-lyr-loader
    start(p.document.querySelector("div[data-lyr-loader]"), p.importer);
    expect(p.imports).toEqual(["/_lyr/Y.js"]); // start trusts the element it's given: currentScript
    expect(g.lyr__ran.has("X-1")).toBe(false);
  });
});

describe("lazy loader in a partial", () => {
  const marker = `<lyr-lazy component="Tip-1" client="idle"><i>tip</i></lyr-lazy>`;
  const cfg = { dev: false, lazy: { "Tip-1": ["/_lyr/Tip-T.js"] }, ids: { "/_lyr/Tip-T.js": "Tip-1" } };

  test("host has one: the run merges its map and installs nothing again", async () => {
    const p = page(`<lyr-lazy component="A-1" client="idle"></lyr-lazy><div id="target"></div>`, {
      lazy: { "A-1": ["/_lyr/A.js"] },
    });
    start(null, p.importer);
    const installed = g.lyr__lazy;
    const me = swap(p.document, marker + script(cfg));
    // The host's islands observer hands the nodes over first, before the
    // partial's script runs: the marker must still be picked up.
    await settle();
    start(me, p.importer);
    expect(g.lyr__lazy).toBe(installed);
    p.runIdle();
    expect(p.imports.sort()).toEqual(["/_lyr/A.js", "/_lyr/Tip-T.js"]);
  });

  test("host lacks one: the run installs it", () => {
    const p = page(`<div id="target"></div>`);
    start(swap(p.document, marker + script(cfg)), p.importer);
    expect(p.idle).toHaveLength(1);
    p.runIdle();
    expect(p.imports).toEqual(["/_lyr/Tip-T.js"]);
  });

  test("two partials: the entry is imported once", () => {
    const p = page(`<div id="target"></div><div id="second"></div>`);
    start(swap(p.document, marker + script(cfg)), p.importer);
    start(swap(p.document, marker + script(cfg), "second"), p.importer);
    p.runIdle();
    expect(p.imports).toEqual(["/_lyr/Tip-T.js"]);
  });

  test("skips a component the host already ran", () => {
    const p = page(`<div id="target"></div>`);
    (g.lyr__ran ??= new Set()).add("Tip-1");
    start(swap(p.document, marker + script(cfg)), p.importer);
    p.runIdle();
    expect(p.imports).toEqual([]);
  });

  test("a lazy load marks the component, so a later run skips it", () => {
    const p = page(`<div id="target"></div><div id="second"></div>`);
    start(swap(p.document, marker + script(cfg)), p.importer);
    p.runIdle();
    start(swap(p.document, script({ run: [["Tip-1", "/_lyr/Tip-T.js"]] }), "second"), p.importer);
    expect(p.imports).toEqual(["/_lyr/Tip-T.js"]);
  });

  test("an ID that already fired gets the entries a partial adds", () => {
    const p = page(`<lyr-lazy component="Tip-1" client="idle"></lyr-lazy><div id="target"></div>`, {
      lazy: { "Tip-1": ["/_lyr/Tip-T.js"] },
    });
    start(null, p.importer);
    p.runIdle();
    start(
      swap(p.document, script({ lazy: { "Tip-1": ["/_lyr/Tip-T.js", "/_lyr/Icon-I.js"] }, ids: { "/_lyr/Icon-I.js": "Icon-2" } })),
      p.importer,
    );
    expect(p.imports).toEqual(["/_lyr/Tip-T.js", "/_lyr/Icon-I.js"]);
  });

  test("a config on an element other than a script is ignored", () => {
    const p = page(`<div id="target"></div>`);
    start(null, p.importer);
    swap(p.document, marker + `<div data-lyr-loader='${JSON.stringify(cfg)}'></div>`);
    g.lyr__lazy([p.document], true);
    p.runIdle();
    expect(p.imports).toEqual([]);
  });
});

describe("islands loader in a partial", () => {
  const placeholder = (name: string, attrs = "") =>
    `<lyr-island src="/_lyr/island/b/${name}" props="t"${attrs}><p>wait</p></lyr-island>`;

  test("host has one: the run installs nothing, the host's observer fetches once", async () => {
    const p = page(`<div id="target"></div>`, { respond: () => html("<p>done</p>") });
    start(null, p.importer);
    const installed = g.lyr__islands;
    const me = swap(p.document, placeholder("C-1") + script({}));
    await settle();
    start(me, p.importer);
    await settle();
    expect(g.lyr__islands).toBe(installed);
    expect(p.calls.map((c) => c.url)).toEqual(["/_lyr/island/b/C-1?p=t"]);
    expect(p.document.querySelector("#target p")!.textContent).toBe("done");
  });

  test("host lacks one: the run fetches, and later swaps are observed", async () => {
    const p = page(`<div id="target"></div><div id="later"></div>`, { respond: () => html("<p>done</p>") });
    start(swap(p.document, placeholder("C-1") + script({})), p.importer);
    await settle();
    const me = swap(p.document, placeholder("D-2") + script({}), "later");
    await settle();
    start(me, p.importer);
    await settle();
    expect(p.calls.map((c) => c.url)).toEqual(["/_lyr/island/b/C-1?p=t", "/_lyr/island/b/D-2?p=t"]);
  });

  test("a delayed placeholder a partial brings waits for its strategy", async () => {
    const p = page(`<div id="target"></div>`, { respond: () => html("<p>done</p>") });
    start(null, p.importer);
    start(swap(p.document, placeholder("Now-1") + placeholder("Late-2", ` client="idle"`) + script({})), p.importer);
    await settle();
    expect(p.calls.map((c) => c.url)).toEqual(["/_lyr/island/b/Now-1?p=t"]);
    p.runIdle();
    await settle();
    expect(p.calls.map((c) => c.url)).toEqual(["/_lyr/island/b/Now-1?p=t", "/_lyr/island/b/Late-2?p=t"]);
    expect(p.timeouts).toEqual([]); // no fallback timer
  });
});

// The URL guards (PLAN.md §5f): the loader imports only same-origin URLs
// under /_lyr/, and fetches islands only under /_lyr/island/, after
// resolving them, so traversal and other origins are refused.
describe("guards", () => {
  const bad = [
    "/_lyr/../x.js",
    "/_lyr/%2e%2e/x.js",
    "/_lyr/%2E%2E/x.js",
    "/_lyr/%2F..%2Fx.js", // %2F stays encoded: a decoding router would see /x.js
    "/_lyr/..%5Cx.js",
    "/_lyr/island/..%2F..%2Fx",
    "/_lyr\\..\\x.js",
    "\\\\evil.example/_lyr/x.js",
    "//evil.example/_lyr/x.js",
    "https://evil.example/_lyr/x.js",
    "javascript:alert(1)//_lyr/",
    "data:text/javascript,1",
    "/x/_lyr/x.js",
  ];

  for (const dev of [true, false]) {
    test(`run and lazy URLs outside /_lyr/ aren't imported${dev ? ", with a dev error" : ""}`, () => {
      const p = page(`<lyr-lazy component="L-1" client="load"></lyr-lazy><div id="target"></div>`, {
        dev,
        lazy: { "L-1": [...bad, "/_lyr/ok-lazy.js"] },
      });
      start(
        swap(p.document, script({ dev, run: [...bad.map((u, i) => [`B-${i}`, u]), ["OK-1", "http://localhost/_lyr/ok.js"]] })),
        p.importer,
      );
      expect(p.imports).toEqual(["/_lyr/ok.js", "/_lyr/ok-lazy.js"]);
      expect(p.errors).toHaveLength(dev ? 2 * bad.length : 0);
    });
  }

  test("a refused or failed import doesn't mark its component as run", async () => {
    const p = page(`<div id="target"></div><div id="second"></div>`);
    let fail = true;
    const importer = async (url: string) => {
      p.imports.push(url);
      if (fail) throw new Error("network");
    };
    start(swap(p.document, script({ run: [["R-1", "//evil.example/_lyr/r.js"], ["F-2", "/_lyr/f.js"]] })), importer);
    expect(g.lyr__ran.has("R-1")).toBe(false);
    expect(g.lyr__ran.has("F-2")).toBe(true); // pending
    await settle();
    expect(g.lyr__ran.has("F-2")).toBe(false);
    fail = false;
    start(swap(p.document, script({ run: [["R-1", "/_lyr/r.js"], ["F-2", "/_lyr/f.js"]] }), "second"), importer);
    expect(p.imports).toEqual(["/_lyr/f.js", "/_lyr/r.js", "/_lyr/f.js"]);
    expect([...g.lyr__ran].sort()).toEqual(["F-2", "R-1"]);
  });

  test("an island src outside /_lyr/island/ isn't fetched", async () => {
    const p = page(
      [...bad, "/_lyr/x.js", "/_lyr/island/../x"].map((u) => `<lyr-island src="${u}" props="t"></lyr-island>`).join("") +
        `<lyr-island src="/_lyr/island/b/Ok-1" props="t"></lyr-island>`,
      { dev: true, respond: () => html("ok") },
    );
    start(null, p.importer);
    await settle();
    expect(p.calls.map((c) => c.url)).toEqual(["/_lyr/island/b/Ok-1?p=t"]);
    expect(p.errors).toHaveLength(bad.length + 2);
  });
});
