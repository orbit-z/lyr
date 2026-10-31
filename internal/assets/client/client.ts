// lyr:client, the runtime of the components' client modules (PLAN.md §5c):
// import { data } from "./X.lyr" resolves to a re-export of these, typed
// by lyr_gen/types/<path>.lyr.d.ts.

// data parses the JSON of el's data-* attribute for a dataset key ("authorName"
// reads data-author-name).
export function data(el: Element, key: string): unknown {
  const name = "data-" + key.replace(/[A-Z]/g, (c) => "-" + c.toLowerCase());
  const v = el.getAttribute(name);
  if (v === null) throw new Error(`lyr: <${el.localName}> has no ${name} attribute`);
  return JSON.parse(v);
}

// json parses the set:json block with this id.
export function json(id: string): unknown {
  const el = document.getElementById(id);
  if (el === null) throw new Error(`lyr: no element with id "${id}"`);
  return JSON.parse(el.textContent!);
}
