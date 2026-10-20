import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
import buildManifest from './package.json';
const buildVersion = buildManifest.version;
export default defineConfig({
  base: './',
  plugins: [
    vue(),
    {
      name: 'frontend-build-version',
      generateBundle() {
        this.emitFile({ type: 'asset', fileName: 'version.txt', source: buildVersion });
      },
    },
  ],
  server: {
    proxy: {
      // Keep the browser's Host and Origin aligned with production's CSRF check.
      // Vite's string shorthand enables changeOrigin and rewrites the Host.
      '/api': { target: 'http://127.0.0.1:3120', changeOrigin: false },
    },
  },
  build: { outDir: 'dist', target: 'es2022' },
});
