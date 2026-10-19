import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import packageJSON from "./package.json" with { type: "json" };
export default defineConfig({
  plugins: [react()],
  define: {
    __APP_VERSION__: JSON.stringify(packageJSON.version),
    __BUILD_TIMESTAMP__: JSON.stringify(new Date().toISOString()),
  },
  test: { environment: "jsdom", setupFiles: ["./src/test/setup.ts"] },
});
