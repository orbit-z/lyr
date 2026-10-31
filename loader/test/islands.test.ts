import { afterEach, describe, expect, test } from "bun:test";
import { islands, MAX_URL } from "../src/islands";
import { lazy } from "../src/lazy";
import { page, restore, settle } from "./dom";

afterEach(restore);

const html = (body: string, status = 200) =>
  new Response(body, { status, headers: { "content-type": "text/html; charset=utf-8" } });

describe("islands", () => {
  test("fetches every island in parallel and replaces the placeholders", async () => {
    const pending: (() => void)[] = [];
    const p = page(
      `<main><lyr-island src="/_lyr/island/b1/A-1" props="tokA"><p>loading A</p></lyr-island>` +
        `<lyr-island src="/_lyr/island/b1/B-2" props="tok+B/="><p>loading B</p></lyr-island></main>`,
      {
        respond: (url) =>
          new Promise((r) => pending.push(() => r(html(url.includes("A-1") ? "<p>A!</p>" : "<b>B!</b><i>b</i>")))),
      },
    );
    islands();
    await settle();
    // Both requests are in flight before either answers.
    expect(p.calls.map((c) => c.url)).toEqual([
      "/_lyr/island/b1/A-1?p=tokA",
      "/_lyr/island/b1/B-2?p=tok%2BB%2F%3D",
    ]);
    for (const c of p.calls) {
      expect(c.init.redirect).toBe("manual");
      expect(c.init.method ?? "GET").toBe("GET");
    }
    pending.forEach((f) => f());
    await settle();
    expect(p.document.querySelector("main")!.innerHTML).toBe("<p>A!</p><b>B!</b><i>b</i>");
  });

  test("inline scripts in a fragment run", async () => {
    const p = page(`<lyr-island src="/i/A" props="t"></lyr-island>`, {
      respond: () => html(`<p id="x">x</p><script>document.getElementById("x").dataset.ran = "yes"</script>`),
    });
    islands();
    await settle();
    expect((p.document.getElementById("x") as HTMLElement).dataset.ran).toBe("yes");
  });

  test("a nested island inside a fetched fragment loads", async () => {
    const p = page(`<div id="root"><lyr-island src="/i/Outer" props="o">…</lyr-island></div>`, {
      respond: (url) =>
        url.startsWith("/i/Outer")
          ? html(`<section>outer <lyr-island src="/i/Inner" props="n"><em>wait</em></lyr-island></section>`)
          : html(`<span>inner</span>`),
    });
    islands();
    await settle(10);
    expect(p.calls.map((c) => c.url)).toEqual(["/i/Outer?p=o", "/i/Inner?p=n"]);
    expect(p.document.getElementById("root")!.innerHTML).toBe("<section>outer <span>inner</span></section>");
  });

  test("an island inserted later by the page loads", async () => {
    const p = page(`<div id="root"></div>`, { respond: () => html(`<p>late</p>`) });
    islands();
    await settle();
    expect(p.calls).toHaveLength(0);
    p.document.getElementById("root")!.innerHTML = `<lyr-island src="/i/L" props="l"></lyr-island>`;
    await settle();
    expect(p.calls.map((c) => c.url)).toEqual(["/i/L?p=l"]);
    expect(p.document.getElementById("root")!.innerHTML).toBe("<p>late</p>");
  });

  for (const dev of [true, false]) {
    test(`a 500 keeps the fallback and ${dev ? "logs in dev" : "stays silent in prod"}`, async () => {
      const p = page(`<div id="root"><lyr-island src="/i/E" props="e"><p>fallback</p></lyr-island></div>`, {
        dev,
        respond: () => html("boom at Comments.lyr:3:1", 500),
      });
      islands();
      await settle();
      expect(p.document.getElementById("root")!.innerHTML).toBe(
        `<lyr-island src="/i/E" props="e"><p>fallback</p></lyr-island>`,
      );
      if (dev) {
        expect(p.errors).toHaveLength(1);
        expect(String(p.errors[0]![0])).toContain("500");
        expect(p.errors[0]![1]).toBe("boom at Comments.lyr:3:1");
      } else {
        expect(p.errors).toHaveLength(0);
      }
    });

    test(`a network error keeps the fallback (${dev ? "dev" : "prod"})`, async () => {
      const p = page(`<div id="root"><lyr-island src="/i/E" props="e"><p>fallback</p></lyr-island></div>`, {
        dev,
        respond: () => {
          throw new TypeError("network down");
        },
      });
      islands();
      await settle();
      expect(p.document.querySelector("lyr-island p")!.textContent).toBe("fallback");
      expect(p.errors).toHaveLength(dev ? 1 : 0);
    });
  }

  test("a redirect (opaque, not followed) keeps the fallback", async () => {
    const p = page(`<lyr-island src="/i/R" props="r"><p>fallback</p></lyr-island>`, {
      respond: () => new Response(null, { status: 302, headers: { location: "/login" } }),
    });
    islands();
    await settle();
    expect(p.calls[0]!.init.redirect).toBe("manual");
    expect(p.document.querySelector("lyr-island p")!.textContent).toBe("fallback");
  });

  for (const dev of [true, false]) {
    test(`a token over ${MAX_URL} characters is POSTed as text/plain and ${dev ? "warns once in dev" : "is silent in prod"}`, async () => {
      const big = "x".repeat(MAX_URL);
      const p = page(
        `<lyr-island src="/i/Big" props="${big}"></lyr-island><lyr-island src="/i/Big" props="${big}"></lyr-island>` +
          `<lyr-island src="/i/Small" props="s"></lyr-island>`,
        { dev, respond: () => html(`<p>ok</p>`) },
      );
      islands();
      await settle();
      const posts = p.calls.filter((c) => c.init.method === "POST");
      expect(posts).toHaveLength(2);
      for (const c of posts) {
        expect(c.url).toBe("/i/Big");
        expect(c.init.body).toBe(big);
        expect((c.init.headers as Record<string, string>)["content-type"]).toBe("text/plain");
        expect(c.init.redirect).toBe("manual");
      }
      expect(p.calls.find((c) => c.url.startsWith("/i/Small"))!.init.method).toBeUndefined();
      expect(p.warnings).toHaveLength(dev ? 1 : 0);
      if (dev) expect(String(p.warnings[0]![0])).toContain("POST");
    });
  }

  test("a URL of exactly the limit still uses GET", async () => {
    const src = "/i/X";
    const token = "y".repeat(MAX_URL - (src + "?p=").length);
    const p = page(`<lyr-island src="${src}" props="${token}"></lyr-island>`, { respond: () => html("") });
    islands();
    await settle();
    expect(p.calls[0]!.url.length).toBe(MAX_URL);
    expect(p.calls[0]!.init.method).toBeUndefined();
  });

  test("lazy markers inside a fetched island are handed to the lazy loader", async () => {
    const p = page(`<lyr-island src="/i/C" props="c"></lyr-island>`, {
      lazy: { "Map-1": "/_lyr/Map.js" },
      respond: () => html(`<lyr-lazy component="Map-1" client="visible"><div>map</div></lyr-lazy>`),
    });
    islands();
    lazy(p.importer);
    await settle();
    const marker = p.document.querySelector("lyr-lazy")!;
    expect(p.imports).toEqual([]);
    p.see(marker);
    expect(p.imports).toEqual(["/_lyr/Map.js"]);
  });

  test("the hand-off works when the lazy loader starts after a fragment arrived", async () => {
    const p = page(`<lyr-island src="/i/C" props="c"></lyr-island>`, {
      lazy: { "Late-1": "/_lyr/Late.js" },
      respond: () => html(`<lyr-lazy component="Late-1" client="idle"><div>x</div></lyr-lazy>`),
    });
    islands();
    await settle();
    lazy(p.importer);
    p.runIdle();
    expect(p.imports).toEqual(["/_lyr/Late.js"]);
  });
});
