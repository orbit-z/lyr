// Helpers shared by the blog's client scripts.

// plural formats a count with its noun: plural(1, "like") is "1 like".
export function plural(n: number, noun: string): string {
  return `${n} ${noun}${n === 1 ? "" : "s"}`;
}
