import { defineConfig } from "vite";

export default defineConfig({
  base: "/",
  build: {
    outDir: "../internal/api/console/dist",
    emptyOutDir: true,
  },
});
