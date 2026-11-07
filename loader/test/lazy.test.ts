import { afterEach, describe, expect, test } from "bun:test";
import { lazy } from "../src/lazy";
import { FakeIO, FakeMQL, page, restore, settle } from "./dom";

afterEach(restore);

const marker = (id: string, client: string, inner = "<div>c</div>", opts?: string) =>
  `<lyr-lazy component="${id}" client="${client}"${opts ? ` opts="${opts}"` : ""}>${inner}</lyr-lazy>`;

describe("lazy", () => {
  test("client:idle imports once the browser is idle", () => {
    const p = page(marker("A-1", "idle"), { lazy: { "A-1": "/_lyr/A.js" } });
    lazy(p.importer);
    expect(p.imports).toEqual([]);
    expect(p.idle).toHaveLength(1);
    p.runIdle();
    expect(p.imports).toEqual(["/_lyr/A.js"]);
  });

  test("client:idle falls back to setTimeout without requestIdleCallback", () => {
    const p = page(marker("A-1", "idle"), { lazy: { "A-1": "/_lyr/A.js" }, idle: false });
    lazy(p.importer);
    expect(p.imports).toEqual([]);
    expect(p.timeouts).toHaveLength(1);
    expect(p.timeouts[0]!.ms).toBe(200);
    p.timeouts[0]!.f();
    expect(p.imports).toEqual(["/_lyr/A.js"]);
  });

  test("client:visible observes the element children with the rootMargin", () => {
    const p = page(marker("V-1", "visible", "text<p>one</p><p>two</p>", "200px"), {
      lazy: { "V-1": "/_lyr/V.js" },
    });
    lazy(p.importer);
    expect(FakeIO.all).toHaveLength(1);
    const io = FakeIO.all[0]!;
    expect(io.options.rootMargin).toBe("200px");
    expect(io.targets.map((t) => t.textContent)).toEqual(["one", "two"]);
    expect(p.imports).toEqual([]);
    io.hit(io.targets[1]!);
    expect(p.imports).toEqual(["/_lyr/V.js"]);
    expect(io.disconnected).toBe(true);
  });

  for (const dev of [true, false]) {
    test(`client:visible with no element child imports at once and ${dev ? "warns once in dev" : "is silent in prod"}`, () => {
      const p = page(marker("T-1", "visible", "just text") + marker("T-1", "visible", ""), {
        dev,
        lazy: { "T-1": "/_lyr/T.js" },
      });
      lazy(p.importer);
      expect(p.imports).toEqual(["/_lyr/T.js"]);
      expect(FakeIO.all).toHaveLength(0);
      expect(p.warnings).toEqual(dev ? [["lyr: T-1: client:visible has no element to observe, so it was loaded at once"]] : []);
    });
  }

  test("a delayed island's placeholder imports its entries when its strategy fires", () => {
    const p = page(
      `<lyr-island src="/_lyr/island/b1/Comments-1" props="t" client="media" opts="(min-width: 900px)"><p>wait</p></lyr-island>`,
      { lazy: { "Comments-1": ["/_lyr/Comments.js"] } },
    );
    lazy(p.importer);
    const mq = FakeMQL.all[0]!;
    expect(mq.media).toBe("(min-width: 900px)");
    expect(p.imports).toEqual([]);
    mq.set(true);
    expect(p.imports).toEqual(["/_lyr/Comments.js"]);
  });

  test("a bare placeholder isn't a marker, and an eager delayed island's ID is ignored", () => {
    const p = page(
      `<lyr-island src="/i/b1/A-1" props="t"><p>x</p></lyr-island>` +
        `<lyr-island src="/i/b1/E-1" props="t" client="idle"><p>x</p></lyr-island>`,
      { lazy: { "A-1": "/_lyr/A.js" } },
    );
    lazy(p.importer);
    expect(p.idle).toHaveLength(0);
    expect(FakeIO.all).toHaveLength(0);
    expect(p.imports).toEqual([]);
  });

  test("a lazy marker in a delayed island's fallback waits for the placeholder", () => {
    const p = page(
      `<lyr-island src="/i/b1/D-1" props="t" client="idle">${marker("S-1", "idle")}</lyr-island>`,
      { lazy: { "D-1": "/_lyr/D.js", "S-1": "/_lyr/S.js" } },
    );
    lazy(p.importer);
    expect(p.idle).toHaveLength(1);
    p.runIdle();
    expect(p.imports).toEqual(["/_lyr/D.js"]);
    p.runIdle();
    expect(p.imports).toEqual(["/_lyr/D.js", "/_lyr/S.js"]);
  });

  test("client:media imports at load when the query matches", () => {
    const p = page(marker("M-1", "media", undefined, "(max-width: 600px)"), {
      lazy: { "M-1": "/_lyr/M.js" },
      media: { "(max-width: 600px)": true },
    });
    lazy(p.importer);
    expect(p.imports).toEqual(["/_lyr/M.js"]);
  });

  test("client:media imports when the query starts matching later", () => {
    const p = page(marker("M-1", "media", undefined, "(max-width: 600px)"), { lazy: { "M-1": "/_lyr/M.js" } });
    lazy(p.importer);
    const mq = FakeMQL.all[0]!;
    expect(mq.media).toBe("(max-width: 600px)");
    expect(p.imports).toEqual([]);
    mq.set(false);
    expect(p.imports).toEqual([]);
    mq.set(true);
    mq.set(false);
    mq.set(true);
    expect(p.imports).toEqual(["/_lyr/M.js"]);
    expect(mq.listeners).toHaveLength(0);
  });

  test("multiple instances import once, whichever fires first", () => {
    const p = page(marker("C-1", "visible") + marker("C-1", "visible") + marker("C-1", "idle"), {
      lazy: { "C-1": "/_lyr/C.js" },
    });
    lazy(p.importer);
    const ms = p.document.querySelectorAll("lyr-lazy");
    p.see(ms[1]!);
    p.see(ms[0]!);
    p.runIdle();
    expect(p.imports).toEqual(["/_lyr/C.js"]);
    expect(FakeIO.all.every((io) => io.disconnected)).toBe(true);
  });

  test("an ID with several entry URLs imports each once", () => {
    const p = page(marker("S-1", "idle") + marker("T-1", "idle"), {
      lazy: { "S-1": ["/_lyr/a.js", "/_lyr/b.js"], "T-1": ["/_lyr/b.js", "/_lyr/c.js"] },
    });
    lazy(p.importer);
    p.runIdle();
    expect(p.imports).toEqual(["/_lyr/a.js", "/_lyr/b.js", "/_lyr/c.js"]);
  });

  test("unknown IDs are ignored, and lazy markers inside them are observed", () => {
    const p = page(marker("Eager-1", "idle", marker("In-1", "idle")), { lazy: { "In-1": "/_lyr/In.js" } });
    lazy(p.importer);
    expect(p.idle).toHaveLength(1);
    p.runIdle();
    expect(p.imports).toEqual(["/_lyr/In.js"]);
  });

  test("a nested lazy marker waits for the outer one to fire", () => {
    const p = page(marker("Outer-1", "idle", `<div>${marker("Map-1", "visible", "<div>map</div>")}</div>`), {
      lazy: { "Outer-1": "/_lyr/Outer.js", "Map-1": "/_lyr/Map.js" },
    });
    lazy(p.importer);
    // Only the outer marker is observed.
    expect(FakeIO.all).toHaveLength(0);
    const inner = p.document.querySelector('[component="Map-1"]')!;
    p.see(inner);
    expect(p.imports).toEqual([]);
    p.runIdle();
    expect(p.imports).toEqual(["/_lyr/Outer.js"]);
    expect(FakeIO.all).toHaveLength(1);
    p.see(inner);
    expect(p.imports).toEqual(["/_lyr/Outer.js", "/_lyr/Map.js"]);
  });

  test("markers added later through the hand-off are observed", () => {
    const p = page(`<div id="root"></div>`, { lazy: { "A-1": "/_lyr/A.js" } });
    lazy(p.importer);
    const root = p.document.getElementById("root")!;
    root.innerHTML = marker("A-1", "idle");
    window.lyr__lazy!([root.firstChild!]);
    window.lyr__lazy!([root]); // already seen: no second observation
    expect(p.idle).toHaveLength(1);
    p.runIdle();
    expect(p.imports).toEqual(["/_lyr/A.js"]);
  });

  test("a failed import logs in dev only", async () => {
    for (const dev of [true, false]) {
      const p = page(marker("A-1", "idle"), { dev, lazy: { "A-1": "/_lyr/A.js" } });
      lazy(() => Promise.reject(new Error("404")));
      p.runIdle();
      await settle(1);
      expect(p.errors).toHaveLength(dev ? 1 : 0);
    }
  });

  test("a page without a lazy map imports nothing", () => {
    const p = page(marker("A-1", "idle"));
    lazy(p.importer);
    p.runIdle();
    expect(p.idle).toHaveLength(0);
    expect(p.imports).toEqual([]);
  });
});
