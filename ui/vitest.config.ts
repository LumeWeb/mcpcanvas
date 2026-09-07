import { defineConfig } from "vitest/config";

// No path aliases: the package is fully self-contained (relative imports only),
// so consumers can bundle ./src/index.ts directly with any TS-aware bundler.
export default defineConfig({
  test: {
    globals: true,
    include: ["tests/**/*.test.ts"],
  },
});
