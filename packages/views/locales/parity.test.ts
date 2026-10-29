// @vitest-environment node
import { readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { createI18n } from "@multica/core/i18n/react";
import { RESOURCES } from "./index";

// Runtime copy may fall back to English for untranslated messages. Keep that
// contract covered here; workflows with copy that must be localized have
// explicit locale contracts in their feature tests.

// Derive the canonical namespace list from disk so the test fails if a JSON
// file ships without a matching RESOURCES entry. Without this guard the test
// would still pass when EN and a non-English bundle skip a namespace (e.g. issues +
// agents both unregistered), since the iteration happens over RESOURCES.en
// itself — that's a tautology, not parity.
const LOCALES_DIR = dirname(fileURLToPath(import.meta.url));

function jsonNamespacesIn(locale: string): string[] {
  return readdirSync(resolve(LOCALES_DIR, locale))
    .filter((name) => name.endsWith(".json"))
    .map((name) => name.replace(/\.json$/, ""))
    .sort();
}

function flattenKeys(obj: unknown, prefix = ""): string[] {
  if (obj === null || typeof obj !== "object") return [prefix];
  const entries = Object.entries(obj);
  if (entries.length === 0) return [];
  return entries.flatMap(([k, v]) =>
    flattenKeys(v, prefix ? `${prefix}.${k}` : k),
  );
}

const en = RESOURCES.en;
const translatedLocales = Object.keys(RESOURCES).filter(
  (locale) => locale !== "en",
);

describe("locale bundle parity", () => {
  it("registers every JSON file in RESOURCES (EN)", () => {
    expect(Object.keys(en).sort()).toEqual(jsonNamespacesIn("en"));
  });

  for (const locale of translatedLocales) {
    const bundle = RESOURCES[locale as keyof typeof RESOURCES];

    it(`declares the same namespaces in EN and ${locale}`, () => {
      expect(Object.keys(bundle).sort()).toEqual(Object.keys(en).sort());
    });

    it(`registers every JSON file in RESOURCES (${locale})`, () => {
      expect(Object.keys(bundle).sort()).toEqual(jsonNamespacesIn(locale));
    });

    it(`uses English as a fallback for ${locale}`, () => {
      const i18n = createI18n(locale as keyof typeof RESOURCES, RESOURCES);
      expect(i18n.options.fallbackLng).toEqual(["en"]);
    });
  }
});

// Dead plural-key guard: a locale whose CLDR plural rules have no `one`
// category (e.g. ja/ko/zh-Hans) resolves only `_other`, so any `_one` key in
// it is dead weight i18next never renders. Left unchecked these accumulate and
// hide bugs — a missing `_other` silently falls back while the orphan `_one`
// looks like coverage. i18next resolves plurals via Intl.PluralRules, so we
// gate on the same source of truth.
describe("dead plural-key guard", () => {
  for (const locale of translatedLocales) {
    const categories = new Intl.PluralRules(locale).resolvedOptions()
      .pluralCategories;
    if (categories.includes("one")) continue;

    const bundle = RESOURCES[locale as keyof typeof RESOURCES];
    it(`${locale} ships no dead _one keys (plural categories: ${categories.join("/")})`, () => {
      const offenders = Object.keys(bundle).flatMap((ns) =>
        flattenKeys(bundle[ns])
          .filter((key) => key.endsWith("_one"))
          .map((key) => `${ns}:${key}`),
      );
      expect(offenders).toEqual([]);
    });
  }
});
