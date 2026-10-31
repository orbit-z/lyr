// The benchmark's fixed data set. It mirrors
// bench/lyr-site/internal/data/data.go exactly, so both sites render the
// same text.

export interface Item {
  id: number;
  title: string;
  summary: string;
  href: string;
  likes: number;
  featured: boolean;
  tags: string[];
}

const tagSet = ['go', 'astro', 'ssr', 'html', 'css'];

function makeItems(n: number): Item[] {
  const items: Item[] = [];
  for (let i = 0; i < n; i++) {
    const id = i + 1;
    items.push({
      id,
      title: `Card ${id} <&> friends`,
      summary: `Summary of card ${id}: "quotes" & <tags> are escaped.`,
      href: `/items/${id}?ref=bench&page=${id % 7}`,
      likes: (id * 37) % 1000,
      featured: id % 3 === 0,
      tags: [tagSet[id % 5], tagSet[(id + 2) % 5]],
    });
  }
  return items;
}

/** The 500-card list of the render-heavy page. */
export const cards = makeItems(500);

const fetched = makeItems(20);

/** Simulates a 10 ms data fetch and returns a modest list. */
export async function fetchItems(): Promise<Item[]> {
  await new Promise((resolve) => setTimeout(resolve, 10));
  return fetched;
}
