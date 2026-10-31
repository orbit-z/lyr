// @ts-check
import { defineConfig, memoryCache } from 'astro/config';
import node from '@astrojs/node';

// Every page renders per request, served by a single standalone Node process,
// mirroring bench/lyr-site.
export default defineConfig({
  output: 'server',
  adapter: node({ mode: 'standalone' }),
  // Lyr links its page stylesheet; do the same here rather than inline it.
  build: { inlineStylesheets: 'never' },
  // In-memory route cache, like bench/lyr-site's cache.NewMemory: only pages
  // that call Astro.cache.set (/fetch-cached) are stored.
  cache: { provider: memoryCache() },
});
