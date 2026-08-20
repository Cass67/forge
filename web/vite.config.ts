import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The Wails runtime is served by the window itself at /wails/runtime.js, so it
// is left as a bare import rather than bundled.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "dist",
    // dist is committed so a machine without bun can still build the app.
    // Content hashes in the filenames would make every rebuild a churn of
    // ~100 added and deleted files; stable names overwrite in place instead.
    // Nothing caches these — the window serves them from the binary.
    rollupOptions: {
      external: ["/wails/runtime.js"],
      output: {
        entryFileNames: "assets/[name].js",
        chunkFileNames: "assets/[name].js",
        assetFileNames: "assets/[name].[ext]",
      },
    },
  },
});
