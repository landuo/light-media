import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
export default defineConfig({
  base: './',
  plugins: [vue()],
  server: {
    proxy: {
      // Keep the browser's Host and Origin aligned with production's CSRF check.
      // Vite's string shorthand enables changeOrigin and rewrites the Host.
      '/api': { target: 'http://127.0.0.1:3120', changeOrigin: false },
    },
  },
  build: { outDir: 'dist', target: 'es2022' },
});
