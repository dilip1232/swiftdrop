import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import { resolve } from "path";
import { cpSync, mkdirSync } from "fs";

const outDir = resolve(__dirname, "dist");

// After build, copy output to each platform's web/ directory.
function copyToPlatforms() {
  return {
    name: "copy-to-platforms",
    writeBundle() {
      const platforms = ["mac", "linux", "windows"];
      for (const p of platforms) {
        const dest = resolve(__dirname, "..", p, "ui");
        mkdirSync(dest, { recursive: true });
        cpSync(outDir, dest, { recursive: true });
      }
      console.log("✓ Copied build to mac/ui, linux/ui, windows/ui");
    },
  };
}

export default defineConfig({
  plugins: [svelte(), copyToPlatforms()],
  build: {
    outDir,
    emptyOutDir: true,
  },
  server: {
    proxy: {
      "/api": "http://localhost:8421",
    },
  },
});
