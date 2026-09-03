import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The Wails runtime is served by the window itself at /wails/runtime.js, so it
// is left as a bare import rather than bundled.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "dist",
    // Stable filenames rather than content hashes: nothing caches these — the
    // window serves them from the binary — and hashed names would leave stale
    // assets behind on every rebuild.
    //
    // public/.gitkeep is copied into dist on each build. dist is not committed,
    // and that one tracked file keeps the directory present on a clean clone so
    // //go:embed all:dist in web/embed.go still resolves.
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
