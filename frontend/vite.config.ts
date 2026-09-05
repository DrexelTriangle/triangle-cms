/// <reference types="vitest/config" />
import path from "path"
import { defineConfig } from "vite"
import react from "@vitejs/plugin-react"

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
      // Yoast's transitive Node dependencies need browser polyfills in the worker.
      events: path.resolve(__dirname, "node_modules/events/events.js"),
      buffer: path.resolve(__dirname, "node_modules/buffer/index.js"),
      url: path.resolve(__dirname, "node_modules/url/url.js"),
    },
  },
  build: {
    rollupOptions: {
      output: {
        // Cache shared dependencies independently of route changes.
        manualChunks: {
          react: ["react", "react-dom", "react-router-dom"],
          ui: [
            "@radix-ui/react-avatar",
            "@radix-ui/react-dropdown-menu",
            "@radix-ui/react-popover",
            "@radix-ui/react-separator",
            "@radix-ui/react-slot",
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
