import type { DeploymentRecipe } from "../types/deployment";

export const FRAMEWORK_RECIPES: readonly DeploymentRecipe[] = [
  {
    id: "nextjs",
    name: "Next.js",
    framework: "nextjs",
    description:
      "Production-grade React framework with hybrid static & server rendering",
    defaultRootDir: "",
    defaultBuildCommand: "npm run build",
    defaultOutputDir: ".next",
    defaultInstallCommand: "npm install",
    defaultDevCommand: "next dev",
  },
  {
    id: "vite",
    name: "Vite",
    framework: "vite",
    description: "Next Generation Frontend Tooling for modern web projects",
    defaultRootDir: "",
    defaultBuildCommand: "npm run build",
    defaultOutputDir: "dist",
    defaultInstallCommand: "npm install",
    defaultDevCommand: "vite",
  },
  {
    id: "remix",
    name: "Remix",
    framework: "remix",
    description:
      "Full stack web framework focused on web standards and modern UX",
    defaultRootDir: "",
    defaultBuildCommand: "npm run build",
    defaultOutputDir: "build",
    defaultInstallCommand: "npm install",
    defaultDevCommand: "remix dev",
  },
  {
    id: "astro",
    name: "Astro",
    framework: "astro",
    description: "The web framework for content-driven websites",
    defaultRootDir: "",
    defaultBuildCommand: "npm run build",
    defaultOutputDir: "dist",
    defaultInstallCommand: "npm install",
    defaultDevCommand: "astro dev",
  },
  {
    id: "nuxtjs",
    name: "Nuxt",
    framework: "nuxtjs",
    description: "Intuitive Vue framework for hybrid web applications",
    defaultRootDir: "",
    defaultBuildCommand: "npm run build",
    defaultOutputDir: ".output",
    defaultInstallCommand: "npm install",
    defaultDevCommand: "nuxi dev",
  },
  {
    id: "static",
    name: "Static HTML",
    framework: "static",
    description:
      "Pre-rendered static HTML, CSS, and client-side JavaScript assets",
    defaultRootDir: "",
    defaultBuildCommand: "",
    defaultOutputDir: "public",
    defaultInstallCommand: "",
    defaultDevCommand: "",
  },
  {
    id: "custom",
    name: "Other / Custom",
    framework: "custom",
    description: "Custom project structure and build configuration",
    defaultRootDir: "",
    defaultBuildCommand: "",
    defaultOutputDir: "",
    defaultInstallCommand: "",
    defaultDevCommand: "",
  },
] as const;

export interface RecipeDetectionHints {
  files?: readonly string[];
  dependencies?:
    | readonly string[]
    | Readonly<Record<string, unknown>>;
  rootDir?: string;
}

export interface RepositoryMetadata {
  files?: readonly string[];
  dependencies?: readonly string[] | Readonly<Record<string, unknown>>;
  rootDirectories?: readonly string[];
  environmentVariables?:
    | readonly string[]
    | Readonly<Record<string, unknown>>;
  environmentVariableNames?: readonly string[];
}

export type RepositorySuitabilityWarningCode =
  | "persistent_process"
  | "persistent_local_disk"
  | "runtime_assumption";

export interface RepositorySuitabilityWarning {
  code: RepositorySuitabilityWarningCode;
  message: string;
}

export interface RepositoryAnalysis {
  recipe: DeploymentRecipe;
  framework: string;
  rootDirectory: string;
  files: readonly string[];
  dependencyNames: readonly string[];
  environmentVariableNames: readonly string[];
  warnings: readonly RepositorySuitabilityWarning[];
  suitabilityWarnings: readonly RepositorySuitabilityWarning[];
}

function normalizePath(value: string): string {
  const normalized = value.trim().replaceAll("\\", "/");
  return normalized.replace(/^\.\//, "").replace(/\/+/g, "/");
}

function uniqueStrings(values: readonly string[]): string[] {
  return [...new Set(values.map((value) => value.trim()).filter(Boolean))];
}

function dependencyNames(
  dependencies: RecipeDetectionHints["dependencies"],
): string[] {
  if (Array.isArray(dependencies)) return uniqueStrings(dependencies);
  return dependencies ? uniqueStrings(Object.keys(dependencies)) : [];
}

function environmentVariableNames(
  metadata: RepositoryMetadata | undefined,
): string[] {
  const values = [
    ...(metadata?.environmentVariables
      ? Array.isArray(metadata.environmentVariables)
        ? metadata.environmentVariables
        : Object.keys(metadata.environmentVariables)
      : []),
    ...(metadata?.environmentVariableNames ?? []),
  ];

  return uniqueStrings(
    values
      .map((value) => value.split("=", 1)[0]?.trim() ?? "")
      .filter((value) => /^[A-Za-z_][A-Za-z0-9_]*$/.test(value)),
  );
}

function rootDirectoryFor(
  files: readonly string[],
  configuredRoots: readonly string[] | undefined,
  explicitRoot: string | undefined,
): string {
  const explicit = explicitRoot?.trim();
  if (explicit) return explicit;

  const roots = uniqueStrings((configuredRoots ?? []).map(normalizePath));
  if (roots.length === 1) return roots[0]!;

  const packageRoots = uniqueStrings(
    files
      .filter((file) => /(^|\/)package\.json$/i.test(file))
      .map((file) => file.replace(/\/package\.json$/i, ""))
      .map((root) => root || ""),
  );
  if (packageRoots.length === 1) return packageRoots[0]!;
  return roots[0] ?? "";
}

function repositorySuitabilityWarnings(
  files: readonly string[],
  dependencies: readonly string[],
  environmentVariables: readonly string[],
): RepositorySuitabilityWarning[] {
  const dependencySet = new Set(dependencies.map((dependency) => dependency.toLowerCase()));
  const hasDependency = (...names: string[]) =>
    names.some((name) => dependencySet.has(name));
  const hasFile = (pattern: RegExp) => files.some((file) => pattern.test(file));
  const warnings: RepositorySuitabilityWarning[] = [];

  if (
    hasDependency(
      "express",
      "fastify",
      "koa",
      "socket.io",
      "ws",
      "bull",
      "bullmq",
      "agenda",
      "node-cron",
      "@nestjs/core",
    ) ||
    hasFile(/(^|\/)(server|worker|daemon|queue)\.[^/]+$/i) ||
    hasFile(/(^|\/)(docker-compose|Procfile)(\.|$)/i)
  ) {
    warnings.push({
      code: "persistent_process",
      message:
        "This repository appears to require a persistent process or worker; Vercel request runtimes do not keep arbitrary processes running.",
    });
  }

  if (
    hasDependency("sqlite", "sqlite3", "better-sqlite3", "lowdb", "nedb", "level") ||
    hasFile(/(^|\/)(data|storage|uploads?)\//i) ||
    hasFile(/\.(sqlite|sqlite3|db)$/i)
  ) {
    warnings.push({
      code: "persistent_local_disk",
      message:
        "This repository appears to rely on persistent local disk; Vercel filesystem writes are not durable between executions.",
    });
  }

  if (
    environmentVariables.some((name) =>
      /^(PORT|HOST|LISTEN_ADDR|LISTEN_HOST|LISTEN_PORT|WORKER|CRON)_?/.test(name),
    ) ||
    hasFile(/(^|\/)(Dockerfile|Procfile)$/i)
  ) {
    warnings.push({
      code: "runtime_assumption",
      message:
        "The repository declares runtime assumptions that may not match Vercel functions; review the runtime and entrypoint before saving.",
    });
  }

  return warnings;
}

export function detectRecipe(hints?: RecipeDetectionHints): DeploymentRecipe {
  if (!hints) {
    return getRecipe("custom");
  }

  const files = (hints.files ?? []).map(normalizePath);
  const dependencies = dependencyNames(hints.dependencies);
  const deps = new Set(dependencies.map((dependency) => dependency.toLowerCase()));

  // 1. Next.js detection
  if (
    deps.has("next") ||
    files.some(
      (f) =>
        /(^|\/)next\.config\.[^/]+$/i.test(f),
    )
  ) {
    return getRecipe("nextjs");
  }

  // 2. Astro detection
  if (
    deps.has("astro") ||
    files.some((f) => /(^|\/)astro\.config\.[^/]+$/i.test(f))
  ) {
    return getRecipe("astro");
  }

  // 3. Remix detection
  if (
    deps.has("@remix-run/react") ||
    deps.has("@remix-run/node") ||
    files.some((f) => f.includes("remix.config."))
  ) {
    return getRecipe("remix");
  }

  // 4. Nuxt detection
  if (
    deps.has("nuxt") ||
    deps.has("nuxt3") ||
    files.some((f) => f.includes("nuxt.config."))
  ) {
    return getRecipe("nuxtjs");
  }

  // 5. Vite detection
  if (
    deps.has("vite") ||
    files.some((f) => /(^|\/)vite\.config\.[^/]+$/i.test(f))
  ) {
    return getRecipe("vite");
  }

  // Static HTML is a recipe only when no application framework matched.
  if (files.some((file) => /(^|\/)index\.html$/i.test(file))) {
    return getRecipe("static");
  }

  return getRecipe("custom");
}

export function analyzeRepository(
  metadata?: RepositoryMetadata,
): RepositoryAnalysis {
  const files = uniqueStrings((metadata?.files ?? []).map(normalizePath));
  const dependencyNamesOnly = dependencyNames(metadata?.dependencies);
  const environmentNames = environmentVariableNames(metadata);
  const recipe = detectRecipe({
    files,
    dependencies: dependencyNamesOnly,
  });
  const warnings = repositorySuitabilityWarnings(
    files,
    dependencyNamesOnly,
    environmentNames,
  );

  return {
    recipe,
    framework: recipe.framework,
    rootDirectory: rootDirectoryFor(files, metadata?.rootDirectories, undefined),
    files,
    dependencyNames: dependencyNamesOnly,
    environmentVariableNames: environmentNames,
    warnings,
    suitabilityWarnings: warnings,
  };
}

export function getRecipe(frameworkIdOrFramework: string): DeploymentRecipe {
  const match = FRAMEWORK_RECIPES.find(
    (r) =>
      r.id === frameworkIdOrFramework || r.framework === frameworkIdOrFramework,
  );
  return (
    match ?? {
      id: frameworkIdOrFramework,
      name: frameworkIdOrFramework,
      framework: frameworkIdOrFramework,
      description: "Custom configuration",
      defaultRootDir: "",
      defaultBuildCommand: "",
      defaultOutputDir: "",
      defaultInstallCommand: "",
      defaultDevCommand: "",
    }
  );
}
