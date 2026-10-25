// Shared by both loader scripts. Each script is bundled on its own, so
// this module is inlined into each and they share no state at runtime.

// The page's loader config, written by the runtime's head injection
// before the module scripts:
//
//	<script type="application/json" id="orbit-loader">{"dev":false,"lazy":{"<ID>":["<url>",…]}}</script>
//
// "lazy" is left out on pages without lazy entries. A single string value
// is accepted too.
export interface Config {
  dev?: boolean;
  lazy?: Record<string, string | string[]>;
}

export const config = (): Config =>
  JSON.parse(document.getElementById("orbit-loader")?.textContent || "{}");

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

declare global {
  interface Window {
    // The islands → lazy hand-off. The lazy script sets it to its scan
    // function; the islands script calls it with the nodes added to the
    // page (island fragments included), so <orbit-lazy> markers that
    // arrive later are observed too. Optional because the lazy script is
    // only on pages that have lazy entries.
    orbit__lazy?: (nodes: Node[]) => void;
  }
}
