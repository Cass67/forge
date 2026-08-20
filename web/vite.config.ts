import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The Wails runtime is served by the window itself at /wails/runtime.js, so it
// is left as a bare import rather than bundled.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "dist",
    rollupOptions: { external: ["/wails/runtime.js"] },
  },
});
