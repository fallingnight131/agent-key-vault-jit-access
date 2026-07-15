import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  base: '/',
  plugins: [vue()],
  define: {
    __VUE_OPTIONS_API__: false,
    __VUE_PROD_DEVTOOLS__: false,
    __VUE_PROD_HYDRATION_MISMATCH_DETAILS__: false,
  },
  build: {
    emptyOutDir: true,
    outDir: 'dist',
    sourcemap: false,
  },
  test: {
    environment: 'jsdom',
    restoreMocks: true,
  },
})
