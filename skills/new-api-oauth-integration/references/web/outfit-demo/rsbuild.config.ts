import { defineConfig, loadEnv } from '@rsbuild/core';
import { pluginReact } from '@rsbuild/plugin-react';
import { pluginTailwindcss } from '@rsbuild/plugin-tailwindcss';

// 加载 .env / .env.local 并以 VITE_ 前缀暴露给前端
// （Rsbuild 默认只暴露 PUBLIC_ 前缀，这里显式加上 VITE_）
const { publicVars } = loadEnv({ prefixes: ['VITE_'] });

export default defineConfig({
  plugins: [pluginReact(), pluginTailwindcss()],
  server: {
    port: 5173,
    host: '0.0.0.0',
  },
  source: {
    entry: { index: './src/main.tsx' },
    define: publicVars,
  },
  html: {
    template: './index.html',
  },
});
