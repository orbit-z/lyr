// count adds 1 to <html data-<name>>: how many times a script ran.
export function count(name: string): void {
  const d = document.documentElement.dataset;
  d[name] = String(Number(d[name] || 0) + 1);
}
