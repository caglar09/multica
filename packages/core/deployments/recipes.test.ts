import { describe, it, expect } from "vitest";
import {
  analyzeRepository,
  detectRecipe,
  getRecipe,
  FRAMEWORK_RECIPES,
} from "./recipes";

describe("Framework Recipes", () => {
  it("defines standard framework recipes with required build presets", () => {
    expect(FRAMEWORK_RECIPES.length).toBeGreaterThan(5);

    const nextRecipe = getRecipe("nextjs");
    expect(nextRecipe.framework).toBe("nextjs");
    expect(nextRecipe.defaultOutputDir).toBe(".next");
    expect(nextRecipe.defaultBuildCommand).toContain("build");

    const viteRecipe = getRecipe("vite");
    expect(viteRecipe.framework).toBe("vite");
    expect(viteRecipe.defaultOutputDir).toBe("dist");
  });

  it("detects Next.js from dependencies or files", () => {
    const fromDeps = detectRecipe({
      dependencies: { next: "15.0.0", react: "19.0.0" },
    });
    expect(fromDeps.framework).toBe("nextjs");

    const fromFiles = detectRecipe({
      files: ["next.config.js", "package.json"],
    });
    expect(fromFiles.framework).toBe("nextjs");
  });

  it("detects Vite from dependencies or config files", () => {
    const fromDeps = detectRecipe({
      dependencies: { vite: "6.0.0" },
    });
    expect(fromDeps.framework).toBe("vite");

    const fromFiles = detectRecipe({
      files: ["vite.config.ts", "package.json"],
    });
    expect(fromFiles.framework).toBe("vite");
  });

  it("detects Remix from dependencies or config files", () => {
    const fromDeps = detectRecipe({
      dependencies: { "@remix-run/react": "2.0.0" },
    });
    expect(fromDeps.framework).toBe("remix");
  });

  it("detects Astro from dependencies or config files", () => {
    const fromFiles = detectRecipe({
      files: ["astro.config.mjs"],
    });
    expect(fromFiles.framework).toBe("astro");
  });

  it("detects Nuxt and static HTML from repository metadata", () => {
    expect(
      detectRecipe({ dependencies: { nuxt: "3.0.0" } }).framework,
    ).toBe("nuxtjs");
    expect(detectRecipe({ files: ["index.html", "styles.css"] }).framework).toBe(
      "static",
    );
  });

  it("falls back to custom when repository metadata is missing or unknown", () => {
    const defaultRecipe = detectRecipe();
    expect(defaultRecipe.framework).toBe("custom");
    expect(detectRecipe({}).framework).toBe("custom");
    expect(analyzeRepository({}).rootDirectory).toBe("");
  });

  it("falls back to custom if unrecognized files provided", () => {
    const customRecipe = detectRecipe({
      files: ["Makefile", "main.go"],
    });
    expect(customRecipe.framework).toBe("custom");
  });

  it("returns safe metadata only and warns about unsupported Vercel workloads", () => {
    const analysis = analyzeRepository({
      files: ["apps/web/package.json", "apps/web/server.ts", "data/app.sqlite"],
      dependencies: { vite: "6.0.0", express: "5.0.0", sqlite3: "5.0.0" },
      rootDirectories: ["apps/web"],
      environmentVariables: ["DATABASE_URL", "PORT=do-not-return-this-value"],
    });

    expect(analysis.framework).toBe("vite");
    expect(analysis.rootDirectory).toBe("apps/web");
    expect(analysis.dependencyNames).toEqual(["vite", "express", "sqlite3"]);
    expect(analysis.environmentVariableNames).toEqual(["DATABASE_URL", "PORT"]);
    expect(JSON.stringify(analysis)).not.toContain("do-not-return-this-value");
    expect(analysis.warnings.map((warning) => warning.code)).toEqual(
      expect.arrayContaining([
        "persistent_process",
        "persistent_local_disk",
        "runtime_assumption",
      ]),
    );
  });
});
