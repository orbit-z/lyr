// Test harness: a fresh happy-dom page per test, with fetch,
// IntersectionObserver, requestIdleCallback and matchMedia mocked on
// globalThis (which the loader sees as window, like a browser).

import { Window } from "happy-dom";
// @ts-expect-error: internal module, no type declarations.
import HTMLParser from "happy-dom/lib/html-parser/HTMLParser.js";

const g = globalThis as any;

export interface Call {
  url: string;
  init: RequestInit;
}

export type Responder = (url: string, init: RequestInit) => Response | Promise<Response>;

export class FakeIO {
  static all: FakeIO[] = [];
  targets: Element[] = [];
  disconnected = false;
  constructor(
    public cb: (entries: { isIntersecting: boolean; target: Element }[]) => void,
    public options: { rootMargin?: string } = {},
  ) {
    FakeIO.all.push(this);
  }
  observe(el: Element) {
    this.targets.push(el);
  }
  disconnect() {
    this.disconnected = true;
  }
  // Reports el as intersecting, if this observer watches it.
  hit(el: Element) {
    if (!this.disconnected && this.targets.includes(el)) this.cb([{ isIntersecting: true, target: el }]);
  }
}

export class FakeMQL {
  static all: FakeMQL[] = [];
  listeners: ((e: { matches: boolean }) => void)[] = [];
  constructor(
    public media: string,
    public matches: boolean,
  ) {
    FakeMQL.all.push(this);
  }
  addEventListener(_: string, f: (e: { matches: boolean }) => void) {
    this.listeners.push(f);
  }
  removeEventListener(_: string, f: (e: { matches: boolean }) => void) {
    this.listeners = this.listeners.filter((l) => l !== f);
  }
  set(matches: boolean) {
    this.matches = matches;
    for (const l of [...this.listeners]) l({ matches });
  }
}

export interface Page {
  win: Window;
  document: Document;
  calls: Call[];
  idle: (() => void)[];
  timeouts: { f: () => void; ms: number }[];
  errors: unknown[][];
  warnings: unknown[][];
  imports: string[];
  importer: (url: string) => Promise<unknown>;
  // Runs every pending requestIdleCallback callback.
  runIdle(): void;
  // Reports every child element of the marker as intersecting.
  see(marker: Element): void;
}

export interface Options {
  dev?: boolean;
  lazy?: Record<string, string | string[]>;
  respond?: Responder;
  idle?: boolean; // whether requestIdleCallback exists (default true)
  media?: Record<string, boolean>; // initial matchMedia results
}

const origConsole = { error: console.error, warn: console.warn };

export function page(body: string, o: Options = {}): Page {
  const win = new Window({
    url: "http://localhost/",
    settings: { enableJavaScriptEvaluation: true, suppressInsecureJavaScriptEnvironmentWarning: true },
  });
  const document = win.document as unknown as Document;
  // Per the DOM spec, scripts in a Range.createContextualFragment result
  // run when inserted; happy-dom's parser marks them inert, so model the
  // browser here.
  (win.Range.prototype as any).createContextualFragment = (s: string) =>
    new HTMLParser(win, { evaluateScripts: true }).parse(s);
  const cfg = JSON.stringify({ dev: !!o.dev, lazy: o.lazy });
  document.head.innerHTML = `<script type="application/json" id="orbit-loader">${cfg}</script>`;
  document.body.innerHTML = body;

  const p: Page = {
    win,
    document,
    calls: [],
    idle: [],
    timeouts: [],
    errors: [],
    warnings: [],
    imports: [],
    importer: async (url) => {
      p.imports.push(url);
    },
    runIdle() {
      const fs = p.idle.splice(0);
      for (const f of fs) f();
    },
    see(marker) {
      for (const c of Array.from(marker.children)) for (const io of FakeIO.all) io.hit(c);
    },
  };

  FakeIO.all = [];
  FakeMQL.all = [];
  delete g.orbit__lazy;
  g.window = globalThis;
  g.document = document;
  g.MutationObserver = win.MutationObserver;
  g.IntersectionObserver = FakeIO;
  g.matchMedia = (q: string) => new FakeMQL(q, !!o.media?.[q]);
  if (o.idle === false) {
    delete g.requestIdleCallback;
  } else {
    g.requestIdleCallback = (f: () => void) => p.idle.push(f);
  }
  g.setTimeout = (f: () => void, ms: number) => p.timeouts.push({ f, ms });
  const respond = o.respond ?? (() => new Response("", { status: 404 }));
  g.fetch = async (url: string, init: RequestInit = {}) => {
    p.calls.push({ url, init });
    return respond(url, init);
  };
  console.error = (...a: unknown[]) => void p.errors.push(a);
  console.warn = (...a: unknown[]) => void p.warnings.push(a);
  return p;
}

const realSetTimeout = setTimeout;

// Restores the globals page() replaced that bun's own machinery uses.
export function restore() {
  g.setTimeout = realSetTimeout;
  console.error = origConsole.error;
  console.warn = origConsole.warn;
}

// Waits for pending promises, fetches and mutation observer callbacks.
export async function settle(times = 5) {
  for (let i = 0; i < times; i++) await new Promise<void>((r) => realSetTimeout(r, 0));
}
