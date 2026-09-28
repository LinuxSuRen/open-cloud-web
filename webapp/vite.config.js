import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 构建产物直接输出到 Go 内嵌目录（internal/web/dist），
// `go build` 后打进 ocw 二进制，部署无需分发静态文件。
export default defineConfig({
  plugins: [vue()],
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/api': { target: 'http://127.0.0.1:8080', ws: true },
      '/healthz': { target: 'http://127.0.0.1:8080' },
    },
  },
})
