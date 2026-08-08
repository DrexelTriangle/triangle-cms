/// <reference types="vitest/config" />
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
  build: {
    rollupOptions: {
      output: {
        // Vendor code changes only when a dependency is upgraded, while app
        // code changes every deploy. Splitting them means a routine deploy
        // invalidates the app chunk and leaves React and the UI kit in the
        // browser cache, which is what the immutable caching in nginx.conf is
        // there to exploit.
        manualChunks: {
          react: ["react", "react-dom", "react-router-dom"],
          // Radix and the two icon sets: large, stable, and pulled in by nearly
          // every route, so they belong in neither the app chunk nor a route's.
          ui: [
            "@radix-ui/react-avatar",
            "@radix-ui/react-checkbox",
            "@radix-ui/react-dropdown-menu",
            "@radix-ui/react-label",
            "@radix-ui/react-popover",
            "@radix-ui/react-separator",
            "@radix-ui/react-slot",
            "@phosphor-icons/react",
            "lucide-react",
          ],
        },
      },
    },
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
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
