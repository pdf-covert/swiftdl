import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// base './':fnOS 网关把页面挂在 /app/swiftdl/ 下,相对引用随挂随走。
// dev 时 BASE 解析为 '/',/api 代理到本地 Go 后端(TCP 模式,无 -sock 即监听 8642)。
export default defineConfig({
  base: "./",
  plugins: [react()],
  build: { outDir: "www", emptyOutDir: true, chunkSizeWarningLimit: 900 },
  server: {
    proxy: {
      "/api": "http://127.0.0.1:8642",
    },
  },
});
