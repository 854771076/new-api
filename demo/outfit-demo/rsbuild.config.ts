import { defineConfig } from '@rsbuild/core';
import { pluginReact } from '@rsbuild/plugin-react';

export default defineConfig({
  plugins: [pluginReact()],
  server: {
    port: 5173,
    host: true,
  },
  source: {
    entry: { index: './src/main.tsx' },
  },
  html: {
    template: './index.html',
  },
});
