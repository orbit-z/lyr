// Helpers shared by the blog's client scripts. LikeButton (in the post
// page's bundle) and the lazy ShareButtons both import this file, so it's
// a shared chunk: loaded once, with one module state (PLAN.md §5e).

// plural formats a count with its noun: plural(1, "like") is "1 like".
export function plural(n: number, noun: string): string {
  return `${n} ${noun}${n === 1 ? "" : "s"}`;
}

// flash shows text on a button for a moment, then puts its label back.
export function flash(button: HTMLElement, text: string): void {
  const label = button.textContent;
  button.textContent = text;
  setTimeout(() => (button.textContent = label), 1500);
}
