import path from "path"
import { defineConfig } from "vite"
import react from "@vitejs/plugin-react"

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
      // yoastseo pulls in node-core modules via transitive deps (htmlparser2 →
      // events, safe-buffer → buffer, its url helper). Point them at browser
      // polyfills so Vite doesn't externalize them to empty stubs that crash the
      // analysis worker at load time.
      events: path.resolve(__dirname, "node_modules/events/events.js"),
      buffer: path.resolve(__dirname, "node_modules/buffer/index.js"),
      url: path.resolve(__dirname, "node_modules/url/url.js"),
    },
  },
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      "/v1": {
        target: "https://localhost:8080",
        changeOrigin: true,
        secure: false,
      },
    },
  },
})
