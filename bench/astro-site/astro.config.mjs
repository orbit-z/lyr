// @ts-check
import { defineConfig } from 'astro/config';
import node from '@astrojs/node';

// Every page renders per request, served by a single standalone Node process,
// mirroring bench/orbit-site.
export default defineConfig({
  output: 'server',
  adapter: node({ mode: 'standalone' }),
  // Orbit links its page stylesheet; do the same here rather than inline it.
  build: { inlineStylesheets: 'never' },
});
