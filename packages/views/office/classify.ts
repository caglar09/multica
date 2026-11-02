import type { AgentCategory, AgentCharacterType } from "./types";

export interface AgentOfficeClassification {
  category: AgentCategory;
  characterType: AgentCharacterType;
}

function words(value: string): Set<string> {
  return new Set(value.toLowerCase().replace(/[^a-z0-9]+/g, " ").trim().split(/\s+/).filter(Boolean));
}

function hasAny(source: Set<string>, values: readonly string[]): boolean {
  return values.some((value) => source.has(value));
}

function classification(category: AgentCategory): AgentOfficeClassification {
  const characterType: Record<AgentCategory, AgentCharacterType> = {
    management: "lion_lead",
    architecture: "owl_architect",
    frontend: "cat_frontend",
    mobile: "cat_mobile",
    backend: "raccoon_backend",
    qa: "panda_qa",
    security: "fox_security",
    general: "robot_general",
  };
  return { category, characterType: characterType[category] };
}

/** Classifies known role/family terms without substring guesses or UI copy. */
export function resolveAgentCategoryAndCharacter(
  family: string,
  role: string,
  isLeader = false,
): AgentOfficeClassification {
  const familyWords = words(family);
  const roleWords = words(role);
  const all = new Set([...familyWords, ...roleWords]);
  const roleText = role.toLowerCase();

  if (
    isLeader ||
    hasAny(all, ["leader", "manager", "management", "yönetici", "lider"]) ||
    roleText.includes("project manager") ||
    roleText.includes("product manager")
  ) return classification("management");

  if (hasAny(all, ["architect", "architecture", "solution", "system", "mimar"])) {
    return classification("architecture");
  }
  if (
    hasAny(all, ["mobile", "android", "ios", "flutter", "native"]) ||
    (all.has("react") && all.has("native"))
  ) {
    return classification("mobile");
  }
  if (hasAny(all, ["frontend", "front", "ui", "web", "design", "css"])) {
    return classification("frontend");
  }
  if (hasAny(all, ["backend", "back", "api", "server", "go", "sql", "database"])) {
    return classification("backend");
  }
  if (hasAny(all, ["qa", "test", "review", "quality", "doğrulama"])) {
    return classification("qa");
  }
  if (hasAny(all, ["security", "sec", "guard", "privacy", "güvenlik"])) {
    return classification("security");
  }
  return classification("general");
}
