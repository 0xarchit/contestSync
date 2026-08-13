import { defineConfig } from 'vite';

export default defineConfig({
  build: {
    outDir: 'static',
    emptyOutDir: false,
    minify: 'terser',
    terserOptions: {
      compress: {
        drop_console: true,
      }
    },
    lib: {
      entry: 'src/app.js',
      name: 'App',
      formats: ['iife'],
      fileName: () => 'app.bundle.js'
    },
    rollupOptions: {
      output: {
        assetFileNames: 'style.bundle.css'
      }
    }
  }
});
