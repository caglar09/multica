/**
 * Role-Scoped Team Skill Presets & Policies.
 *
 * Ensures agents and teams in Multica inherit ONLY the skills relevant to their
 * specialized responsibilities, avoiding prompt bloat, token wastage, and unauthorized
 * tool execution while preserving the "0-Token Standby" discipline.
 */

export type TeamRoleCategory =
  | "lead"
  | "architecture"
  | "frontend"
  | "mobile"
  | "backend"
  | "qa"
  | "security"
  | "general";

export interface RoleSkillProfile {
  roleId: TeamRoleCategory;
  title: string;
  departmentRoom: "architecture" | "engineering" | "qa" | "executive";
  description: string;
  allowedSkillKeywords: string[];
  deniedSkillKeywords?: string[];
  recommendedSkills: string[];
}

export const ROLE_SKILL_PROFILES: Record<TeamRoleCategory, RoleSkillProfile> = {
  lead: {
    roleId: "lead",
    title: "Project Lead / CEO",
    departmentRoom: "executive",
    description: "High-level planning, requirement decomposition, and agent team dispatch.",
    allowedSkillKeywords: ["ultrathink", "architecture", "senior-architect", "documentation", "thinking-partner"],
    recommendedSkills: ["senior-architect", "ultrathink", "documentation"],
  },
  architecture: {
    roleId: "architecture",
    title: "Solution Architect",
    departmentRoom: "architecture",
    description: "System design, ADRs, module interfaces, seams, and cross-cutting concerns.",
    allowedSkillKeywords: ["architecture", "senior-architect", "codebase-design", "ultrathink", "documentation"],
    recommendedSkills: ["architecture", "codebase-design", "senior-architect"],
  },
  frontend: {
    roleId: "frontend",
    title: "Frontend Engineer",
    departmentRoom: "engineering",
    description: "Web interfaces, design system compliance, component architecture, state management.",
    allowedSkillKeywords: [
      "frontend-ui-engineering",
      "design-system",
      "shadcn-ui",
      "react-doctor",
      "web-design-guidelines",
    ],
    deniedSkillKeywords: ["backend", "drizzle", "prisma", "mobile", "ios", "android"],
    recommendedSkills: ["frontend-ui-engineering", "design-system", "shadcn-ui", "react-doctor"],
  },
  mobile: {
    roleId: "mobile",
    title: "Mobile Engineer",
    departmentRoom: "engineering",
    description: "Cross-platform mobile applications, Expo, React Native optimizations and animations.",
    allowedSkillKeywords: [
      "mobile",
      "react-native-best-practices",
      "react-native-animations",
      "vercel-react-native-skills",
      "controlling-mobile-devices",
    ],
    deniedSkillKeywords: ["backend", "sql", "drizzle", "prisma", "seo"],
    recommendedSkills: ["mobile", "react-native-best-practices", "react-native-animations"],
  },
  backend: {
    roleId: "backend",
    title: "Backend Engineer",
    departmentRoom: "engineering",
    description: "Server architecture, API design, database schemas, migrations, performance.",
    allowedSkillKeywords: [
      "backend",
      "drizzle-orm",
      "drizzle-migrations",
      "prisma-client-api",
      "prisma-cli",
      "debugging",
      "optimization",
    ],
    deniedSkillKeywords: ["mobile", "ios", "react-native", "design-system"],
    recommendedSkills: ["backend", "debugging", "drizzle-orm", "optimization"],
  },
  qa: {
    roleId: "qa",
    title: "QA Engineer",
    departmentRoom: "qa",
    description: "Test automation, E2E tests, regression suites, edge-case coverage.",
    allowedSkillKeywords: ["testing", "debugging", "unlazy", "codebase-design"],
    recommendedSkills: ["testing", "debugging", "unlazy"],
  },
  security: {
    roleId: "security",
    title: "Security & Code Reviewer",
    departmentRoom: "qa",
    description: "Vulnerability analysis, zero-trust policies, safe dependency audits, code reviews.",
    allowedSkillKeywords: ["code-mentor", "codebase-design", "dependency-management", "debugging", "testing"],
    recommendedSkills: ["code-mentor", "codebase-design", "testing"],
  },
  general: {
    roleId: "general",
    title: "Software Engineer",
    departmentRoom: "engineering",
    description: "General engineering tasks, refactoring, and maintenance.",
    allowedSkillKeywords: ["code-mentor", "refactoring", "debugging", "documentation"],
    recommendedSkills: ["code-mentor", "debugging", "documentation"],
  },
};

/**
 * Resolves a role identifier into a standard role profile.
 */
export function resolveRoleCategory(rawRoleOrTitle?: string | null): TeamRoleCategory {
  if (!rawRoleOrTitle) return "general";
  const r = rawRoleOrTitle.toLowerCase();
  if (r.includes("ceo") || r.includes("lead") || r.includes("manager") || r.includes("pm")) return "lead";
  if (r.includes("architect")) return "architecture";
  if (r.includes("mobile") || r.includes("ios") || r.includes("android") || r.includes("expo")) return "mobile";
  if (r.includes("front") || r.includes("ui") || r.includes("web") || r.includes("react")) return "frontend";
  if (r.includes("back") || r.includes("api") || r.includes("server") || r.includes("database")) return "backend";
  if (r.includes("qa") || r.includes("test")) return "qa";
  if (r.includes("sec") || r.includes("audit") || r.includes("review")) return "security";
  return "general";
}

/**
 * Filter scanned system skills down to only those appropriate for a specific agent role.
 */
export function filterSkillsForRole(
  rawRole: string | null | undefined,
  availableSkillNames: string[]
): { allowedSkills: string[]; deniedSkills: string[]; profile: RoleSkillProfile } {
  const category = resolveRoleCategory(rawRole);
  const profile = ROLE_SKILL_PROFILES[category];

  const allowed: string[] = [];
  const denied: string[] = [];

  for (const skill of availableSkillNames) {
    const sLower = skill.toLowerCase();

    // Check denied keywords first
    const isDenied = profile.deniedSkillKeywords?.some((kw) => sLower.includes(kw));
    if (isDenied) {
      denied.push(skill);
      continue;
    }

    // Check allowed keywords or recommended
    const isAllowed = profile.allowedSkillKeywords.some((kw) => sLower.includes(kw)) ||
      profile.recommendedSkills.includes(skill);

    if (isAllowed) {
      allowed.push(skill);
    } else {
      denied.push(skill);
    }
  }

  // If none explicitly matched, at least provide recommended ones that are available
  if (allowed.length === 0) {
    for (const rec of profile.recommendedSkills) {
      if (availableSkillNames.includes(rec)) {
        allowed.push(rec);
      }
    }
  }

  return { allowedSkills: allowed, deniedSkills: denied, profile };
}
