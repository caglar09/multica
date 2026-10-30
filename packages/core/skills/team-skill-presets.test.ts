// @vitest-environment node
import { describe, it, expect } from "vitest";
import {
  resolveRoleCategory,
  filterSkillsForRole,
} from "./team-skill-presets";

describe("team-skill-presets", () => {
  it("resolves role categories accurately from various titles", () => {
    expect(resolveRoleCategory("CEO / Project Lead")).toBe("lead");
    expect(resolveRoleCategory("Solution Architect")).toBe("architecture");
    expect(resolveRoleCategory("Senior Mobile Engineer (iOS/Expo)")).toBe("mobile");
    expect(resolveRoleCategory("Frontend React Engineer")).toBe("frontend");
    expect(resolveRoleCategory("Backend Go/SQL Specialist")).toBe("backend");
    expect(resolveRoleCategory("QA Automation Tester")).toBe("qa");
    expect(resolveRoleCategory("Security Auditor & Code Reviewer")).toBe("security");
    expect(resolveRoleCategory("Unknown Role")).toBe("general");
    expect(resolveRoleCategory(null)).toBe("general");
  });

  it("filters skills specifically to the agent role, blocking denied domains", () => {
    const allAvailableSkills = [
      "frontend-ui-engineering",
      "design-system",
      "react-doctor",
      "mobile",
      "react-native-best-practices",
      "backend",
      "drizzle-orm",
      "prisma-cli",
      "testing",
      "debugging",
      "architecture",
      "senior-architect",
      "seo-fundamentals",
    ];

    // Mobile role test
    const mobileResult = filterSkillsForRole("Mobile Engineer", allAvailableSkills);
    expect(mobileResult.allowedSkills).toContain("mobile");
    expect(mobileResult.allowedSkills).toContain("react-native-best-practices");
    expect(mobileResult.allowedSkills).not.toContain("backend");
    expect(mobileResult.allowedSkills).not.toContain("drizzle-orm");
    expect(mobileResult.deniedSkills).toContain("backend");

    // Backend role test
    const backendResult = filterSkillsForRole("Backend Engineer", allAvailableSkills);
    expect(backendResult.allowedSkills).toContain("backend");
    expect(backendResult.allowedSkills).toContain("drizzle-orm");
    expect(backendResult.allowedSkills).not.toContain("mobile");
    expect(backendResult.allowedSkills).not.toContain("design-system");

    // QA role test
    const qaResult = filterSkillsForRole("QA Engineer", allAvailableSkills);
    expect(qaResult.allowedSkills).toContain("testing");
    expect(qaResult.allowedSkills).toContain("debugging");
  });

  it("falls back to recommended skills when no explicit match exists in scanned list", () => {
    const result = filterSkillsForRole("Frontend Developer", [
      "react-doctor",
      "seo-fundamentals",
    ]);
    expect(result.allowedSkills).toContain("react-doctor");
  });
});
