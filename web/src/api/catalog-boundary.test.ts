import { parse, validate } from "./boundary.js";

// Runs against the aggregate generated schemas through the actual browser
// boundary validator. Kept separate from Go's authoritative cross-field tests.
export function runCatalogBoundaryTests(): number {
  let passed = 0;
  const check = (name: string, value: unknown, accepted: boolean): void => {
    let valid = true;
    try { validate(`transport:${name}`, parse(JSON.stringify(value))); } catch { valid = false; }
    if (valid !== accepted) throw Error(`Catalog boundary mismatch: ${name}`);
    passed++;
  };
  for (const [state, value, accepted] of [
    ["known", 0, true], ["stale", 0, true], ["unknown", null, true], ["not_configured", null, true],
    ["known", null, false], ["stale", null, false], ["unknown", 0, false], ["not_configured", 0, false],
  ] as const) check("CatalogCount", { state, value }, accepted);
  const coverage = { historyFloor: null, activityFloor: null, outcomePayloadFloor: null, gaps: [] };
  check("CatalogActivity", { state: "known", lastCommittedAt: null, coverage }, true);
  check("CatalogActivity", { state: "unknown", lastCommittedAt: null, coverage: null }, true);
  check("CatalogActivity", { state: "unknown", lastCommittedAt: null, coverage }, false);
  check("CatalogActivity", { state: "stale", lastCommittedAt: null, coverage: null }, false);
  const unknown = { state: "unknown", value: null }, unsupported = { state: "not_configured", value: null };
  const entry = { projectKey: "p", boardKey: "b", projectLabel: "P", label: "B", identity: null, memberEpoch: null,
    availability: "unavailable", freshness: "never", ageSeconds: null, lastObservedAt: null, boardRevision: null,
    activity: { state: "unknown", lastCommittedAt: null, coverage: null },
    attention: { readyForReview: unknown, blocked: unsupported, needsTerry: unsupported } };
  check("WorkspaceCatalogEntry", entry, true);
  check("WorkspaceCatalogEntry", { ...entry, boardRevision: 0 }, false);
  check("WorkspaceCatalogEntry", { ...entry, freshness: "fresh" }, false);
  check("CatalogAttention", { ...entry.attention, blocked: { state: "known", value: 0 } }, false);
  return passed;
}
