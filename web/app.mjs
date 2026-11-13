import { newOperationId } from "./operation-id.mjs";
import {
  ApiError,
  createBrowserApi,
  DEFAULT_APPROVAL_COMMENT,
  MAX_ATTACHMENT_BYTES,
  MAX_BULK_APPROVAL_COMMENT_LENGTH,
  MAX_LANE_APPROVAL_CARDS,
  openBrowserSession,
  validPublishing,
} from "./api.mjs";
import { createBoardRefresh, waitForRetry, watchUpdates } from "./board-updates.mjs";
import { appendCardProse } from "./card-prose.mjs";

/** @typedef {{id: string, label: string}} Labeled */
/** @typedef {Labeled & {revision: number, created_at: string, kind: "human"|"bot", color: string}} User */
/** @typedef {Partial<Record<User["kind"], {label?: string, accent?: string}>>} ActorPresentation */
/** @typedef {import("./api.mjs").RelationshipKind} RelationshipKind */
/** @typedef {{id: string, revision: number, board_id: string, source_card_id: string, target_card_id: string,
 * kind: RelationshipKind}} Relationship */
/** @typedef {{type: HTMLSelectElement, savedKind: RelationshipKind, expectedRevision: number}} RelationshipEditor */
/** @typedef {{resource_type: string, resource_id: string, operation: string, field: string|null,
 * actor_id: string, board_id: string, at: string, revision: number, before: string|null, after: string|null}} Change */
/** @typedef {import("./api.mjs").Publishing} Publishing */
/** @typedef {{id: string, name: string, revision: number, policy_id: string, default_owner_id: string, user_css?: string,
 * publishing?: unknown, actor_presentation?: ActorPresentation}} Board */
/** @typedef {{actor: string, create: Set<string>, move: Map<string, Set<string>>,
 * canClose: boolean, canManageRelationships: boolean}} Workflow */
/** @typedef {{blocked: number, needs_input: number, ready_for_review: number}} Attention */
/** @typedef {{id: string, name: string|null, revision: number|null, attention: Attention|null,
 * last_card_edit_at: string|null, error_code: string|null,
 * availability: import("./api.mjs").BoardAvailability["availability"]}} BoardSummary */
/** @typedef {import("./board-updates.mjs").BoardWatch} BoardWatch */
/** @typedef {Labeled & {revision: number, key: string, retired: boolean, position: number, role: string,
 * approval_enabled: boolean, reorder_enabled: boolean, bulk_approval_comment?: string, notes?: string}} Lane */
/** @typedef {{id: string, text: string, created_at: string, author_id: string, board_id: string,
 * card_id: string, ticket: number}} CommentListEntry */
/** @typedef {{id: string, card_id: string, original_name: string, created_at: string, added_by: string, size: number, board_id: string, active: boolean, previewable: boolean, revision: number, sha2_256: string}} Attachment */
/** @typedef {{id: string, title: string, description: string, board_id: string, lane_id: string,
 * owner_id: string, priority_id: string, ticket: number, revision: number, comment_count: number, attachment_count: number,
 * archived: boolean, completed_at: string|null, cover_attachment_id: string|null, parent_id: string|null, child_ids: string[]}} Card */
/** @typedef {Partial<Pick<Card, "title"|"description"|"lane_id"|"owner_id"|"priority_id">>} PendingFields */
/** @typedef {{kind: "patch", card: Card, fields: PendingFields}
 * | {kind: "create", card: Pick<Card, "id"|"board_id"|"title"|"lane_id">}} PendingCard */
/** @typedef {{card: Card, opener: HTMLButtonElement, payload: string, boardTag: string}} CardDrag */
/** @typedef {{ticket: string, kind: RelationshipKind}} RelationshipSeedDraft */
/** @typedef {RelationshipSeedDraft & {row: RelationshipSeedDraft}} SubmittedRelationshipSeed */
/** @typedef {{rows: RelationshipSeedDraft[], discard?: (submitted: SubmittedRelationshipSeed[]) => void}} RelationshipSeedState */
/** @typedef {{data: object, etag: string|null}} Reply */
/** @typedef {() => Promise<void|boolean>} InspectionRead */
/** @typedef {{scope: string, label: string, require: (read: InspectionRead) => void,
 * inspect: () => void, sync: () => void, recovery: HTMLElement}} Inspection */
/** @typedef {keyof CardText} TextField */
/** @typedef {Pick<Card, "title"|"description">} CardText */
/** @typedef {{scope: string, revision: number, fields: Partial<CardText>, editor: CardText,
 * outcome: "pending"|"confirmed"|"unknown"|"refused"}} CardTextSubmission */
/** @typedef {Partial<Record<TextField, CardTextSubmission>>} TextReversions */
/** @typedef {{title?: string, description?: string, lane?: string, owner?: string, priority?: string,
 * comment?: string, textReversions?: TextReversions}} CardDraft */
/** @typedef {Pick<CardDraft, "title"|"description"|"lane"|"comment">} TextDraft */
/** @typedef {{kind: "comment"|"approve-card"|"add-link", id: string}
 * | {kind: "create-card", id: string, fingerprint: string}} SubmissionIdentity */
/** @typedef {SubmissionIdentity & {submittedAt: number}} SavedSubmission */
/** @typedef {{name: string, size: number, digest: string}} UploadFingerprint */
/** @typedef {UploadFingerprint & {id: string, attempted: boolean}} PendingUpload */
/** @typedef {{name: string, outcome: string, id?: string}} UploadResult */
/** @typedef {{boardId: string, running: boolean, needsInspection: boolean, processed: number,
 * results: UploadResult[], message: string}} UploadBatch */
/** @typedef {{id: string, board_revision: number, size: number, sha2_256: string, attachment_ids: string[]}} CapturedBackup */
/** @typedef {{id: string, busy: boolean}} BackupOperation */
/** @typedef {BackupOperation & {backupId: string, boardRevision: number}} BackupPublication */
/** @typedef {{backup?: CapturedBackup, capture?: BackupOperation,
 * publication?: BackupPublication, downloading?: boolean,
 * captureMessage?: string, publicationMessage?: string, downloadMessage?: string}} BackupState */
/** @typedef {{query: string, content: boolean, order: "triage"|"ticket", lane: string, limit: string}} SearchFields */
/** @typedef {{open: boolean, fields: SearchFields, submitted?: SearchFields}} BoardSearch */
/** @typedef {{card?: {id: string, ticket: string|null}, filter?: string, search?: BoardSearch,
 * scroll?: {left: number, lanes: Record<string, number>}}} BoardView */
/** @typedef {Awaited<ReturnType<typeof readBoardContents>>} BoardContents */
/** @typedef {{board: Pick<Board, "id"|"name"|"revision"|"user_css"|"actor_presentation">,
 * lanes: Lane[], cards: Card[], priorities: Labeled[]}} BoardDisplay */
/** @typedef {Awaited<ReturnType<typeof api.getBoardAvailability>>} BoardAvailability */
/** @typedef {import("./api.mjs").Service} Service */
/** @typedef {import("./api.mjs").Build} Build */
/** @typedef {{prior: Service, priorEtag: string, activation?: {id: string, buildId: string}, acknowledged: boolean, refusal?: string}} LifecycleOperation */

const CARD_DRAG_TYPE = "application/x-localswim-card";
const CATALOG_RETRY_MS = 250;
const CATALOG_REVALIDATE_MS = 15_000;
const CATALOG_READ_TIMEOUT_MS = 15_000;
const status = /** @type {HTMLParagraphElement|null} */ (document.getElementById("status"));
const identity = document.getElementById("identity");
const navigation = document.getElementById("boards");
const main = document.querySelector("main");
const details = document.getElementById("details");
const refresh = document.getElementById("refresh");
if (!status || !identity || !navigation || !main || !details || !refresh) {
  throw new Error("Missing page structure.");
}

let capability = "";
/** @type {Promise<string>|undefined} */
let connecting;
const api = createBrowserApi({ getCapability: () => capability });
const pageTitle = document.title;
let activeView = 0;
let activeCard = 0;
let writing = false;
/** @type {PendingCard|undefined} */
let pendingCard;
/** @type {{id: string, paint: () => void}|undefined} */
let activePendingBoard;
/** @type {AbortController|undefined} */
let activeBoardLoad;
/** @type {ReturnType<typeof createBoardRefresh>|undefined} */
let activeBoardRefresh;
/** @type {LifecycleOperation|undefined} */
let pendingLifecycle;
/** @type {(() => Promise<void>)|undefined} */
let refreshActiveService;
/** @type {Inspection|undefined} */
let boardInspection;
/** @type {Inspection|undefined} */
let cardInspection;
/** @type {(() => void)|undefined} */
let retrySelectedCard;
/** @type {(() => void)|undefined} */
let retainCardDraft;
/** @type {(() => void)|undefined} */
let retainBoardDraft;
/** @type {(() => void)|undefined} */
let retainNewCardDraft;
/** @type {(() => void)|undefined} */
let retainBoardView;
/** @type {Map<string, CardDraft>} */
const cardDrafts = new Map();
/** @type {CardTextSubmission|undefined} */
let submittedCardText;
/** @type {Map<string, UploadBatch>} */
const attachmentUploads = new Map();
/** @type {Map<string, string[]>} */
const boardDrafts = new Map();
/** @type {{boardId: string, title: HTMLInputElement, description: HTMLTextAreaElement}|undefined} */
let activeNewCardText;
/** @type {Map<string, File[]>} */
const newCardFiles = new Map();
/** @type {{boardId: string, paint: () => void, sync: () => void}|undefined} */
let activeNewCardFiles;
/** @type {Map<string, RelationshipSeedState>} */
const relationshipSeedDrafts = new Map();
/** @type {Map<string, BoardView>} */
const boardViews = new Map();
const CACHE_BUDGET_KEY = "localswim:recent-board-budget";
const DEFAULT_CACHE_BUDGET = 4 * 1024 * 1024;
const MAX_CACHE_BUDGET = 32 * 1024 * 1024;
let recentBoardBudget = DEFAULT_CACHE_BUDGET;
let recentBoardBytes = 0;
/** @type {Map<string, Uint8Array>} */
const recentBoards = new Map();
try {
  const stored = window.sessionStorage.getItem(CACHE_BUDGET_KEY);
  if (stored !== null && validCacheBudget(stored)) recentBoardBudget = Number(stored);
} catch {
  // The in-memory budget remains usable when storage is unavailable.
}
/** @type {Map<string, boolean>} */
const publishingDrafts = new Map();
/** @type {Map<string, Promise<void>>} */
const preferenceWrites = new Map();
/** @typedef {Awaited<ReturnType<typeof api.getWorkspace>>} WorkspaceReply */
/** @typedef {{id: string, action: "pin"|"unpin"} | {id: string, action: "before"|"after", anchor: string}
 * | {action: "remove-unavailable"}} PinEdit */
/** @typedef {{disabled: boolean, change: (edit: PinEdit) => void, beginDrag: () => boolean,
 * endDrag: (edit?: PinEdit) => void}} PinControls */
/** @type {PinEdit|undefined} */
let pendingPinEdit;
/** @type {Promise<void>|undefined} */
let pinWrite;
let pinMessage = "";
/** @type {Map<string, BackupState>} */
const boardBackups = new Map();
/** @type {{boardId: string, panel: HTMLElement, paint: () => void}|undefined} */
let activeBackupControls;
/** @type {Map<string, {read: InspectionRead, isCurrent: () => boolean}>} */
const pendingInspections = new Map();
/** @type {{scope: string, control: HTMLTextAreaElement}|undefined} */
let activeComment;
/** @type {{boardId: string, id: string, close: () => void,
 * refreshWorkflow: (canApply?: () => boolean, automatic?: boolean) => Promise<void>,
 * refreshPriority: (requested: string, effective: Labeled|undefined, replaced: boolean) => Promise<void>,
 * settleText: (discarded?: Partial<CardText>) => void,
 * showPending: () => void, pasteAttachments: (event: ClipboardEvent) => void,
 * showAttachmentUploads: (changed?: number) => void}|undefined} */
let activeDrawer;
/** @type {(() => void)|undefined} */
let releaseCardMedia;
/** @type {(() => void)|undefined} */
let releaseBoardMedia;
const boardStyle = new CSSStyleSheet();
document.adoptedStyleSheets = [...document.adoptedStyleSheets, boardStyle];

/** @param {string} value */
function validCacheBudget(value) {
  return /^(0|[1-9][0-9]*)$/.test(value) && Number(value) <= MAX_CACHE_BUDGET;
}

/** @param {string} boardId */
function forgetRecentBoard(boardId) {
  const bytes = recentBoards.get(boardId);
  if (!bytes) return;
  recentBoards.delete(boardId);
  recentBoardBytes -= bytes.byteLength;
}

function trimRecentBoards() {
  for (const id of recentBoards.keys()) {
    if (recentBoardBytes <= recentBoardBudget) break;
    forgetRecentBoard(id);
  }
}

/** @param {BoardDisplay} display */
function retainRecentBoard(display) {
  const { id } = display.board;
  forgetRecentBoard(id);
  if (!recentBoardBudget) return;
  const serialized = JSON.stringify(display);
  if (serialized.length > recentBoardBudget) return;
  const bytes = new TextEncoder().encode(serialized);
  if (bytes.byteLength > recentBoardBudget) return;
  recentBoards.set(id, bytes);
  recentBoardBytes += bytes.byteLength;
  trimRecentBoards();
}

/** @param {string} boardId @returns {BoardDisplay|undefined} */
function readRecentBoard(boardId) {
  const bytes = recentBoards.get(boardId);
  if (!bytes) return undefined;
  recentBoards.delete(boardId);
  recentBoards.set(boardId, bytes);
  // The bytes come only from retainRecentBoard's serialization of BoardDisplay.
  /** @type {unknown} */
  const display = JSON.parse(new TextDecoder().decode(bytes));
  return /** @type {BoardDisplay} */ (display);
}

function renderRecentBoardSettings() {
  const panel = element("details");
  panel.append(element("summary", "Recent board cache"));
  const bytes = element("input");
  bytes.type = "number";
  bytes.min = "0";
  bytes.max = String(MAX_CACHE_BUDGET);
  bytes.step = "1";
  bytes.required = true;
  bytes.value = String(recentBoardBudget);
  const note = element("p", "Set the budget to 0 to turn off the cache in this tab.");
  const result = element("p");
  result.setAttribute("role", "status");
  panel.append(
    form("Save cache budget", [field("Cache budget in bytes", bytes), note], () => {
      if (!validCacheBudget(bytes.value)) return;
      recentBoardBudget = Number(bytes.value);
      trimRecentBoards();
      try {
        window.sessionStorage.setItem(CACHE_BUDGET_KEY, bytes.value);
        result.textContent = "Cache budget saved.";
      } catch {
        result.textContent = "Cache budget applied until this page reloads.";
      }
    }),
    result,
  );
  return panel;
}

/** @param {BoardContents} contents @returns {BoardDisplay} */
function boardDisplay({ board, lanes, cards, priorities }) {
  const { id, name, revision, user_css, actor_presentation } = board.data;
  return { board: { id, name, revision, user_css, actor_presentation }, lanes, cards, priorities };
}

const draftKey = "localswim:tab-drafts";
const submissionKey = "localswim:tab-submissions";
const pendingUploadKey = "localswim:tab-uploads";
const maxDraftUnits = 512 * 1024;
const maxSubmissionUnits = 32 * 1024;
const maxUploadUnits = 128 * 1024;
const maxDraftFields = { title: 4096, description: 128 * 1024, comment: 128 * 1024, lane: 36 };
const maxTabScopes = 96;
const submissionLifetime = 24 * 60 * 60 * 1000;
const resourceUUID = "[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}";
const draftResourceId = new RegExp("^" + resourceUUID + "$");
const draftOperationId = new RegExp("^" + resourceUUID.replace("-4", "-7") + "$");
const boardDraftScope = new RegExp("^board:" + resourceUUID + "$");
const cardDraftScope = new RegExp("^card:" + resourceUUID + ":" + resourceUUID + "$");
const submissionNames = {
  comment: "comment",
  "create-card": "new card",
  "approve-card": "card approval",
  "add-link": "new link",
};

/** @param {string} boardId */
function newCardScope(boardId) {
  return "board:" + boardId;
}

/** @template T @param {string} key @param {number} budget
 * @param {(scope: string, value: unknown) => T|undefined} validate */
function loadTabEntries(key, budget, validate) {
  /** @type {Map<string, T>} */
  const entries = new Map();
  try {
    const raw = window.sessionStorage.getItem(key);
    if (raw === null || raw.length > budget) return entries;
    /** @type {unknown} */
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed) || parsed.length > maxTabScopes) return entries;
    for (const entry of /** @type {unknown[]} */ (parsed)) {
      if (!Array.isArray(entry) || entry.length !== 2) continue;
      const [scope, saved] = /** @type {unknown[]} */ (entry);
      if (typeof scope !== "string" || !(boardDraftScope.test(scope) || cardDraftScope.test(scope)))
        continue;
      const value = validate(scope, saved);
      if (value !== undefined) entries.set(scope, value);
    }
  } catch {
    // The current page retains its working state if tab storage is inaccessible.
  }
  return entries;
}

/** @template T @param {string} key @param {Map<string, T>} entries @param {number} budget */
function persistTabEntries(key, entries, budget) {
  while (entries.size > maxTabScopes)
    entries.delete(/** @type {string} */ (entries.keys().next().value));
  let serialized = JSON.stringify([...entries]);
  while (serialized.length > budget && entries.size) {
    entries.delete(/** @type {string} */ (entries.keys().next().value));
    serialized = JSON.stringify([...entries]);
  }
  try {
    window.sessionStorage.setItem(key, serialized);
  } catch {
    // A denied or exhausted storage area must not interrupt an edit or readback.
  }
}

/** @param {string} scope @param {unknown} value @returns {TextDraft|undefined} */
function boundedTextDraft(scope, value) {
  if (!value || typeof value !== "object") return undefined;
  const source = /** @type {Record<string, unknown>} */ (value);
  /** @type {TextDraft} */
  const draft = {};
  for (const field of /** @type {(keyof TextDraft)[]} */ (Object.keys(maxDraftFields))) {
    const text = source[field];
    if (
      typeof text !== "string" ||
      text.length > maxDraftFields[field] ||
      (field === "comment" && !cardDraftScope.test(scope)) ||
      (field === "lane" && (!boardDraftScope.test(scope) || !draftResourceId.test(text)))
    )
      continue;
    draft[field] = text;
  }
  return Object.keys(draft).length ? draft : undefined;
}

/** @param {string} scope @param {unknown} value @returns {SavedSubmission|undefined} */
function validSavedSubmission(scope, value) {
  if (!value || typeof value !== "object") return undefined;
  const entry = /** @type {SavedSubmission} */ (value);
  const { kind, id, submittedAt } = entry;
  const now = Date.now();
  if (
    typeof kind !== "string" ||
    !Object.hasOwn(submissionNames, kind) ||
    typeof id !== "string" ||
    !Number.isSafeInteger(submittedAt) ||
    submittedAt > now ||
    now - submittedAt >= submissionLifetime ||
    (kind === "create-card") !== boardDraftScope.test(scope)
  )
    return undefined;
  if (!(kind === "approve-card" ? draftOperationId : draftResourceId).test(id)) return undefined;
  if (kind === "create-card") {
    const { fingerprint } = entry;
    if (typeof fingerprint !== "string" || !/^[0-9a-f]{64}$/.test(fingerprint)) return undefined;
    return { kind, id, submittedAt, fingerprint };
  }
  return { kind, id, submittedAt };
}

const textDrafts = loadTabEntries(draftKey, maxDraftUnits, boundedTextDraft);
const savedSubmissions = loadTabEntries(submissionKey, maxSubmissionUnits, validSavedSubmission);
/** @type {WeakMap<SavedSubmission, InspectionRead>} */
const liveSubmissionReads = new WeakMap();

/** @returns {Map<string, PendingUpload[]>|undefined} */
function loadPendingUploads() {
  try {
    const raw = window.sessionStorage.getItem(pendingUploadKey) ?? "[]";
    if (raw.length > maxUploadUnits) return undefined;
    /** @type {unknown} */
    const entries = JSON.parse(raw);
    if (!Array.isArray(entries) || entries.length > maxTabScopes) return undefined;
    /** @type {Map<string, PendingUpload[]>} */
    const batches = new Map();
    for (const entry of /** @type {unknown[]} */ (entries)) {
      if (!Array.isArray(entry) || entry.length !== 2) return undefined;
      const [scope, files] = /** @type {unknown[]} */ (entry);
      if (
        typeof scope !== "string" ||
        !cardDraftScope.test(scope) ||
        batches.has(scope) ||
        !Array.isArray(files) ||
        !files.length
      )
        return undefined;
      const ids = new Set();
      /** @type {PendingUpload[]} */
      const uploads = [];
      for (const [index, file] of /** @type {unknown[]} */ (files).entries()) {
        if (!file || typeof file !== "object" || Object.keys(file).length !== 5) return undefined;
        const { id, name, digest, size, attempted } = /** @type {Record<string, unknown>} */ (file);
        if (
          typeof id !== "string" ||
          !draftResourceId.test(id) ||
          ids.has(id) ||
          typeof name !== "string" ||
          typeof digest !== "string" ||
          !/^[0-9a-f]{64}$/.test(digest) ||
          typeof size !== "number" ||
          !Number.isSafeInteger(size) ||
          size < 0 ||
          size > MAX_ATTACHMENT_BYTES ||
          typeof attempted !== "boolean" ||
          (attempted && index !== 0)
        )
          return undefined;
        ids.add(id);
        uploads.push({ id, name, digest, size, attempted });
      }
      batches.set(scope, uploads);
    }
    return batches;
  } catch {
    return undefined;
  }
}

const pendingUploads = loadPendingUploads();

/** @param {string} scope @param {PendingUpload[]} files */
function savePendingUploads(scope, files) {
  if (!pendingUploads) throw new ApiError("attachment_upload_storage");
  const next = new Map(pendingUploads);
  if (files.length) next.set(scope, files);
  else next.delete(scope);
  const serialized = JSON.stringify([...next]);
  if (next.size > maxTabScopes || serialized.length > maxUploadUnits) {
    throw new ApiError("attachment_upload_storage");
  }
  try {
    window.sessionStorage.setItem(pendingUploadKey, serialized);
  } catch {
    throw new ApiError("attachment_upload_storage");
  }
  if (files.length) pendingUploads.set(scope, files);
  else pendingUploads.delete(scope);
}

/** @param {string} scope */
function unconfirmedUpload(scope) {
  const first = pendingUploads?.get(scope)?.[0];
  return first?.attempted ? first : undefined;
}

/** @param {string} scope @param {TextDraft} draft */
function storeTextDraft(scope, draft) {
  const next = boundedTextDraft(scope, draft);
  if (JSON.stringify(textDrafts.get(scope)) === JSON.stringify(next)) return;
  textDrafts.delete(scope);
  if (next) textDrafts.set(scope, next);
  persistTabEntries(draftKey, textDrafts, maxDraftUnits);
}

/** @param {string} scope @param {CardDraft} draft */
function storeCardDraft(scope, draft) {
  if (draft.textReversions && !Object.keys(draft.textReversions).length)
    delete draft.textReversions;
  if (Object.keys(draft).length) cardDrafts.set(scope, draft);
  else cardDrafts.delete(scope);
  const text = { ...draft };
  if (draft.textReversions?.title) delete text.title;
  if (draft.textReversions?.description) delete text.description;
  storeTextDraft(scope, text);
}

/** @param {string} scope @param {Card} [saved] */
function reconcileTextReversions(scope, saved) {
  const draft = cardDrafts.get(scope);
  const reversions = draft?.textReversions;
  /** @type {Partial<Record<TextField, true>>} */
  const preserve = {};
  /** @type {Partial<CardText>} */
  const discarded = {};
  if (!draft || !reversions) return { preserve, discarded };
  for (const field of /** @type {const} */ (["title", "description"])) {
    const submission = reversions[field];
    if (!submission) continue;
    const { outcome } = submission;
    if (
      outcome === "pending" ||
      (outcome !== "refused" && (!saved || saved.revision <= submission.revision))
    ) {
      preserve[field] = true;
      continue;
    }
    delete reversions[field];
    // An advancing read can help compare an uncertain write after HTTP settles.
    // While HTTP is pending the same text could come from a different writer.
    const { title, description } = submission.fields;
    if (
      outcome === "confirmed" ||
      (outcome === "unknown" &&
        saved &&
        (title === undefined || title === saved.title) &&
        (description === undefined || description === saved.description))
    ) {
      preserve[field] = true;
    } else {
      discarded[field] = draft[field];
      delete draft[field];
    }
  }
  storeCardDraft(scope, draft);
  return { preserve, discarded };
}

/** @param {string} scope */
function syncInspection(scope) {
  if (cardInspection?.scope === scope) cardInspection.sync();
  if (boardInspection?.scope === scope) {
    boardInspection.sync();
    activeNewCardFiles?.sync();
  }
}

/** @param {string} scope @param {SavedSubmission} submission */
function clearSavedSubmission(scope, submission) {
  if (savedSubmissions.get(scope) !== submission) return;
  savedSubmissions.delete(scope);
  persistTabEntries(submissionKey, savedSubmissions, maxSubmissionUnits);
}

/** @param {string} scope */
function savedSubmission(scope) {
  const entry = savedSubmissions.get(scope);
  if (entry && validSavedSubmission(scope, entry)) return entry;
  if (entry) clearSavedSubmission(scope, entry);
  return undefined;
}

/** @param {string} scope */
function hasUnresolvedInspection(scope) {
  return (
    pendingInspections.has(scope) ||
    (!attachmentUploads.get(scope)?.running &&
      (savedSubmission(scope) !== undefined || Boolean(unconfirmedUpload(scope))))
  );
}

/** @param {string} scope @param {SubmissionIdentity} identity @param {InspectionRead} [read] */
function recordSubmission(scope, identity, read) {
  const submission = { ...identity, submittedAt: Date.now() };
  savedSubmissions.delete(scope);
  savedSubmissions.set(scope, submission);
  if (read) liveSubmissionReads.set(submission, read);
  persistTabEntries(submissionKey, savedSubmissions, maxSubmissionUnits);
  syncInspection(scope);
  return submission;
}

/** @param {string} boardId */
function viewStorageKey(boardId) {
  return "localswim:board-view:" + boardId;
}

/** @param {string} boardId @returns {BoardView} */
function boardViewState(boardId) {
  const existing = boardViews.get(boardId);
  if (existing) return existing;
  /** @type {BoardView} */
  let state = {};
  try {
    const stored = window.sessionStorage.getItem(viewStorageKey(boardId));
    /** @type {unknown} */
    const parsed = stored === null ? null : JSON.parse(stored);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      // Each consumer validates the saved field it reads before using it.
      state = /** @type {BoardView} */ (parsed);
    }
  } catch {
    // Keep the in-page view available if tab storage is inaccessible.
  }
  boardViews.set(boardId, state);
  return state;
}

/** @param {string} boardId */
function persistBoardView(boardId) {
  try {
    window.sessionStorage.setItem(viewStorageKey(boardId), JSON.stringify(boardViewState(boardId)));
  } catch {
    // The current tab can still switch boards using its in-memory state.
  }
}

/** @param {string} boardId @param {string} [id] */
function rememberCard(boardId, id) {
  const state = boardViewState(boardId);
  if (id === undefined) delete state.card;
  else state.card = { id, ticket: new URLSearchParams(location.search).get("ticket") };
  persistBoardView(boardId);
}

/** @param {string} boardId @param {string|null} ticket @returns {string|undefined} */
function rememberedCard(boardId, ticket) {
  const state = boardViewState(boardId);
  const selected = state.card;
  if (
    !selected ||
    typeof selected.id !== "string" ||
    !/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(selected.id)
  ) {
    if (Object.hasOwn(state, "card")) rememberCard(boardId);
    return undefined;
  }
  return ticket === null || selected.ticket === ticket ? selected.id : undefined;
}

/** @param {string} phase @param {string} message */
function announce(phase, message) {
  if (!status) return;
  status.dataset.phase = phase;
  status.textContent = message;
}

/** @param {Element} control */
function syncLaneWriteControl(control) {
  if (writing) control.setAttribute("aria-disabled", "true");
  else control.removeAttribute("aria-disabled");
}

function syncLaneWriteControls() {
  main?.querySelectorAll("button[data-lane-write]").forEach(syncLaneWriteControl);
}

function startWriting() {
  writing = true;
  syncLaneWriteControls();
  activeNewCardFiles?.sync();
}

function finishWriting() {
  writing = false;
  syncLaneWriteControls();
  activeNewCardFiles?.sync();
  activeBoardRefresh?.resume();
}

/** @param {PendingCard} projection @param {boolean} show */
function showPendingCard(projection, show) {
  if (!show && pendingCard !== projection) return;
  pendingCard = show ? projection : undefined;
  const { board_id, id } = projection.card;
  if (activePendingBoard?.id === board_id) activePendingBoard.paint();
  if (activeDrawer?.boardId === board_id && activeDrawer.id === id) activeDrawer.showPending();
}

/** @param {Card} saved @returns {PendingFields|undefined} */
function pendingFields(saved) {
  if (pendingCard?.kind !== "patch") return undefined;
  const { card, fields } = pendingCard;
  return card.id === saved.id &&
    card.board_id === saved.board_id &&
    card.revision === saved.revision &&
    card.lane_id === saved.lane_id &&
    card.archived === saved.archived
    ? fields
    : undefined;
}

/** @param {PendingCard} projection @param {number} originalView */
async function refreshReplacementBoard(projection, originalView) {
  if (activeView !== originalView && activePendingBoard?.id === projection.card.board_id) {
    await activeBoardRefresh?.request();
  }
}

/** @param {Publishing} state */
function publishingStatus(state) {
  const parts = [state.enabled ? "Automatic publishing is on." : "Automatic publishing is off."];
  if (state.error_code !== null) {
    switch (state.error_code) {
      case "git_push_failed":
        parts.push("Git push failed.");
        break;
      case "repository_busy":
        parts.push("The repository is busy.");
        break;
      default:
        parts.push("Publishing failed.");
    }
  }
  if (state.pending)
    parts.push(
      state.enabled ? "Publication pending." : "Publication pending while publishing is off.",
    );
  if (state.last_success_at !== null)
    parts.push("Last successful publish at " + state.last_success_at + ".");
  return parts.join(" ");
}

/** @param {string} key @param {Promise<void>} write */
function trackPreferenceWrite(key, write) {
  preferenceWrites.set(key, write);
  void write.finally(() => {
    preferenceWrites.delete(key);
  });
}

/** @param {string} key @param {(saving: boolean) => void} update */
function watchPreferenceWrite(key, update) {
  const write = preferenceWrites.get(key);
  if (!write) return;
  update(true);
  void write.finally(() => {
    update(false);
  });
}

/** @param {"board"|"workspace"} scope @param {string} key
 * @param {{data: Publishing, etag: string}|undefined} initial
 * @param {() => Promise<{data: Publishing, etag: string}>} read
 * @param {(enabled: boolean, tag: string) => Promise<unknown>} save
 * @param {() => Promise<void>} afterSave @param {Inspection} inspection */
function renderPublishing(scope, key, initial, read, save, afterSave, inspection) {
  const panel = element("section");
  const enabled = element("input");
  enabled.type = "checkbox";
  enabled.checked = publishingDrafts.get(key) ?? initial?.data.enabled ?? false;
  const current = element("p");
  current.id = scope + "-publishing-status";
  current.setAttribute("aria-live", "polite");
  current.textContent = initial
    ? publishingStatus(initial.data)
    : "Publishing status is unavailable.";
  let saved = initial;
  /** @param {{data: Publishing, etag: string}} reply */
  function show(reply) {
    if (!panel.isConnected) return;
    if (!publishingDrafts.has(key) && (!saved || enabled.checked === saved.data.enabled)) {
      enabled.checked = reply.data.enabled;
    }
    saved = reply;
    retain();
    current.textContent = publishingStatus(reply.data);
    delete settings.dataset.unavailable;
    inspection.sync();
  }
  function retain() {
    if (!saved) return;
    if (enabled.checked === saved.data.enabled) publishingDrafts.delete(key);
    else publishingDrafts.set(key, enabled.checked);
  }
  enabled.addEventListener("change", () => {
    if (saved) retain();
    else publishingDrafts.set(key, enabled.checked);
  });
  const option = field("Enable " + scope + " publishing", enabled);
  option.className = "check-option";
  /** @param {boolean} saving */
  const disableWhileSaving = (saving) => {
    enabled.disabled = saving;
  };
  watchPreferenceWrite(key, disableWhileSaving);
  const settings = form("Save " + scope + " publishing", [option], (submit) => {
    if (!saved) return;
    const requested = enabled.checked;
    const expected = saved.etag;
    trackPreferenceWrite(
      key,
      writeResource(
        submit,
        () => save(requested, expected),
        async () => {
          await afterSave();
          const reply = await read();
          show(reply);
          return reply.data.enabled === requested;
        },
        "Publishing settings saved.",
        inspection,
      ),
    );
    watchPreferenceWrite(key, disableWhileSaving);
  });
  if (!saved) {
    settings.dataset.unavailable = "true";
    const submit = /** @type {HTMLButtonElement} */ (settings.querySelector("button"));
    submit.disabled = true;
  }
  const refreshStatus = button("Refresh " + scope + " publishing status", () => {
    if (writing) return;
    if (pendingInspections.has(inspection.scope)) {
      inspection.inspect();
      return;
    }
    startWriting();
    refreshStatus.disabled = true;
    void readWithSession(read)
      .then(show)
      .catch(() => {
        if (panel.isConnected)
          current.textContent = "Publishing status could not be read. Try refreshing again.";
      })
      .finally(() => {
        finishWriting();
        refreshStatus.disabled = false;
      });
  });
  panel.append(
    element("h3", scope === "board" ? "Board publishing" : "Workspace publishing"),
    current,
    settings,
    refreshStatus,
  );
  return { panel, show, retain };
}

/** @template {keyof HTMLElementTagNameMap} Tag @param {Tag} tag @param {string} [text] */
function element(tag, text = "") {
  const node = document.createElement(tag);
  node.textContent = text;
  return node;
}

/** @param {string} title @param {HTMLElement} control */
function field(title, control) {
  const label = element("label", title);
  label.append(control);
  return label;
}

/** @param {string} title @param {() => void} action */
function button(title, action) {
  const node = element("button", title);
  node.type = "button";
  node.addEventListener("click", action);
  return node;
}

/** @param {Labeled[]} choices @param {string|null} selected */
function select(choices, selected) {
  const node = element("select");
  setOptions(node, choices, selected);
  return node;
}

/** @param {HTMLSelectElement} node @param {Labeled[]} choices @param {string|null} selected */
function setOptions(node, choices, selected) {
  node.replaceChildren();
  for (const choice of choices) {
    const option = element("option", choice.label);
    option.value = choice.id;
    node.append(option);
  }
  if (selected !== null) node.value = selected;
}

/** @param {string} title @param {HTMLElement[]} controls @param {(submit: HTMLButtonElement) => void} action */
function form(title, controls, action) {
  const node = element("form");
  const submit = element("button", title);
  submit.type = "submit";
  node.append(...controls, submit);
  node.addEventListener("submit", (event) => {
    event.preventDefault();
    if (!writing && !submit.disabled && node.closest('[data-unavailable="true"]') === null)
      action(submit);
  });
  return node;
}

/** @param {Reply} reply @param {string} collection @returns {unknown[]} */
function items(reply, collection) {
  const value = /** @type {Record<string, unknown>} */ (reply.data)[collection];
  if (!Array.isArray(value)) throw new ApiError("invalid_response");
  return value;
}

/** @param {(cursor?: string) => Promise<Reply>} request @param {string} collection
 * @param {AbortSignal} [signal] */
async function* collectionPages(request, collection, signal) {
  /** @type {string|undefined} */
  let cursor;
  const seen = new Set();
  for (;;) {
    signal?.throwIfAborted();
    const reply = await request(cursor);
    signal?.throwIfAborted();
    const page = items(reply, collection);
    const next = /** @type {{next_cursor?: unknown}} */ (reply.data).next_cursor;
    if (next !== null && (typeof next !== "string" || !next || seen.has(next))) {
      throw new ApiError("invalid_response");
    }
    yield page;
    if (next === null) return;
    seen.add(next);
    cursor = next;
  }
}

/** @template T @param {(cursor?: string) => Promise<Reply>} request
 * @param {string} collection @param {AbortSignal} [signal] @returns {Promise<T[]>} */
async function collect(request, collection, signal) {
  /** @type {T[]} */
  const result = [];
  for await (const page of collectionPages(request, collection, signal)) {
    result.push(.../** @type {T[]} */ (page));
  }
  return result;
}

/** @param {Reply} reply */
function etag(reply) {
  if (reply.etag === null) throw new ApiError("invalid_response");
  return reply.etag;
}

/** @param {AbortSignal} [signal] */
async function ensureCapability(signal) {
  signal?.throwIfAborted();
  if (capability) return;
  const session = (connecting ??= openBrowserSession()
    .then((token) => {
      capability = token;
      return token;
    })
    .finally(() => {
      connecting = undefined;
    }));
  if (!signal) {
    await session;
    return;
  }
  /** @type {() => void} */
  let aborted = () => {};
  try {
    await new Promise((resolve, reject) => {
      aborted = () => reject(signal.reason);
      signal.addEventListener("abort", aborted, { once: true });
      session.then(resolve, reject);
    });
  } finally {
    signal.removeEventListener("abort", aborted);
  }
}

/** @template T @param {() => Promise<T>} read @param {AbortSignal} [signal] */
async function readWithSession(read, signal) {
  await ensureCapability(signal);
  signal?.throwIfAborted();
  const original = capability;
  try {
    return await read();
  } catch (error) {
    signal?.throwIfAborted();
    if (!(error instanceof ApiError) || error.status !== 401) throw error;
    if (capability === original) capability = "";
    await ensureCapability(signal);
    signal?.throwIfAborted();
    return read();
  }
}

/** @param {HTMLInputElement|HTMLTextAreaElement} control @param {string} previous @param {string} saved
 * @param {boolean} [preserve] */
function adoptText(control, previous, saved, preserve = false) {
  if (!preserve && control.value === previous) {
    control.value = saved;
    return control.value;
  }
  // Use the browser's value normalization without disturbing the dirty control.
  const probe = /** @type {HTMLInputElement|HTMLTextAreaElement} */ (control.cloneNode(false));
  probe.value = saved;
  return probe.value;
}

/**
 * A textarea exposes CRLF and bare CR as LF. Keep the saved spelling of line
 * endings in the unchanged prefix and suffix. Use the textarea value verbatim
 * in the changed span, where the original line endings cannot be recovered.
 * @param {string} saved @param {string} baseline @param {string} edited
 */
function editedDescription(saved, baseline, edited) {
  if (!saved.includes("\r")) return edited;
  const sharedLength = Math.min(baseline.length, edited.length);
  let prefix = 0;
  while (prefix < sharedLength && baseline[prefix] === edited[prefix]) prefix++;
  let suffix = 0;
  while (
    prefix + suffix < sharedLength &&
    baseline[baseline.length - 1 - suffix] === edited[edited.length - 1 - suffix]
  )
    suffix++;

  let rawPrefix = 0;
  for (let i = 0; i < prefix; i++) {
    if (saved[rawPrefix] === "\r" && saved[rawPrefix + 1] === "\n") rawPrefix++;
    rawPrefix++;
  }
  let rawSuffix = saved.length;
  for (let i = 0; i < suffix; i++) {
    rawSuffix--;
    if (saved[rawSuffix] === "\n" && saved[rawSuffix - 1] === "\r") rawSuffix--;
  }
  const before = saved.slice(0, rawPrefix);
  const after = edited.slice(prefix, edited.length - suffix) + saved.slice(rawSuffix);
  // Complete the last bare CR as CRLF so it cannot absorb the following LF.
  if (before.endsWith("\r") && after.startsWith("\n")) return before + "\n" + after;
  return before + after;
}

/** @param {unknown} error @param {string} [submittingCapability] */
function writePhase(error, submittingCapability = capability) {
  if (!(error instanceof ApiError)) return "error";
  if (error.status === 401 && capability === submittingCapability) capability = "";
  if (error.status === 412) return "conflict";
  if (error.outcomeUnknown) return "unknown";
  return "error";
}

/** @param {unknown} error */
function writeRefusal(error) {
  if (error instanceof ApiError) {
    if (error.status === 413) {
      return error.code === "snapshot_too_large"
        ? "Native backups are limited to 96 MiB."
        : "Attachments are limited to 10 MiB.";
    }
    switch (error.code) {
      case "invalid_target":
        return "Choose a different, existing ticket on this board.";
      case "move_view_changed":
        return "The board view changed. Drag the card again from its current lane.";
      case "attachment_upload_active":
        return "Wait for attachments to finish uploading before making this change.";
      case "attachment_upload_inspection":
        return "Open the affected card and use Inspect saved card before making this change.";
      case "attachment_upload_storage":
        return "The tab could not save upload recovery details. Check available tab storage and try again.";
    }
  }
  return "The service refused the change. Your draft is still here.";
}

/** @param {string} scope @param {() => boolean} isCurrent @param {InspectionRead} focusedRead */
function requireInspection(scope, isCurrent, focusedRead) {
  const previous = pendingInspections.get(scope);
  pendingInspections.set(
    scope,
    previous
      ? {
          async read() {
            const earlier = await previous.read();
            const latest = await focusedRead();
            return earlier !== false && latest !== false;
          },
          isCurrent: () => previous.isCurrent() && isCurrent(),
        }
      : { read: focusedRead, isCurrent },
  );
  syncInspection(scope);
}

/** @param {HTMLElement} region @param {() => boolean} isCurrent @param {InspectionRead} read
 * @param {string} resource @param {string} scope
 * @param {(control: HTMLButtonElement) => boolean} [busy]
 * @param {(submission: SavedSubmission) => Promise<boolean>} [recover]
 * @param {{read: () => Promise<boolean>, sync: () => void}} [uploadRecovery] @returns {Inspection} */
function createInspection(
  region,
  isCurrent,
  read,
  resource,
  scope,
  busy = () => false,
  recover,
  uploadRecovery,
) {
  const label = "Inspect saved " + resource;
  const recovery = element("div");
  const recoveryMessage = element("p");
  recoveryMessage.setAttribute("role", "status");
  const dismiss = button("Dismiss unresolved action", () => {
    if (
      writing ||
      !isCurrent() ||
      attachmentUploads.get(scope)?.running ||
      (attachmentUploads.get(scope)?.needsInspection && pendingInspections.has(scope))
    )
      return;
    const submission = savedSubmission(scope);
    if (submission) clearSavedSubmission(scope, submission);
    syncInspection(scope);
  });
  recovery.hidden = true;
  recovery.append(recoveryMessage, dismiss);

  /** @param {boolean} disabled */
  function setSubmissionsDisabled(disabled) {
    for (const control of region.querySelectorAll("form button[type=submit]")) {
      if (control instanceof HTMLButtonElement)
        control.disabled =
          disabled || busy(control) || control.closest('[data-unavailable="true"]') !== null;
    }
  }

  return {
    scope,
    label,
    recovery,
    require(focusedRead) {
      requireInspection(scope, isCurrent, focusedRead);
    },
    sync() {
      if (!isCurrent()) return;
      const running = attachmentUploads.get(scope)?.running;
      const submission = recover && !running && savedSubmission(scope);
      const upload = uploadRecovery && !running && unconfirmedUpload(scope);
      setSubmissionsDisabled(pendingInspections.has(scope) || Boolean(submission || upload));
      uploadRecovery?.sync();
      recovery.hidden = !submission && !upload;
      dismiss.hidden = Boolean(running || upload || attachmentUploads.get(scope)?.needsInspection);
      if (upload)
        recoveryMessage.textContent =
          "Upload outcome unknown. Use " + label + " to check the original attachment ID.";
      else if (submission)
        recoveryMessage.textContent =
          "Unresolved " +
          submissionNames[submission.kind] +
          ". Use " +
          label +
          " to check the original submission.";
    },
    inspect() {
      if (writing || !isCurrent() || attachmentUploads.get(scope)?.running) return;
      startWriting();
      announce("loading", "Reading saved " + resource + "…");
      const pending = pendingInspections.get(scope);
      const upload = uploadRecovery && unconfirmedUpload(scope);
      const submission = recover && savedSubmission(scope);
      void readWithSession(async () => {
        const liveRead = submission && liveSubmissionReads.get(submission);
        /** @type {boolean|void} */
        let confirmed = upload ? await uploadRecovery?.read() : undefined;
        if (submission) {
          confirmed = liveRead ? await liveRead() : await recover?.(submission);
          if (confirmed === true) clearSavedSubmission(scope, submission);
        }
        if (pending) {
          const inspected = await pending.read();
          if (!pending.isCurrent() && isCurrent()) await read();
          if (inspected === false) confirmed = false;
        } else if (!submission && !upload) return read();
        return confirmed;
      })
        .then((confirmed) => {
          if (pendingInspections.get(scope) === pending) pendingInspections.delete(scope);
          syncInspection(scope);
          if (!isCurrent()) return;
          if (confirmed === false) {
            announce(
              "unknown",
              upload
                ? "The upload may still be running. Use " + label + " again to check its outcome."
                : "The submission could not be confirmed. Compare the saved state with your draft.",
            );
          } else if (submission || upload) {
            announce("ready", "Original submission confirmed by readback.");
          } else {
            announce("ready", "Saved " + resource + " read. Compare it with your draft.");
          }
        })
        .catch(() => {
          if (isCurrent()) announce("error", "Saved state could not be read.");
        })
        .finally(finishWriting);
    },
  };
}

/** @param {HTMLButtonElement|null} submit @param {(record: (identity: SubmissionIdentity) => void) => Promise<unknown>} request
 * @param {InspectionRead} update @param {string} success @param {Inspection} inspection
 * @param {PendingCard} [projection] */
async function writeResource(submit, request, update, success, inspection, projection) {
  const view = activeView;
  startWriting();
  if (submit) submit.disabled = true;
  announce("writing", "Saving…");
  let phase = "ready";
  /** @type {SavedSubmission|undefined} */
  let submission;
  try {
    await request((identity) => {
      submission = recordSubmission(inspection.scope, identity, update);
    });
  } catch (error) {
    if (projection) showPendingCard(projection, false);
    phase = writePhase(error);
    if (submission && phase !== "unknown") clearSavedSubmission(inspection.scope, submission);
    if (phase === "error" && view === activeView) {
      announce(phase, writeRefusal(error));
    }
  }
  let inspected = false;
  try {
    if (phase !== "error") {
      const confirmed = await readWithSession(update);
      if (submission && confirmed === true) clearSavedSubmission(inspection.scope, submission);
      if (projection) await refreshReplacementBoard(projection, view);
      inspected = true;
      if (phase === "ready" && confirmed === false) phase = "unknown";
      if (view === activeView) {
        if (phase === "unknown" && confirmed === true) {
          announce("ready", success + " Confirmed by readback.");
        } else {
          const uncertain = projection
            ? "The outcome was uncertain. Use " +
              inspection.label +
              " to compare saved state before making another edit."
            : "The outcome was uncertain. Inspect the latest state before making another edit.";
          announce(
            phase,
            phase === "ready"
              ? success
              : phase === "conflict"
                ? "State changed. Inspect the latest state before making another edit."
                : uncertain,
          );
        }
      }
    }
  } catch {
    if (!submission || savedSubmission(inspection.scope) !== submission) inspection.require(update);
    if (view === activeView) {
      announce(
        phase === "ready" ? "error" : phase,
        "Readback failed. Use " + inspection.label + " before making another edit.",
      );
    }
  } finally {
    if (projection) showPendingCard(projection, false);
    finishWriting();
    if (submit) {
      submit.disabled =
        (phase !== "error" && !inspected) || submit.closest('[data-unavailable="true"]') !== null;
    }
    if (submission) syncInspection(inspection.scope);
  }
}

/** @param {unknown} error */
function readError(error) {
  return error instanceof ApiError && error.status === 404
    ? "This board or card is unavailable."
    : "The latest state could not be read. Use Refresh to try again.";
}

/** @param {string} href */
function go(href) {
  if (writing) return;
  history.pushState(null, "", href);
  void load();
}

/** @param {string} href @param {string} title */
function link(href, title) {
  const node = element("a", title);
  node.href = href;
  node.addEventListener("click", (event) => followLink(event, href));
  return node;
}

/** @param {MouseEvent} event @param {string} href */
function followLink(event, href) {
  if (event.button !== 0 || event.altKey || event.ctrlKey || event.metaKey || event.shiftKey)
    return;
  event.preventDefault();
  go(href);
}

/** @param {string} boardId @param {number} ticket */
function ticketHref(boardId, ticket) {
  return "/boards/" + boardId + "?ticket=" + ticket;
}

/** @param {string} boardId @param {number} ticket */
async function findCardByTicket(boardId, ticket) {
  const matches = /** @type {Card[]} */ (
    items(await api.listCards(boardId, { ticket, archived: "include", limit: 2 }), "cards")
  );
  const [match] = matches;
  return matches.length === 1 && match?.board_id === boardId && match.ticket === ticket
    ? match
    : undefined;
}

/** @param {string} text @param {string} boardId @param {boolean} [linkTickets] */
function cardProse(text, boardId, linkTickets = true) {
  const body = element("p");
  body.className = "body-text";
  appendCardProse(
    body,
    text,
    linkTickets ? (ticket, label) => link(ticketHref(boardId, ticket), label) : undefined,
  );
  return body;
}

/** @param {CommentListEntry} comment @param {string} boardId @param {User[]} users */
function commentProse(comment, boardId, users) {
  return cardProse(
    comment.text,
    boardId,
    users.some((user) => user.id === comment.author_id && user.kind === "human"),
  );
}

const relationshipLabels = Object.freeze({
  blocks: "Blocks",
  duplicates: "Duplicates",
  references: "References",
  relates_to: "Relates to",
});
const relationshipChoices = Object.entries(relationshipLabels).map(([id, label]) => ({
  id,
  label,
}));

/** @param {Relationship} entry @param {string} cardId */
function relationshipDirection(entry, cardId) {
  if (entry.source_card_id === cardId || entry.kind === "relates_to")
    return relationshipLabels[entry.kind];
  return {
    blocks: "Other blocks this card",
    duplicates: "Other duplicates this card",
    references: "Other references this card",
  }[entry.kind];
}

function ticketInput() {
  const control = element("input");
  control.type = "number";
  control.min = "1";
  control.max = String(Number.MAX_SAFE_INTEGER);
  return control;
}

/** @param {RelationshipSeedDraft} row @param {SubmittedRelationshipSeed[]} submitted */
function matchesSubmittedSeed(row, submitted) {
  return submitted.some(
    (seed) => seed.row === row && seed.ticket === row.ticket && seed.kind === row.kind,
  );
}

/** @param {string} boardId */
function renderRelationshipSeeds(boardId) {
  const panel = element("details");
  const list = element("div");
  const state = relationshipSeedDrafts.get(boardId) ?? { rows: [] };
  relationshipSeedDrafts.set(boardId, state);
  /** @type {Map<RelationshipSeedDraft, {node: HTMLFieldSetElement, ticket: HTMLInputElement, kind: HTMLSelectElement}>} */
  const controls = new Map();
  const add = button("Add relationship", () => {
    if (add.disabled) return;
    append({ ticket: "", kind: "relates_to" }).focus();
    updateAddButton();
  });
  panel.append(element("summary", "Link the new card"), list, add);

  function updateAddButton() {
    add.disabled = Boolean(panel.hidden) || state.rows.length >= 1000;
  }

  /** @param {RelationshipSeedDraft} row */
  function mount(row) {
    const node = element("fieldset");
    node.disabled = Boolean(panel.hidden);
    const ticket = ticketInput();
    ticket.value = row.ticket;
    const kind = select(relationshipChoices, row.kind);
    node.append(
      element("legend", "Relationship"),
      field("Target ticket", ticket),
      field("Link type", kind),
      button("Remove relationship", () => {
        remove(row);
        add.focus();
      }),
    );
    controls.set(row, { node, ticket, kind });
    list.append(node);
    return ticket;
  }

  /** @param {RelationshipSeedDraft} row */
  function append(row) {
    state.rows.push(row);
    return mount(row);
  }

  /** @param {RelationshipSeedDraft} row */
  function remove(row) {
    controls.get(row)?.node.remove();
    controls.delete(row);
    state.rows = state.rows.filter((candidate) => candidate !== row);
    updateAddButton();
  }

  function syncValues() {
    for (const [row, { ticket, kind }] of controls) {
      row.ticket = ticket.value;
      row.kind = /** @type {RelationshipKind} */ (kind.value);
    }
  }

  for (const row of state.rows) mount(row);
  if (!state.rows.length) append({ ticket: "", kind: "relates_to" });
  panel.open = state.rows.some((row) => row.ticket !== "");
  updateAddButton();
  state.discard = (submitted) => {
    syncValues();
    for (const row of state.rows) {
      if (matchesSubmittedSeed(row, submitted)) remove(row);
    }
    if (!state.rows.length) append({ ticket: "", kind: "relates_to" });
  };
  return {
    panel,
    /** @param {boolean} allowed */
    setAllowed(allowed) {
      panel.hidden = !allowed;
      for (const { node } of controls.values()) node.disabled = !allowed;
      updateAddButton();
    },
    capture() {
      syncValues();
      return state.rows.filter((row) => row.ticket !== "").map((row) => ({ ...row, row }));
    },
    /** @param {SubmittedRelationshipSeed[]} submitted */
    clearSubmitted(submitted) {
      if (!submitted.length) return;
      if (state.discard) state.discard(submitted);
      else state.rows = state.rows.filter((row) => !matchesSubmittedSeed(row, submitted));
    },
    retain() {
      syncValues();
      delete state.discard;
    },
  };
}

/** @param {string} boardId @param {{title: string, description: string}} submitted */
function clearCreatedCardText(boardId, submitted) {
  const saved = boardDrafts.get(boardId);
  if (saved?.[0] === submitted.title) saved[0] = "";
  if (saved?.[1] === submitted.description) saved[1] = "";
  const scope = newCardScope(boardId);
  const persisted = { ...textDrafts.get(scope) };
  if (persisted.title === submitted.title) delete persisted.title;
  if (persisted.description === submitted.description) delete persisted.description;
  storeTextDraft(scope, persisted);
  if (activeNewCardText?.boardId === boardId) {
    const { title, description } = activeNewCardText;
    if (title.value === submitted.title) title.value = "";
    if (description.value === submitted.description) description.value = "";
  }
}

/** @param {string} scope @param {string} submitted */
function clearSubmittedComment(scope, submitted) {
  if (activeComment?.scope === scope && activeComment.control.value === submitted) {
    activeComment.control.value = "";
  }
  const memory = cardDrafts.get(scope);
  if (memory?.comment === submitted) delete memory.comment;
  const persisted = textDrafts.get(scope);
  if (persisted?.comment === submitted) {
    const { comment, ...remaining } = persisted;
    storeTextDraft(scope, remaining);
  }
}

/** @param {string} boardId @param {string} id */
async function readCreatedCard(boardId, id) {
  try {
    const saved = /** @type {Card} */ ((await api.getCard(id)).data);
    if (saved.id !== id || saved.board_id !== boardId) throw new ApiError("invalid_response");
    return saved;
  } catch (error) {
    if (error instanceof ApiError && error.status === 404) return undefined;
    throw error;
  }
}

/** @param {{title: string, description: string}} draft */
async function newCardTextFingerprint(draft) {
  const bytes = new TextEncoder().encode(JSON.stringify([draft.title, draft.description]));
  const digest = new Uint8Array(await crypto.subtle.digest("SHA-256", bytes));
  return Array.from(digest, (byte) => byte.toString(16).padStart(2, "0")).join("");
}

/** @param {ClipboardEvent} event */
function pastedFiles(event) {
  const clipboard = event.clipboardData;
  if (!clipboard || event.defaultPrevented) return [];
  if (
    (event.target instanceof HTMLInputElement || event.target instanceof HTMLTextAreaElement) &&
    clipboard.types.includes("text/plain")
  )
    return [];
  return [...clipboard.files];
}

/** @param {string} boardId @param {() => boolean} canStage */
function renderNewCardFiles(boardId, canStage) {
  const picker = element("input");
  picker.type = "file";
  picker.multiple = true;
  const list = element("ul");
  const scope = newCardScope(boardId);
  const canChange = () =>
    canStage() && !writing && !pendingInspections.has(scope) && !savedSubmission(scope);

  function sync() {
    picker.disabled = !canChange();
    for (const control of list.querySelectorAll("button")) control.disabled = picker.disabled;
  }

  /** @param {File[]} files */
  function store(files) {
    if (files.length) newCardFiles.set(boardId, files);
    else newCardFiles.delete(boardId);
    paint();
  }

  function paint() {
    const files = newCardFiles.get(boardId) ?? [];
    list.hidden = !files.length;
    list.replaceChildren(
      ...files.map((file, index) => {
        const row = element(
          "li",
          file.name + (file.size > MAX_ATTACHMENT_BYTES ? " (over 10 MiB)" : ""),
        );
        row.append(
          button("Remove " + file.name, () => {
            if (!canChange()) return;
            store(files.filter((_, other) => other !== index));
            picker.focus();
          }),
        );
        return row;
      }),
    );
    sync();
  }

  picker.addEventListener("change", () => {
    const files = [...(picker.files ?? [])];
    picker.value = "";
    if (canChange() && files.length) store([...(newCardFiles.get(boardId) ?? []), ...files]);
  });
  /** @param {ClipboardEvent} event */
  function paste(event) {
    if (!canChange()) return;
    const files = pastedFiles(event);
    if (!files.length) return;
    event.preventDefault();
    store([...(newCardFiles.get(boardId) ?? []), ...files]);
  }
  paint();
  return { field: field("Files to attach", picker), list, paint, paste, sync };
}

/** @param {string} boardId @param {SubmittedRelationshipSeed[]} submitted @param {() => boolean} isCurrent */
async function resolveRelationshipSeeds(boardId, submitted, isCurrent) {
  /** @type {Map<number, string>} */
  const targets = new Map();
  for (const { ticket } of submitted) {
    const number = Number(ticket);
    if (!isCurrent() || !Number.isSafeInteger(number) || number < 1)
      throw new ApiError("invalid_target");
    if (targets.has(number)) continue;
    const target = await readWithSession(() => findCardByTicket(boardId, number));
    if (!target || !isCurrent()) throw new ApiError("invalid_target");
    targets.set(number, target.id);
  }
  return submitted.map(({ ticket, kind }) => {
    const id = targets.get(Number(ticket));
    if (!id) throw new ApiError("invalid_target");
    return { target_card_id: id, kind };
  });
}

/** @param {string} name */
function downloadName(name) {
  const safe = name.replace(/[<>:"/\\|?*\p{Cc}\p{Cf}]/gu, "_").replace(/^[ .]+|[ .]+$/g, "");
  if (!safe) return "attachment";
  return /^(?:con|prn|aux|nul|com[1-9¹²³]|lpt[1-9¹²³])(?:\s*\.|$)/i.test(safe) ? "_" + safe : safe;
}

/** @param {string} boardId @param {string} cardId */
function cardScope(boardId, cardId) {
  return "card:" + boardId + ":" + cardId;
}

/** @param {string} boardId @param {string} cardId */
function uploadingToCard(boardId, cardId) {
  return attachmentUploads.get(cardScope(boardId, cardId))?.running === true;
}

/** @param {string} boardId @param {string[]} [cardIds] */
function requireAttachmentUploadsSettled(boardId, cardIds) {
  const batches =
    cardIds === undefined
      ? attachmentUploads.values()
      : cardIds.map((id) => attachmentUploads.get(cardScope(boardId, id)));
  for (const batch of batches) {
    if (batch?.boardId !== boardId) continue;
    if (batch.running) throw new ApiError("attachment_upload_active");
    if (batch.needsInspection) throw new ApiError("attachment_upload_inspection");
  }
  const scopes =
    cardIds === undefined
      ? (pendingUploads?.keys() ?? [])
      : cardIds.map((id) => cardScope(boardId, id));
  for (const scope of scopes) {
    if (scope.startsWith("card:" + boardId + ":") && unconfirmedUpload(scope)) {
      throw new ApiError("attachment_upload_inspection");
    }
  }
}

/** @param {string} boardId @param {string} cardId */
async function readCardVersion(boardId, cardId) {
  const latest = await api.getCard(cardId);
  const saved = /** @type {Card} */ (latest.data);
  if (saved.id !== cardId || saved.board_id !== boardId) throw new ApiError("invalid_response");
  return { saved, tag: etag(latest) };
}

/** @param {Lane[]} lanes @param {Workflow|null} workflow @param {string} source */
function moveDestinations(lanes, workflow, source) {
  const grants = workflow?.move.get(source);
  return activeLanes(lanes).filter(({ id }) => id !== source && grants?.has(id));
}

/** @param {Card} card @param {string} destination @param {string|(() => Promise<string>)} validator
 * @param {HTMLButtonElement|null} submit @param {() => Promise<void>} reloadBoard @param {Inspection} inspection */
function moveCard(card, destination, validator, submit, reloadBoard, inspection) {
  const { id, board_id: boardId } = card;
  /** @type {PendingCard} */
  const projection = { kind: "patch", card, fields: { lane_id: destination } };
  let accepted = false;
  void writeResource(
    submit,
    async () => {
      requireAttachmentUploadsSettled(boardId, [id]);
      const expected = typeof validator === "string" ? validator : await validator();
      showPendingCard(projection, true);
      const reply = await api.patchCard(id, expected, { lane_id: destination });
      const saved = /** @type {Card} */ (reply.data);
      if (saved.id !== id || saved.board_id !== boardId || saved.lane_id !== destination) {
        throw new ApiError("invalid_response", { outcomeUnknown: true });
      }
      accepted = true;
    },
    async () => {
      if (!accepted) {
        try {
          await readCardVersion(boardId, id);
        } catch (error) {
          if (!(error instanceof ApiError) || error.status !== 404) throw error;
        }
      }
      await reloadBoard();
      return accepted;
    },
    "Card saved.",
    inspection,
    projection,
  );
}

/** @param {string} boardId @param {string} cardId @param {Attachment} attachment */
function activeAttachment(boardId, cardId, attachment) {
  return (
    attachment.board_id === boardId && attachment.card_id === cardId && attachment.active === true
  );
}

/** @param {string} boardId @param {string} cardId @param {string} id */
async function readSubmittedAttachment(boardId, cardId, id) {
  let reply;
  try {
    reply = await api.getAttachment(id);
  } catch (error) {
    if (error instanceof ApiError && error.status === 404) return undefined;
    throw error;
  }
  const item = /** @type {Attachment} */ (reply.data);
  if (item.id !== id || item.card_id !== cardId || item.board_id !== boardId) {
    throw new ApiError("invalid_response");
  }
  return item;
}

/** @param {PendingUpload} upload @param {Attachment} item */
function matchesUploadBytes(upload, item) {
  return item.size === upload.size && item.sha2_256 === upload.digest;
}

/** @param {string} boardId @param {string} cardId @param {PendingUpload} upload */
async function readSubmittedUpload(boardId, cardId, upload) {
  const item = await readSubmittedAttachment(boardId, cardId, upload.id);
  if (item && !matchesUploadBytes(upload, item)) throw new ApiError("invalid_response");
  return item;
}

/** @param {File} file @returns {Promise<UploadFingerprint>} */
async function fingerprintUpload(file) {
  const hash = new Uint8Array(await crypto.subtle.digest("SHA-256", await file.arrayBuffer()));
  return {
    name: file.name,
    size: file.size,
    digest: Array.from(hash, (byte) => byte.toString(16).padStart(2, "0")).join(""),
  };
}

/** @param {string} boardId @param {string} cardId @param {number} [changed] */
function reportAttachmentUploads(boardId, cardId, changed) {
  if (activeDrawer?.boardId === boardId && activeDrawer.id === cardId) {
    activeDrawer.showAttachmentUploads(changed);
  }
}

/** @param {string} boardId */
async function refreshUploadBoard(boardId) {
  if (boardAtLocation() === boardId) await activeBoardRefresh?.request();
}

/** @param {string} boardId @param {string} cardId @param {string} initialTag @param {File[]} files
 * @param {boolean} [resuming] */
async function uploadFiles(boardId, cardId, initialTag, files, resuming = false) {
  const scope = cardScope(boardId, cardId);
  const stored = pendingUploads?.get(scope);
  if (uploadingToCard(boardId, cardId) || unconfirmedUpload(scope) || resuming !== Boolean(stored))
    return;
  const isCurrent = () => activeDrawer?.boardId === boardId && activeDrawer.id === cardId;
  const selected = files.flatMap((file, index) =>
    file.size <= MAX_ATTACHMENT_BYTES ? [{ file, index }] : [],
  );
  /** @type {UploadBatch} */
  const batch = {
    boardId,
    running: resuming || selected.length > 0,
    needsInspection: false,
    processed: resuming ? 0 : files.length - selected.length,
    results: (stored ?? files).map(({ name, size }) => ({
      name,
      outcome: size > MAX_ATTACHMENT_BYTES ? "Refused. Files are limited to 10 MiB." : "Waiting.",
    })),
    message: "",
  };
  attachmentUploads.set(scope, batch);
  reportAttachmentUploads(boardId, cardId);
  if (!batch.running) return;

  let expected = initialTag;
  let refreshNeeded = false;
  /** @type {number|undefined} */
  let committedRevision;
  try {
    if (!pendingUploads) throw new ApiError("attachment_upload_storage");
    /** @type {{file: File, index: number, upload: PendingUpload}[]} */
    const pending = [];
    const originals = stored?.map((upload, index) => ({ upload, index })) ?? [];
    if (resuming && (originals.length !== files.length || selected.length !== files.length)) {
      throw new ApiError("attachment_upload_selection");
    }
    for (const { file, index } of selected) {
      const fingerprint = await fingerprintUpload(file);
      if (resuming) {
        const match = originals.findIndex(
          ({ upload: { name, size, digest } }) =>
            name === fingerprint.name && size === fingerprint.size && digest === fingerprint.digest,
        );
        if (match < 0) throw new ApiError("attachment_upload_selection");
        const [original] = originals.splice(match, 1);
        if (!original) throw new ApiError("attachment_upload_selection");
        pending.push({ file, ...original });
      } else {
        pending.push({
          file,
          index,
          upload: { ...fingerprint, id: crypto.randomUUID(), attempted: false },
        });
      }
    }
    if (resuming) {
      pending.sort((a, b) => a.index - b.index);
      expected = (await readWithSession(() => readCardVersion(boardId, cardId))).tag;
    }
    let remaining = pending.map(({ upload }) => upload);
    savePendingUploads(scope, remaining);

    for (const { file, index, upload } of pending) {
      const result = batch.results[index];
      if (!result) throw new ApiError("attachment_upload_selection");
      result.id = upload.id;
      if (resuming) {
        const item = await readWithSession(() => readSubmittedUpload(boardId, cardId, upload));
        if (item) {
          remaining = remaining.slice(1);
          savePendingUploads(scope, remaining);
          committedRevision = item.revision;
          refreshNeeded = true;
          result.outcome = "Uploaded. Confirmed by readback.";
          batch.processed++;
          reportAttachmentUploads(boardId, cardId, index);
          continue;
        }
      }
      if (committedRevision !== undefined) {
        const { saved, tag } = await readWithSession(() => readCardVersion(boardId, cardId));
        if (saved.revision !== committedRevision) {
          batch.message =
            " The card changed during upload. Review the card before selecting more files.";
          break;
        }
        expected = tag;
        committedRevision = undefined;
      }
      // Persist dispatch intent before handing bytes to fetch. A reload cannot distinguish
      // a request that never left the browser from one that is still on the server.
      savePendingUploads(scope, [{ ...upload, attempted: true }, ...remaining.slice(1)]);
      result.outcome = "Uploading…";
      reportAttachmentUploads(boardId, cardId, index);
      const submittedCapability = capability;
      /** @type {Attachment|undefined} */
      let committed;
      try {
        const reply = await api.uploadAttachment(cardId, expected, file, upload.id);
        const item = /** @type {Attachment} */ (reply.data);
        if (
          item.id !== upload.id ||
          !activeAttachment(boardId, cardId, item) ||
          !matchesUploadBytes(upload, item)
        ) {
          throw new ApiError("invalid_response", { outcomeUnknown: true });
        }
        committed = item;
        result.outcome = "Uploaded.";
      } catch (error) {
        const phase = writePhase(error, submittedCapability);
        if (phase === "unknown" || !(error instanceof ApiError) || error.status === 409) {
          refreshNeeded = true;
          committed = await readWithSession(() =>
            readSubmittedUpload(boardId, cardId, upload),
          ).catch(() => undefined);
          if (committed) {
            result.outcome = "Uploaded. Confirmed by readback.";
          } else {
            result.outcome = "Outcome uncertain. Use Inspect saved card.";
            batch.message = " Inspect the card before submitting again.";
          }
        } else if ([400, 413, 422].includes(error.status ?? 0)) {
          remaining = remaining.slice(1);
          savePendingUploads(scope, remaining);
          result.outcome =
            error.status === 413 ? "Refused. Files are limited to 10 MiB." : "Upload refused.";
        } else {
          savePendingUploads(scope, remaining);
          refreshNeeded = true;
          result.outcome =
            phase === "conflict"
              ? "Refused. The card changed."
              : error.code === "unavailable"
                ? "Refused. The board is unavailable."
                : "Upload refused.";
          batch.message = " Review the card before resuming pending uploads.";
        }
      }
      if (committed) {
        remaining = remaining.slice(1);
        savePendingUploads(scope, remaining);
        committedRevision = committed.revision;
        refreshNeeded = true;
        if (!committed.active)
          batch.message =
            " The attachment was detached. Review the card before resuming pending uploads.";
      }
      batch.processed++;
      reportAttachmentUploads(boardId, cardId, index);
      if (batch.message) break;
    }
  } catch (error) {
    const code = error instanceof ApiError ? error.code : "";
    batch.message =
      " " +
      (code === "attachment_upload_selection"
        ? "Select all pending originals. Each file must match its recorded name, size and contents."
        : code === "attachment_upload_storage"
          ? writeRefusal(error)
          : "The files or current saved card could not be read.");
  } finally {
    if (refreshNeeded) {
      try {
        await refreshUploadBoard(boardId);
      } catch {
        if (!unconfirmedUpload(scope)) {
          batch.needsInspection = true;
          batch.message += " Use Inspect saved card to read the saved attachments.";
          requireInspection(scope, isCurrent, async () => {
            await refreshUploadBoard(boardId);
            batch.needsInspection = false;
            batch.message = "";
            reportAttachmentUploads(boardId, cardId);
          });
        }
      }
    }
    for (const result of batch.results) {
      if (result.outcome === "Waiting.") result.outcome = "Upload stopped before this file.";
    }
    batch.processed = batch.results.length;
    batch.running = false;
    reportAttachmentUploads(boardId, cardId);
  }
}

/** @param {User[]} users @param {ActorPresentation|undefined} presentation @param {string} id */
function actorAttribution(users, presentation, id) {
  const user = users.find((entry) => entry.id === id);
  const display = user && presentation?.[user.kind];
  const label = element("span", display?.label ?? user?.label ?? "Unknown user");
  label.className = "actor-label";
  const accent = display?.accent ?? user?.color;
  if (accent && /^#[0-9a-f]{6}$/i.test(accent)) {
    label.className += " actor-accent";
    label.style.borderInlineStartColor = accent;
  }
  return label;
}

/** @param {string} color */
function actionInk(color) {
  /** @param {number} offset */
  const linear = (offset) => {
    const channel = Number.parseInt(color.slice(offset, offset + 2), 16) / 255;
    return channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4;
  };
  const luminance = 0.2126 * linear(1) + 0.7152 * linear(3) + 0.0722 * linear(5);
  return 1.05 / (luminance + 0.05) >= 4.5 ? "#ffffff" : "#000000";
}

function focusWasLost() {
  const focused = document.activeElement;
  return !focused || focused === document.body || !focused.isConnected;
}

/** @param {string} id @param {HTMLElement} opener */
function restoreCardFocus(id, opener) {
  /** @param {HTMLElement} control */
  const visible = (control) =>
    control.isConnected && !control.closest("[hidden]") && !control.closest("details:not([open])");
  const replacement = [...(main?.querySelectorAll("button[data-card-id]") ?? [])].find(
    (control) =>
      control instanceof HTMLButtonElement && control.dataset.cardId === id && visible(control),
  );
  const filter = main?.querySelector("#board-filter");
  const target =
    opener.dataset.cardId === id && visible(opener)
      ? opener
      : (replacement ?? (filter instanceof HTMLInputElement ? filter : refresh));
  if (target instanceof HTMLElement) target.focus();
}

/** @param {Lane} lane */
function laneNotes(lane) {
  const note = element("p", lane.notes);
  note.className = "lane-note body-text";
  return note;
}

/** @param {string} boardId @param {Pick<Card, "id">} summary @param {Lane[]} lanes @param {User[]} users
 * @param {Labeled[]} priorities @param {() => string} boardTag @param {() => Promise<void>} reloadBoard
 * @param {() => Workflow|null} currentWorkflow @param {HTMLElement} opener
 * @param {() => ActorPresentation|undefined} presentation @param {boolean} [restoring] */
async function openCard(
  boardId,
  summary,
  lanes,
  users,
  priorities,
  boardTag,
  reloadBoard,
  currentWorkflow,
  opener,
  presentation,
  restoring = false,
) {
  if (!details) return;
  retainCardDraft?.();
  releaseCardMedia?.();
  releaseCardMedia = undefined;
  retainCardDraft = undefined;
  cardInspection = undefined;
  retrySelectedCard = undefined;
  activeComment = undefined;
  activeDrawer = undefined;
  const view = details;
  const cardView = ++activeCard;
  rememberCard(boardId, summary.id);
  const isSelected = () =>
    cardView === activeCard && boardViewState(boardId).card?.id === summary.id;
  let focusHeading = !restoring;
  view.hidden = false;
  delete view.dataset.unavailable;
  view.replaceChildren(element("p", "Loading card…"));
  const initialFocus = document.activeElement;
  let invalidSelection = false;
  try {
    let reply;
    for (let attempt = 0; attempt < 2; attempt++) {
      // The board tag changes with its in-place paint, before auxiliary reads finish.
      const requestedTag = boardTag();
      try {
        const result = await readWithSession(() => api.getCard(summary.id));
        if (!isSelected()) return;
        if (requestedTag === boardTag()) {
          reply = result;
          break;
        }
      } catch (error) {
        if (!isSelected()) return;
        if (requestedTag === boardTag()) throw error;
      }
      focusHeading = false;
    }
    if (!reply) throw new Error("The board changed during both card reads.");
    const card = /** @type {Card} */ (reply.data);
    if (card.id !== summary.id || card.board_id !== boardId) {
      invalidSelection = true;
      throw new ApiError("invalid_response");
    }
    let cardTag = etag(reply);
    let savedCard = card;
    const title = element("input");
    title.value = card.title;
    let composingTitle = false;
    let compositionEnter = false;
    title.addEventListener("compositionstart", () => {
      composingTitle = true;
    });
    title.addEventListener("compositionend", () => {
      composingTitle = false;
    });
    title.addEventListener("keydown", (event) => {
      if (event.key === "Enter") {
        // Allow the IME to accept the candidate. Its Enter can also reach the
        // form after compositionend, so guard that submission for this event.
        compositionEnter = event.isComposing || composingTitle || event.keyCode === 229;
        if (compositionEnter)
          setTimeout(() => {
            compositionEnter = false;
          }, 0);
      }
    });
    const description = element("textarea");
    description.value = card.description;
    let titleBaseline = title.value;
    let descriptionBaseline = description.value;
    const lane = element("select");
    lane.required = true;
    const moveAction = element("div");
    let moveBoardTag = boardTag();
    const owner = select(users, card.owner_id);
    owner.required = true;
    const priority = select(priorities, card.priority_id);
    priority.required = true;
    const current = element("div");
    const commentList = element("div");
    const attachmentList = element("div");
    const historyPanel = element("details");
    const historyList = element("div");
    const relationshipList = element("div");
    const hierarchy = element("section");
    const hierarchyList = element("div");
    hierarchy.append(element("h3", "Hierarchy"), hierarchyList);
    hierarchy.hidden = true;
    let hierarchyRead = 0;
    const archiveAction = element("div");
    const approvalAction = element("div");
    let attachmentGeneration = 0;
    /** @type {Set<string>} */
    const previewUrls = new Set();
    function clearPreviews() {
      ++attachmentGeneration;
      for (const url of previewUrls) URL.revokeObjectURL(url);
      previewUrls.clear();
    }
    releaseCardMedia = clearPreviews;
    const comment = element("textarea");
    comment.required = true;
    const scope = cardScope(boardId, card.id);
    activeComment = { scope, control: comment };
    const upload = element("input");
    upload.type = "file";
    upload.required = true;
    upload.multiple = true;
    const uploadProgress = element("p");
    uploadProgress.setAttribute("role", "status");
    uploadProgress.hidden = true;
    const uploadResults = element("ul");
    uploadResults.hidden = true;
    uploadResults.className = "body-text";
    const uploadRecovery = element("div");
    uploadRecovery.hidden = true;
    const resumeUploads = button("Resume pending uploads", () => {
      if (!canUpload(true)) return;
      const files = Array.from(upload.files ?? []);
      if (!files.length) return;
      upload.value = "";
      void uploadFiles(boardId, card.id, cardTag, files, true);
    });
    const discardUploads = button("Discard pending files", () => {
      if (!canUpload(true)) return;
      try {
        savePendingUploads(scope, []);
        attachmentUploads.delete(scope);
        upload.value = "";
        showAttachmentUploads();
      } catch (error) {
        announce("error", writeRefusal(error));
      }
    });
    uploadRecovery.append(
      element("p", "Reselect all pending originals in Files, then resume their uploads."),
      resumeUploads,
      discardUploads,
    );
    reconcileTextReversions(scope, card);
    /** @type {CardDraft|undefined} */
    const previous = cardDrafts.get(scope) ?? textDrafts.get(scope);
    if (previous?.title !== undefined) title.value = previous.title;
    if (previous?.description !== undefined) description.value = previous.description;
    let moveDraft = previous?.lane ?? "";
    lane.addEventListener("change", () => {
      moveDraft = lane.value;
    });
    if (previous?.owner !== undefined && users.some(({ id }) => id === previous.owner))
      owner.value = previous.owner;
    if (previous?.priority !== undefined && priorities.some(({ id }) => id === previous.priority))
      priority.value = previous.priority;
    if (previous?.comment !== undefined) comment.value = previous.comment;
    /** @param {boolean} [captureReversions] */
    function retainText(captureReversions = true) {
      /** @type {CardDraft} */
      const draft = {};
      const submitted =
        captureReversions &&
        submittedCardText?.scope === scope &&
        submittedCardText.outcome !== "refused"
          ? submittedCardText
          : undefined;
      const previous = cardDrafts.get(scope);
      const fields = /** @type {const} */ ([
        ["title", title.value, titleBaseline],
        ["description", description.value, descriptionBaseline],
      ]);
      for (const [field, value, baseline] of fields) {
        const carried = previous?.[field] === value ? previous.textReversions?.[field] : undefined;
        const reverted =
          value === baseline &&
          submitted?.revision === savedCard.revision &&
          submitted.fields[field] !== undefined &&
          submitted.editor[field] !== value;
        const reversion = carried ?? (reverted ? submitted : undefined);
        if (reversion) {
          // Keep provenance even when a replacement drawer has a newer baseline.
          (draft.textReversions ??= {})[field] = reversion;
          draft[field] = value;
        } else if (value !== baseline) {
          draft[field] = value;
        }
      }
      const selectedMove = lane.value || moveDraft;
      if (selectedMove && selectedMove !== savedCard.lane_id) draft.lane = selectedMove;
      if (owner.value !== savedCard.owner_id) draft.owner = owner.value;
      if (priority.value && priority.value !== savedCard.priority_id)
        draft.priority = priority.value;
      if (comment.value) draft.comment = comment.value;
      storeCardDraft(scope, draft);
    }
    retainCardDraft = retainText;
    for (const control of [title, description, comment]) {
      control.addEventListener("input", () => {
        if (isSelected()) retainText();
      });
    }
    /** @param {string} id */
    const attribution = (id) => actorAttribution(users, presentation(), id);
    /** @param {boolean} [returnFocus] */
    function closeDrawer(returnFocus = true) {
      if (cardView !== activeCard) return;
      retainCardDraft?.();
      clearPreviews();
      releaseCardMedia = undefined;
      retainCardDraft = undefined;
      cardInspection = undefined;
      activeComment = undefined;
      activeDrawer = undefined;
      rememberCard(boardId);
      ++activeCard;
      view.hidden = true;
      view.replaceChildren();
      if (returnFocus) restoreCardFocus(card.id, opener);
    }
    let workflowRead = 0;
    /** @param {() => boolean} [canApply] @param {boolean} [automatic] */
    async function refreshWorkflow(canApply = () => true, automatic = false) {
      const read = ++workflowRead;
      const isCurrent = () => cardView === activeCard && read === workflowRead && canApply();
      let cardRead = false;
      try {
        await readSavedCard(isCurrent);
        cardRead = true;
        if (!isCurrent()) return;
        await Promise.all([
          refreshLists(),
          refreshRelationships(),
          historyPanel.open ? refreshHistory() : undefined,
        ]);
      } catch (error) {
        if (!isCurrent()) return;
        if (!cardRead) moveAction.hidden = true;
        if (cardRead || !(error instanceof ApiError) || error.status !== 404) {
          if (!automatic && !pendingInspections.has(inspection.scope))
            inspection.require(refreshWorkflow);
          throw error;
        }
        retainCardDraft?.();
        rememberCard(boardId);
        view.dataset.unavailable = "true";
        ++listRead;
        ++relationshipRead;
        ++hierarchyRead;
        ++historyRead;
        clearPreviews();
        heading.textContent = "Card unavailable";
        pendingSummary.hidden = true;
        current.replaceChildren(
          element("p", "This card is absent from the current board. Your drafts are retained."),
        );
        commentList.replaceChildren();
        attachmentList.replaceChildren();
        relationshipList.replaceChildren();
        hierarchy.hidden = true;
        hierarchyList.replaceChildren();
        historyList.replaceChildren();
        archiveAction.hidden = true;
        approvalAction.hidden = true;
        inspection.sync();
      }
    }
    activeDrawer = {
      boardId,
      id: card.id,
      close: () => closeDrawer(view.contains(document.activeElement)),
      refreshWorkflow,
      refreshPriority,
      settleText,
      showPending,
      pasteAttachments,
      showAttachmentUploads,
    };
    const close = button("Close card", () => {
      if (!writing) closeDrawer();
    });
    const heading = element("h2", `#${card.ticket} ${card.title}`);
    heading.tabIndex = -1;
    const pendingSummary = element("p");
    pendingSummary.setAttribute("role", "status");
    const inspection = createInspection(
      view,
      () => cardView === activeCard,
      async () => {
        await reloadBoard();
      },
      "card",
      scope,
      (control) =>
        (control.dataset.cardRevisionWrite === "true" && uploadingToCard(boardId, card.id)) ||
        (control.dataset.newAttachmentUpload === "true" && pendingUploads?.has(scope) === true),
      recoverCardSubmission,
      { read: recoverAttachmentUpload, sync: syncUploadRecovery },
    );
    cardInspection = inspection;

    /** @param {string} label @param {HTMLElement[]} controls @param {(submit: HTMLButtonElement) => void} action
     * @param {boolean} [accented] */
    function cardRevisionForm(label, controls, action, accented = false) {
      const node = form(label, controls, (submit) => {
        if (isSelected() && !uploadingToCard(boardId, card.id) && !unconfirmedUpload(scope))
          action(submit);
      });
      const control = node.querySelector("button");
      if (control) {
        control.dataset.cardRevisionWrite = "true";
        if (accented) control.className = "board-action";
      }
      return node;
    }

    /** @param {Card} saved */
    async function refreshHierarchy(saved) {
      const read = ++hierarchyRead;
      const ids = [...(saved.parent_id ? [saved.parent_id] : []), ...(saved.child_ids ?? [])];
      hierarchy.hidden = ids.length === 0;
      if (!ids.length) {
        hierarchyList.replaceChildren();
        return;
      }
      const isCurrent = () => cardView === activeCard && read === hierarchyRead;
      try {
        const targets = await readWithSession(async () => {
          /** @type {Card[]} */
          const found = [];
          for (let i = 0; i < ids.length && isCurrent(); i += 16) {
            found.push(
              ...(await Promise.all(
                ids.slice(i, i + 16).map(async (id) => {
                  const target = /** @type {Card} */ ((await api.getCard(id)).data);
                  if (target.board_id !== boardId || target.id !== id)
                    throw new ApiError("invalid_response");
                  return target;
                }),
              )),
            );
          }
          return found;
        });
        if (!isCurrent()) return;
        const focusedId = [...hierarchyList.querySelectorAll("a")].find(
          (node) => node === document.activeElement,
        )?.dataset.hierarchyCardId;
        const rows = targets.map((target) => {
          const row = element("p", (target.id === saved.parent_id ? "Parent" : "Child") + " ");
          const openerLink = element("a", "#" + target.ticket + " " + target.title);
          openerLink.href = ticketHref(boardId, target.ticket);
          openerLink.dataset.hierarchyCardId = target.id;
          openerLink.addEventListener("click", (event) => {
            if (
              event.button !== 0 ||
              event.altKey ||
              event.ctrlKey ||
              event.metaKey ||
              event.shiftKey
            )
              return;
            event.preventDefault();
            if (
              !writing &&
              cardView === activeCard &&
              (savedCard.parent_id === target.id || savedCard.child_ids?.includes(target.id))
            ) {
              void openCard(
                boardId,
                target,
                lanes,
                users,
                priorities,
                boardTag,
                reloadBoard,
                currentWorkflow,
                opener,
                presentation,
              );
            }
          });
          row.append(openerLink);
          return row;
        });
        hierarchyList.replaceChildren(...rows);
        if (focusedId)
          [...hierarchyList.querySelectorAll("a")]
            .find((node) => node.dataset.hierarchyCardId === focusedId)
            ?.focus({ preventScroll: true });
      } catch {
        if (isCurrent())
          hierarchyList.replaceChildren(element("p", "Hierarchy could not be read."));
      }
    }

    function showPending() {
      if (cardView !== activeCard || view.dataset.unavailable === "true") return;
      const fields = pendingFields(savedCard);
      heading.textContent = `#${savedCard.ticket} ${fields?.title ?? savedCard.title}`;
      pendingSummary.hidden = !fields;
      const destination = fields?.lane_id && lanes.find(({ id }) => id === fields.lane_id);
      pendingSummary.textContent = destination
        ? "Moving to " + destination.label + "…"
        : fields
          ? "Saving card…"
          : "";
    }

    /** @param {Card} saved */
    function showCurrent(saved) {
      showPending();
      updateActions(saved);
      void refreshHierarchy(saved);
      const links = current.contains(document.activeElement)
        ? [...current.querySelectorAll("a")]
        : [];
      const focusedLink = links.find((link) => link === document.activeElement);
      const body = cardProse(saved.description, boardId);
      const savedLane = lanes.find(({ id }) => id === saved.lane_id);
      current.replaceChildren(
        element("h3", "Saved card"),
        element("p", saved.title),
        body,
        element(
          "p",
          "Lane: " + (savedLane?.label ?? "Unavailable") + (savedLane?.retired ? " (retired)" : ""),
        ),
        element(
          "p",
          "Priority: " +
            (priorities.find(({ id }) => id === saved.priority_id)?.label ?? "Unavailable"),
        ),
        ...(saved.archived ? [element("p", "Archived")] : []),
        element(
          "p",
          "Owner: " + (users.find(({ id }) => id === saved.owner_id)?.label ?? "Unknown user"),
        ),
      );
      if (focusedLink) {
        const index = links.filter(({ href }) => href === focusedLink.href).indexOf(focusedLink);
        const matching = [...body.querySelectorAll("a")].filter(
          ({ href }) => href === focusedLink.href,
        );
        (matching[index] ?? heading).focus({ preventScroll: true });
      }
    }

    /** @param {Partial<CardText>} discarded */
    function restoreBaselineText(discarded) {
      if (!composingTitle && title.value === discarded.title && title.value !== titleBaseline)
        title.value = titleBaseline;
      if (
        description.value === discarded.description &&
        description.value !== descriptionBaseline
      ) {
        description.value = descriptionBaseline;
      }
    }

    /** @param {Partial<CardText>} [discarded] */
    function settleText(discarded = reconcileTextReversions(scope, savedCard).discarded) {
      restoreBaselineText(discarded);
      retainText(false);
    }

    /** @param {Partial<CardText>} [discarded] */
    function settleDisplayedText(discarded) {
      if (activeDrawer?.boardId === boardId && activeDrawer.id === card.id)
        activeDrawer.settleText(discarded);
    }

    /** @param {Card} saved */
    function adoptSaved(saved) {
      const { preserve, discarded } = reconcileTextReversions(scope, saved);
      restoreBaselineText(discarded);
      titleBaseline = adoptText(
        title,
        titleBaseline,
        saved.title,
        composingTitle || preserve.title,
      );
      descriptionBaseline = adoptText(
        description,
        descriptionBaseline,
        saved.description,
        preserve.description,
      );
      if (owner.value === savedCard.owner_id) owner.value = saved.owner_id;
      if (priority.value === savedCard.priority_id) priority.value = saved.priority_id;
      savedCard = saved;
      showCurrent(saved);
      retainText(false);
    }

    let savedRead = 0;
    /** @param {() => boolean} [canApply] */
    async function readSavedCard(canApply = () => true) {
      const read = ++savedRead;
      const { saved, tag } = await readCardVersion(boardId, card.id);
      if (cardView === activeCard && read === savedRead && canApply()) {
        delete view.dataset.unavailable;
        const ownerSelection = owner.value || savedCard.owner_id;
        setOptions(
          owner,
          users.some(({ id }) => id === ownerSelection)
            ? users
            : [...users, { id: ownerSelection, label: "Unavailable owner draft" }],
          ownerSelection,
        );
        cardTag = tag;
        adoptSaved(saved);
      }
      return saved;
    }

    /** @param {"conflict"|"unknown"} phase */
    async function recoverTextSave(phase) {
      const hadInspection = pendingInspections.has(scope);
      inspection.require(async () => {
        await readSavedCard();
        if (cardView === activeCard) await reloadBoard();
      });
      const pending = pendingInspections.get(scope);
      let failed = false;
      if (cardView === activeCard) {
        try {
          await readWithSession(readSavedCard);
        } catch {
          failed = true;
        }
      }

      const drawer = activeDrawer;
      const refresh = activePendingBoard?.id === boardId ? activeBoardRefresh : undefined;
      if (refresh && (!failed || cardView !== activeCard)) {
        try {
          await refresh.request();
          if (
            !hadInspection &&
            pendingInspections.get(scope) === pending &&
            drawer === activeDrawer &&
            drawer?.boardId === boardId &&
            drawer.id === card.id &&
            view.dataset.unavailable !== "true"
          ) {
            pendingInspections.delete(scope);
          }
        } catch {
          failed = true;
        }
      }
      if (cardView !== activeCard) return;
      announce(
        phase,
        failed
          ? "Readback failed. Use Inspect saved card before making another edit."
          : phase === "conflict"
            ? "The card changed. Compare your draft with the saved card before saving again."
            : "The save outcome was uncertain. Use Inspect saved card to compare your draft with saved state.",
      );
    }

    /** @param {PendingFields} draft @param {HTMLButtonElement} submit */
    async function save(draft, submit) {
      if (cardView !== activeCard) return;
      const initiatingView = activeView;
      /** @type {CardTextSubmission} */
      const submission = {
        scope,
        revision: savedCard.revision,
        fields: draft,
        editor: { title: title.value, description: description.value },
        outcome: "pending",
      };
      /** @type {PendingCard} */
      const projection = { kind: "patch", card: savedCard, fields: draft };
      startWriting();
      submittedCardText = submission;
      submit.disabled = true;
      announce("writing", "Saving card…");
      try {
        showPendingCard(projection, true);
        const result = await api.patchCard(card.id, cardTag, draft);
        const saved = /** @type {Card} */ (result.data);
        if (saved.board_id !== boardId)
          throw new ApiError("invalid_response", { outcomeUnknown: true });
        const nextTag = etag(result);
        submission.outcome = "confirmed";
        if (cardView !== activeCard) {
          settleDisplayedText();
          return;
        }
        cardTag = nextTag;
        retainText();
        adoptSaved(saved);
        announce("ready", "Card saved.");
      } catch (error) {
        showPendingCard(projection, false);
        const phase = writePhase(error);
        submission.outcome = phase === "unknown" ? "unknown" : "refused";
        if (submission.outcome === "refused") {
          settleDisplayedText(reconcileTextReversions(scope).discarded);
        } else {
          if (cardView === activeCard) retainText();
          settleDisplayedText();
        }
        if (phase === "error") {
          if (cardView === activeCard)
            announce("error", "Card edit refused. Your draft is still here.");
        } else {
          await recoverTextSave(phase);
        }
      } finally {
        if (submission.outcome === "confirmed") {
          if (initiatingView === activeView) await reloadBoard().catch(() => {});
          await refreshReplacementBoard(projection, initiatingView).catch(() => {});
        }
        showPendingCard(projection, false);
        submittedCardText = undefined;
        finishWriting();
        syncInspection(scope);
      }
    }

    let listRead = 0;
    async function refreshLists() {
      const read = ++listRead;
      const [comments, attachments] = await Promise.all([
        /** @type {Promise<CommentListEntry[]>} */ (
          collect(
            (cursor) =>
              api.listComments({ board_id: boardId, card_id: card.id }, undefined, cursor),
            "comments",
          )
        ),
        /** @type {Promise<Attachment[]>} */ (
          collect((cursor) => api.listAttachments(card.id, cursor), "attachments")
        ),
      ]);
      if (cardView !== activeCard || read !== listRead) return { comments, attachments };
      commentList.replaceChildren(
        ...comments.map((entry) => {
          const item = element("article");
          const body = commentProse(entry, boardId, users);
          const byline = element("small");
          byline.append(attribution(entry.author_id), element("span", " · " + entry.created_at));
          item.append(byline, body);
          return item;
        }),
      );
      clearPreviews();
      attachmentList.replaceChildren(
        ...attachments
          .filter((entry) => activeAttachment(boardId, card.id, entry))
          .map(renderAttachment),
      );
      inspection.sync();
      return { comments, attachments };
    }

    async function readCurrent() {
      const saved = await readSavedCard();
      await reloadBoard();
      const [resources] = await Promise.all([
        refreshLists(),
        cardView === activeCard && historyPanel.open ? refreshHistory() : undefined,
      ]);
      return { ...resources, saved };
    }

    /** @param {string} id */
    async function readSubmittedComment(id) {
      return (await readCurrent()).comments.find(
        (entry) => entry.id === id && entry.card_id === card.id,
      );
    }

    /** @param {string} id */
    async function readSubmittedApproval(id) {
      try {
        await api.inspectApproval(id, boardId, card.id);
        return true;
      } catch (error) {
        if (error instanceof ApiError && error.status === 404) return false;
        throw error;
      }
    }

    /** @param {SavedSubmission} submission */
    async function recoverCardSubmission(submission) {
      switch (submission.kind) {
        case "comment": {
          const saved = await readSubmittedComment(submission.id);
          if (saved && savedSubmission(scope) === submission)
            clearSubmittedComment(scope, saved.text);
          return saved !== undefined;
        }
        case "add-link":
          return (await readLinks()).some((entry) => entry.id === submission.id);
        case "approve-card": {
          const approved = await readSubmittedApproval(submission.id);
          await readCurrent();
          return approved;
        }
        case "create-card":
          return false;
      }
    }

    async function recoverAttachmentUpload() {
      const upload = unconfirmedUpload(scope);
      if (!upload) return false;
      const item = await readSubmittedUpload(boardId, card.id, upload);
      await readCurrent();
      if (!item) return false;
      savePendingUploads(scope, pendingUploads?.get(scope)?.slice(1) ?? []);
      const batch = attachmentUploads.get(scope);
      const result = batch?.results.find(({ id }) => id === upload.id);
      if (result) result.outcome = "Uploaded. Confirmed by readback.";
      if (batch && result) {
        batch.processed = Math.min(batch.processed + 1, batch.results.length);
        batch.message = "";
      }
      showAttachmentUploads();
      return true;
    }

    function syncUploadRecovery() {
      const available =
        pendingUploads?.has(scope) === true &&
        !uploadingToCard(boardId, card.id) &&
        !hasUnresolvedInspection(scope) &&
        view.dataset.unavailable !== "true";
      uploadRecovery.hidden = !available;
      resumeUploads.disabled = !available;
      discardUploads.disabled = !available;
    }

    /** @param {number} [changed] */
    function showAttachmentUploads(changed) {
      const pending = pendingUploads?.get(scope);
      let batch = attachmentUploads.get(scope);
      if (!batch && pending) {
        batch = {
          boardId,
          running: false,
          needsInspection: false,
          processed: 0,
          message: "",
          results: pending.map(({ name, id, attempted }) => ({
            name,
            id,
            outcome: attempted ? "Outcome uncertain. Use Inspect saved card." : "Pending original.",
          })),
        };
        attachmentUploads.set(scope, batch);
      }
      uploadProgress.hidden = !batch;
      uploadResults.hidden = !batch;
      if (!batch) {
        inspection.sync();
        return;
      }
      const progress = batch.processed + " of " + batch.results.length + " files processed.";
      if (uploadProgress.textContent !== progress + batch.message) {
        uploadProgress.textContent = progress + batch.message;
      }
      const row = changed === undefined ? undefined : uploadResults.children[changed];
      const result = changed === undefined ? undefined : batch.results[changed];
      if (row && result) {
        row.textContent = result.name + " — " + result.outcome;
      } else {
        uploadResults.replaceChildren();
        for (const { name, outcome } of batch.results) {
          uploadResults.append(element("li", name + " — " + outcome));
        }
        inspection.sync();
      }
    }

    /** @param {boolean} [resuming] */
    function canUpload(resuming = false) {
      return (
        isSelected() &&
        !writing &&
        !uploadingToCard(boardId, card.id) &&
        !hasUnresolvedInspection(scope) &&
        view.dataset.unavailable !== "true" &&
        resuming === (pendingUploads?.has(scope) === true)
      );
    }

    /** @param {ClipboardEvent} event */
    function pasteAttachments(event) {
      if (!canUpload()) return;
      const files = pastedFiles(event).filter(({ type }) => type.startsWith("image/"));
      if (!files.length) return;
      event.preventDefault();
      void uploadFiles(boardId, card.id, cardTag, files);
    }

    let historyRead = 0;
    async function refreshHistory() {
      const read = ++historyRead;
      try {
        const entries = /** @type {Change[]} */ (
          await readWithSession(() =>
            collect(
              (cursor) =>
                api.listCardActivity(
                  boardId,
                  "0001-01-01 00:00:00+00:00",
                  "9999-12-31 23:59:59+00:00",
                  savedCard.ticket,
                  cursor,
                ),
              "activities",
            ),
          )
        );
        if (entries.some((entry) => entry.board_id !== boardId))
          throw new ApiError("invalid_response");
        if (cardView !== activeCard || read !== historyRead || !historyPanel.open) return;
        historyList.replaceChildren(
          ...entries.reverse().map((entry) => {
            const row = element("article");
            const field = entry.resource_type === "relationship" ? null : entry.field;
            const byline = element("small");
            byline.append(
              attribution(entry.actor_id),
              element("span", " · " + entry.at + " · Revision " + entry.revision),
            );
            row.append(
              byline,
              element("p", field === "priority_id" ? "Priority" : (field ?? entry.operation)),
            );
            if (entry.resource_type === "attachment")
              row.append(element("small", "Attachment " + entry.resource_id));
            if (field !== null) {
              for (const [label, value] of [
                ["Previous", entry.before],
                ["Saved", entry.after],
              ]) {
                const shown =
                  entry.field === "priority_id"
                    ? (priorities.find(({ id }) => id === value)?.label ?? value)
                    : value;
                const text = element("p", label + ": " + (shown ?? "—"));
                text.className = "body-text";
                row.append(text);
              }
            }
            return row;
          }),
        );
        if (!entries.length) historyList.textContent = "No card history.";
      } catch {
        if (cardView === activeCard && read === historyRead && historyPanel.open) {
          historyList.textContent =
            "Card history could not be read. Close and reopen History to retry.";
        }
      }
    }
    historyPanel.append(element("summary", "History"), historyList);
    historyPanel.addEventListener("toggle", () => {
      historyList.replaceChildren();
      if (historyPanel.open) {
        historyList.textContent = "Reading card history…";
        void refreshHistory();
      }
    });

    /** @param {Attachment} entry */
    function renderAttachment(entry) {
      const row = element("article");
      row.className = "attachment";
      const file = element("p");
      file.append(
        element("span", entry.original_name + " · " + entry.size + " bytes · "),
        attribution(entry.added_by),
        element("span", " · " + entry.created_at),
      );
      file.className = "body-text";
      row.append(file);
      const note = element("p");
      note.setAttribute("role", "status");
      const generation = attachmentGeneration;
      const current = () =>
        cardView === activeCard && generation === attachmentGeneration && row.isConnected;
      /** @param {"attachment"|"inline"} disposition @param {HTMLButtonElement} control */
      async function readFile(disposition, control) {
        if (control.disabled || !current()) return undefined;
        control.disabled = true;
        note.textContent = "";
        try {
          const blob = await readWithSession(() =>
            api.getAttachmentContent(entry.id, disposition, entry.size, entry.sha2_256),
          );
          return current() ? blob : undefined;
        } catch {
          if (current()) note.textContent = "Attachment could not be read.";
          return undefined;
        } finally {
          control.disabled = false;
        }
      }
      const download = button("Download", () => {
        void readFile("attachment", download).then((blob) => {
          if (!blob || !current()) return;
          const url = URL.createObjectURL(blob);
          const anchor = element("a");
          anchor.href = url;
          anchor.download = downloadName(entry.original_name);
          row.append(anchor);
          anchor.click();
          anchor.remove();
          setTimeout(() => URL.revokeObjectURL(url), 1000);
        });
      });
      download.setAttribute("aria-label", "Download " + entry.original_name);
      row.append(download);
      if (entry.previewable) {
        const image = element("img");
        image.alt = entry.original_name;
        image.hidden = true;
        let url = "";
        function clearPreview() {
          image.hidden = true;
          image.removeAttribute("src");
          preview.textContent = "Preview";
          if (url) {
            URL.revokeObjectURL(url);
            previewUrls.delete(url);
            url = "";
          }
        }
        const preview = button("Preview", () => {
          if (url) {
            clearPreview();
            return;
          }
          void readFile("inline", preview).then((blob) => {
            if (!blob || !current()) return;
            url = URL.createObjectURL(blob);
            previewUrls.add(url);
            image.src = url;
            image.hidden = false;
            preview.textContent = "Hide preview";
          });
        });
        image.addEventListener("error", () => {
          if (!current()) return;
          clearPreview();
          note.textContent = "The raster preview could not be displayed.";
        });
        row.append(preview, image);
        const selected = savedCard.cover_attachment_id === entry.id;
        row.append(
          cardRevisionForm(selected ? "Clear cover" : "Set cover", [], (submit) => {
            if (!current()) return;
            const cover = selected ? null : entry.id;
            void writeResource(
              submit,
              () => api.patchCard(card.id, cardTag, { cover_attachment_id: cover }),
              async () => (await readCurrent()).saved.cover_attachment_id === cover,
              selected ? "Cover cleared." : "Cover set.",
              inspection,
            );
          }),
        );
      }
      const detach = cardRevisionForm("Detach", [], (submit) => {
        if (!current()) return;
        const expected = boardTag();
        void writeResource(
          submit,
          () => api.detachAttachment(entry.id, entry.revision, expected),
          async () => {
            const metadata = await readSubmittedAttachment(boardId, card.id, entry.id);
            const { saved } = await readCurrent();
            return metadata?.active === false && saved.cover_attachment_id !== entry.id;
          },
          "Attachment detached.",
          inspection,
        );
      });
      detach.querySelector("button")?.setAttribute("aria-label", "Detach " + entry.original_name);
      row.append(detach, note);
      return row;
    }

    let relationshipRead = 0;
    /** @type {Map<string, RelationshipEditor>} */
    let relationshipEditors = new Map();
    async function refreshRelationships() {
      const read = ++relationshipRead;
      const links = /** @type {Relationship[]} */ (
        await collect((cursor) => api.listRelationships(boardId, card.id, cursor), "relationships")
      );
      const activeLinks = links.filter(
        (entry) =>
          entry.board_id === boardId &&
          (entry.source_card_id === card.id || entry.target_card_id === card.id),
      );
      const targets = new Map(
        await Promise.all(
          [
            ...new Set(
              activeLinks.map((entry) =>
                entry.source_card_id === card.id ? entry.target_card_id : entry.source_card_id,
              ),
            ),
          ].map(async (id) => {
            const other = /** @type {Card} */ ((await api.getCard(id)).data);
            if (other.board_id !== boardId || other.id !== id)
              throw new ApiError("invalid_response");
            return /** @type {const} */ ([id, other]);
          }),
        ),
      );
      if (cardView !== activeCard || read !== relationshipRead) return activeLinks;
      const focusedId = [...relationshipEditors].find(
        ([, editor]) => editor.type === document.activeElement,
      )?.[0];
      /** @type {Map<string, RelationshipEditor>} */
      const nextEditors = new Map();
      const rows = activeLinks.map((entry) => {
        const other = targets.get(
          entry.source_card_id === card.id ? entry.target_card_id : entry.source_card_id,
        );
        const direction = relationshipDirection(entry, card.id);
        const row = element("article");
        const title = element("p", direction + " ");
        if (other)
          title.append(
            link(ticketHref(boardId, other.ticket), "#" + other.ticket + " " + other.title),
          );
        row.append(title);
        const previous = relationshipEditors.get(entry.id);
        const selected =
          previous && previous.type.value !== previous.savedKind ? previous.type.value : entry.kind;
        const type = select(relationshipChoices, selected);
        const editor = {
          type,
          savedKind: entry.kind,
          expectedRevision:
            selected === entry.kind
              ? entry.revision
              : (previous?.expectedRevision ?? entry.revision),
        };
        nextEditors.set(entry.id, editor);
        const stale = element(
          "p",
          "Saved link changed. Review the saved type and change the selection before saving.",
        );
        stale.hidden = editor.expectedRevision === entry.revision;
        type.addEventListener("change", () => {
          editor.expectedRevision = entry.revision;
          stale.hidden = true;
        });
        const endpoints = [entry.source_card_id, entry.target_card_id];
        row.append(
          form(
            "Change link type",
            [field("Link type (source to target)", type), stale],
            (submit) => {
              const wanted = /** @type {RelationshipKind} */ (type.value);
              if (wanted === entry.kind) return;
              void writeResource(
                submit,
                async () => {
                  requireAttachmentUploadsSettled(boardId, endpoints);
                  await api.replaceRelationship(
                    entry.id,
                    editor.expectedRevision,
                    boardTag(),
                    wanted,
                  );
                },
                async () =>
                  (await readLinks()).some(
                    (value) => value.id === entry.id && value.kind === wanted,
                  ),
                "Link type changed.",
                inspection,
              );
            },
          ),
          form("Remove link", [], (submit) => {
            void writeResource(
              submit,
              async () => {
                requireAttachmentUploadsSettled(boardId, endpoints);
                await api.removeRelationship(entry.id, entry.revision, boardTag());
              },
              async () => !(await readLinks()).some((value) => value.id === entry.id),
              "Link removed.",
              inspection,
            );
          }),
        );
        return row;
      });
      relationshipList.replaceChildren(...rows);
      relationshipEditors = nextEditors;
      if (!activeLinks.length) relationshipList.textContent = "No links.";
      inspection.sync();
      if (focusedId) relationshipEditors.get(focusedId)?.type.focus({ preventScroll: true });
      return activeLinks;
    }

    async function readLinks() {
      const [links] = await Promise.all([refreshRelationships(), readSavedCard(), reloadBoard()]);
      if (cardView === activeCard && historyPanel.open) await refreshHistory();
      return links;
    }
    const linkTicket = ticketInput();
    linkTicket.required = true;
    const linkType = select(relationshipChoices, "relates_to");
    const addLink = form(
      "Add link",
      [field("Target ticket", linkTicket), field("Link type", linkType)],
      (submit) => {
        const id = crypto.randomUUID();
        const targetTicket = linkTicket.valueAsNumber;
        const kind = /** @type {RelationshipKind} */ (linkType.value);
        const expected = boardTag();
        void writeResource(
          submit,
          async (record) => {
            const target = await readWithSession(() => findCardByTicket(boardId, targetTicket));
            if (!target || target.id === card.id || cardView !== activeCard)
              throw new ApiError("invalid_target");
            requireAttachmentUploadsSettled(boardId, [card.id, target.id]);
            record({ kind: "add-link", id });
            await api.createRelationship(id, boardId, expected, card.id, target.id, kind);
          },
          async () => {
            const confirmed = (await readLinks()).some((entry) => entry.id === id);
            if (confirmed && linkTicket.valueAsNumber === targetTicket) linkTicket.value = "";
            return confirmed;
          },
          "Link added.",
          inspection,
        );
      },
    );

    /** @param {Card} saved */
    function canChangeArchive(saved) {
      return !saved.archived || lanes.some(({ id, retired }) => id === saved.lane_id && !retired);
    }
    const archiveForm = cardRevisionForm("Archive card", [], (submit) => {
      if (cardView !== activeCard || !canChangeArchive(savedCard)) return;
      const archived = !savedCard.archived;
      void writeResource(
        submit,
        () => api.patchCard(card.id, cardTag, { archived }),
        async () => (await readCurrent()).saved.archived === archived,
        archived ? "Card archived." : "Card restored.",
        inspection,
      );
    });
    archiveAction.append(archiveForm);
    const approveForm = cardRevisionForm(
      "Reviewed & approved",
      [],
      (submit) => {
        const id = newOperationId();
        const submitWasFocused = submit === document.activeElement;
        let approved = false;
        void writeResource(
          submit,
          async (record) => {
            record({ kind: "approve-card", id });
            await api.approveCard(id, boardId, boardTag(), card.id, cardTag);
            approved = true;
          },
          async () => {
            if (!approved) approved = await readSubmittedApproval(id);
            if (approved) {
              await reloadBoard().catch(() => {});
              closeDrawer(
                view.contains(document.activeElement) || (submitWasFocused && focusWasLost()),
              );
            } else {
              await readCurrent();
            }
            return approved;
          },
          "Reviewed and moved to Completed.",
          inspection,
        );
      },
      true,
    );
    approvalAction.append(approveForm);

    const priorityAction = element("div");
    const priorityResult = element("p");
    priorityResult.setAttribute("aria-live", "polite");
    priority.addEventListener("change", () => {
      priorityResult.textContent = "";
    });
    /** @param {string} requested @param {Labeled|undefined} effective @param {boolean} replaced */
    async function refreshPriority(requested, effective, replaced) {
      try {
        await reloadBoard();
      } catch (error) {
        if (replaced && cardView === activeCard) {
          priorityResult.textContent =
            "Readback failed. Use " + inspection.label + " before making another edit.";
        }
        throw error;
      }
      if (cardView !== activeCard || view.dataset.unavailable === "true") return;
      if (effective) {
        if (priority.value === requested) priority.value = savedCard.priority_id;
        priorityResult.textContent =
          effective.id === requested
            ? "Effective priority: " + effective.label + "."
            : "Dependency floor kept the effective priority at " + effective.label + ".";
      } else if (replaced) {
        priorityResult.textContent =
          "Compare the saved priority with your selection before making another edit.";
      }
    }
    const setPriority = cardRevisionForm(
      "Set priority",
      [field("Priority", priority)],
      (submit) => {
        if (cardView !== activeCard) return;
        const requested = priority.value;
        if (requested === savedCard.priority_id || !priorities.some(({ id }) => id === requested))
          return;
        const expected = cardTag;
        /** @type {PendingCard} */
        const projection = { kind: "patch", card: savedCard, fields: { priority_id: requested } };
        priorityResult.textContent = "";
        /** @type {Labeled | undefined} */
        let effective;
        void writeResource(
          submit,
          async () => {
            showPendingCard(projection, true);
            const reply = await api.patchCard(card.id, expected, { priority_id: requested });
            const saved = /** @type {Card} */ (reply.data);
            effective = priorities.find(({ id }) => id === saved?.priority_id);
            if (saved?.id !== card.id || saved.board_id !== boardId || !effective) {
              effective = undefined;
              throw new ApiError("invalid_response", { outcomeUnknown: true });
            }
          },
          async () => {
            const drawer = activeDrawer;
            const sameCard = drawer?.boardId === boardId && drawer.id === card.id;
            if (sameCard)
              await drawer.refreshPriority(requested, effective, cardView !== activeCard);
            const ticket = new URLSearchParams(location.search).get("ticket");
            if (
              (!sameCard || drawer !== activeDrawer) &&
              boardAtLocation() === boardId &&
              rememberedCard(boardId, ticket) === card.id
            ) {
              // A replacement drawer was still opening or changed during readback.
              await load();
            }
            return effective !== undefined;
          },
          "Priority saved.",
          inspection,
          projection,
        );
      },
    );
    priorityAction.append(setPriority, priorityResult);

    moveAction.append(
      cardRevisionForm("Move card", [field("Move to lane", lane)], (submit) => {
        if (
          isSelected() &&
          !moveAction.hidden &&
          moveBoardTag === boardTag() &&
          moveDestinations(lanes, currentWorkflow(), savedCard.lane_id).some(
            ({ id }) => id === lane.value,
          )
        ) {
          moveCard(savedCard, lane.value, cardTag, submit, reloadBoard, inspection);
        }
      }),
    );

    /** @param {Card} saved */
    function updateActions(saved) {
      const destinations = moveDestinations(lanes, currentWorkflow(), saved.lane_id);
      moveBoardTag = boardTag();
      moveDraft = lane.value || moveDraft;
      if (moveDraft === saved.lane_id) moveDraft = "";
      setOptions(
        lane,
        [{ id: "", label: "Choose a lane" }, ...destinations],
        destinations.some(({ id }) => id === moveDraft) ? moveDraft : "",
      );
      moveAction.hidden = destinations.length === 0;
      archiveAction.hidden = !canChangeArchive(saved);
      const control = archiveForm.querySelector("button");
      if (control) control.textContent = saved.archived ? "Restore card" : "Archive card";
      approvalAction.hidden =
        saved.archived ||
        !activeLanes(lanes).some(
          ({ id, role }) => id === saved.lane_id && role === "ready_for_review",
        );
    }

    const inspect = button(inspection.label, inspection.inspect);
    const uploadForm = cardRevisionForm("Upload attachments", [field("Files", upload)], () => {
      const files = Array.from(upload.files ?? []);
      if (!files.length || !canUpload()) return;
      upload.value = "";
      void uploadFiles(boardId, card.id, cardTag, files);
    });
    const uploadSubmit = uploadForm.querySelector("button");
    if (uploadSubmit) uploadSubmit.dataset.newAttachmentUpload = "true";
    showCurrent(card);
    const saveText = cardRevisionForm(
      "Save text",
      [field("Title", title), field("Description", description)],
      (submit) => {
        if (composingTitle || compositionEnter) return;
        /** @type {PendingFields} */
        const draft = {};
        if (title.value !== titleBaseline) {
          if (!title.value) {
            announce("error", "Enter a title.");
            title.focus();
            return;
          }
          draft.title = title.value;
        }
        if (description.value !== descriptionBaseline) {
          draft.description = editedDescription(
            savedCard.description,
            descriptionBaseline,
            description.value,
          );
        }
        if (Object.keys(draft).length) void save(draft, submit);
      },
      true,
    );
    view.replaceChildren(
      close,
      inspect,
      inspection.recovery,
      heading,
      pendingSummary,
      current,
      archiveAction,
      approvalAction,
      historyPanel,
      saveText,
      moveAction,
      cardRevisionForm(
        "Assign owner",
        [field("Owner", owner)],
        (submit) => void save({ owner_id: owner.value }, submit),
      ),
      priorityAction,
      hierarchy,
      element("h3", "Relationships"),
      relationshipList,
      addLink,
      element("h3", "Comments"),
      commentList,
      cardRevisionForm(
        "Post comment",
        [field("Comment", comment)],
        (submit) => {
          const id = crypto.randomUUID();
          const text = comment.value;
          retainText();
          void writeResource(
            submit,
            (record) => {
              record({ kind: "comment", id });
              return api.createComment(card.id, cardTag, text, id);
            },
            async () => {
              const confirmed = (await readSubmittedComment(id)) !== undefined;
              if (confirmed) clearSubmittedComment(scope, text);
              return confirmed;
            },
            "Comment saved.",
            inspection,
          );
        },
        true,
      ),
      element("h3", "Attachments"),
      attachmentList,
      element(
        "p",
        "Select files or paste a clipboard image into the card. Each file can be up to 10 MiB.",
      ),
      uploadForm,
      uploadRecovery,
      uploadProgress,
      uploadResults,
    );
    inspection.sync();
    showAttachmentUploads();
    if (focusHeading && document.activeElement === initialFocus) heading.focus();
    void readWithSession(refreshRelationships).catch(() => {
      if (cardView === activeCard && !relationshipList.hasChildNodes())
        relationshipList.textContent = "Links could not be read.";
    });
    void readWithSession(() => refreshLists()).catch(() => {
      if (cardView !== activeCard || commentList.hasChildNodes() || attachmentList.hasChildNodes())
        return;
      commentList.textContent = "Comments could not be read.";
      attachmentList.textContent = "Attachments could not be read.";
    });
  } catch (error) {
    if (!isSelected()) return;
    if (invalidSelection || (error instanceof ApiError && error.status === 404)) {
      rememberCard(boardId);
      if (restoring) {
        view.hidden = true;
        view.replaceChildren();
        return;
      }
    } else {
      retrySelectedCard = () => {
        if (isSelected())
          void openCard(
            boardId,
            summary,
            lanes,
            users,
            priorities,
            boardTag,
            reloadBoard,
            currentWorkflow,
            opener,
            presentation,
            true,
          );
      };
    }
    view.replaceChildren(element("p", readError(error)));
  }
}

/** @param {string} boardId @param {unknown} principalId @param {AbortSignal} signal */
async function readBoardContents(boardId, principalId, signal) {
  for (let attempt = 0; attempt < 2; attempt++) {
    signal.throwIfAborted();
    const ordered = await api.getBoardLanes(boardId, signal);
    signal.throwIfAborted();
    const data = /** @type {{board: Board, cards: Card[]}} */ (ordered.data);
    const board = { data: data.board, etag: ordered.etag };
    const cards = data.cards;
    const [lanes, priorities, workflow] = await Promise.all([
      /** @type {Promise<Lane[]>} */ (
        collect((cursor) => api.listLanes(boardId, cursor, signal), "lanes", signal)
      ),
      /** @type {Promise<Labeled[]>} */ (
        collect((cursor) => api.listPriorities(boardId, cursor, signal), "priorities", signal)
      ),
      readWorkflow(board.data, principalId, signal),
    ]);
    signal.throwIfAborted();
    const check = await api.getBoardIfChanged(boardId, etag(board), signal);
    if (check.data !== null) {
      if (check.etag !== board.etag) continue;
      const summary = /** @type {Board} */ (check.data);
      if (validPublishing(summary.publishing)) data.board.publishing = summary.publishing;
    }
    const active = new Set(activeLanes(lanes).map(({ id }) => id));
    if (
      new Set(cards.map(({ id }) => id)).size !== cards.length ||
      cards.some(
        (card) =>
          card.board_id !== boardId ||
          card.archived !== false ||
          !active.has(card.lane_id) ||
          !Number.isSafeInteger(card.revision) ||
          card.revision < 1,
      )
    )
      throw new ApiError("invalid_response");
    return { board, lanes, cards, priorities, workflow };
  }
  throw new ApiError("board_changed_during_read", { status: 412 });
}

/** @param {string[]} left @param {string[]} right */
function sameOrder(left, right) {
  return left.length === right.length && left.every((id, index) => id === right[index]);
}

/** @param {string[]} pins @param {BoardSummary[]} boards */
function availablePins(pins, boards) {
  const visible = new Set(boards.map(({ id }) => id));
  return pins.filter((id) => visible.has(id));
}

/** @param {string[]} pins @param {PinEdit} edit @param {BoardSummary[]} boards */
function editPinOrder(pins, edit, boards) {
  if (edit.action === "remove-unavailable") return availablePins(pins, boards);
  const rest = pins.filter((id) => id !== edit.id);
  if (edit.action === "unpin") return rest;
  if (edit.action === "pin") return pins.includes(edit.id) ? pins : [...pins, edit.id];
  if (!("anchor" in edit) || !pins.includes(edit.id)) return undefined;
  const index = rest.indexOf(edit.anchor);
  if (index < 0) return undefined;
  rest.splice(index + (edit.action === "after" ? 1 : 0), 0, edit.id);
  return rest;
}

/** @param {(pins: string[]) => string[]|undefined} plan @param {WorkspaceReply|undefined} saved
 * @param {() => Promise<WorkspaceReply>} read @param {(reply: WorkspaceReply) => void} show */
async function savePinEdit(plan, saved, read, show) {
  try {
    let latest = saved ?? (await read());
    for (let attempt = 0; attempt < 2; ++attempt) {
      const pins = latest.data.pinned_board_ids;
      const requested = plan(pins);
      if (!requested)
        return "The move is pending. The board or its destination is no longer pinned.";
      if (sameOrder(pins, requested)) return "";
      const submittingCapability = capability;
      try {
        const reply = await api.setPinnedBoards(requested, latest.etag);
        if (!sameOrder(reply.data.pinned_board_ids, requested)) {
          throw new ApiError("invalid_response", { outcomeUnknown: true });
        }
        show(reply);
        return "";
      } catch (error) {
        const phase = writePhase(error, submittingCapability);
        if (phase === "error") return "The pin change is pending. The service refused the save.";
        latest = await read();
        const current = latest.data.pinned_board_ids;
        const intended = plan(current);
        if (intended && sameOrder(current, intended)) return "";
        if (phase === "unknown")
          return "The saved order could not confirm the pin change. Review it before retrying.";
      }
    }
    return "The shared pin order changed again. Review it before retrying the pin change.";
  } catch {
    return "The saved pin order could not be read. The pin change is pending.";
  }
}

/** @param {boolean} canManage @param {() => boolean} isCurrent */
function renderServiceControls(canManage, isCurrent) {
  const panel = element("section");
  panel.id = "service-controls";
  const health = element("p", "Reading service status…");
  const stage = element("p");
  const outcome = element("p");
  outcome.id = "service-operation-status";
  outcome.setAttribute("role", "status");
  let busy = false;
  let inspectAgain = false;
  /** @type {Service|undefined} */
  let service;
  /** @type {string|undefined} */
  let serviceEtag;
  /** @type {Build|undefined} */
  let candidate;
  const activate = button("Activate staged build", () => {
    if (service && serviceEtag && candidate)
      void submit(service, serviceEtag, { id: newOperationId(), buildId: candidate.id });
  });
  const stop = button("Shut down service", () => {
    if (service && serviceEtag) void submit(service, serviceEtag);
  });
  const refreshStatus = button("Refresh service status", () => {
    if (!writing && !busy) void inspect(true);
  });

  function sync() {
    const disabled = busy || pendingLifecycle !== undefined || service?.state !== "running";
    activate.disabled = disabled || candidate === undefined;
    stop.disabled = disabled;
    refreshStatus.disabled = busy;
  }

  /** @param {AbortSignal} [signal] */
  async function readService(signal) {
    try {
      const reply = await readWithSession(() => api.getService(signal), signal);
      const data = reply.data;
      if (!isCurrent()) return;
      service = data;
      serviceEtag = reply.etag;
      health.textContent =
        "Service is " +
        service.state +
        " on port " +
        service.port +
        ". Running build " +
        service.build_id +
        "." +
        (service.restart_error_code
          ? " Last lifecycle error: " + service.restart_error_code + "."
          : "");
    } catch {
      if (!isCurrent()) return;
      service = undefined;
      serviceEtag = undefined;
      health.textContent = "Service status is unavailable.";
    }
    return service;
  }

  async function readStage() {
    if (!canManage || !isCurrent()) return;
    candidate = undefined;
    if (service?.state !== "running") {
      stage.textContent = "";
      return;
    }
    try {
      const { data } = await readWithSession(api.listBuilds);
      if (!isCurrent()) return;
      stage.textContent = data.builds.length
        ? data.builds
            .map(
              (build) =>
                "Staged build " +
                build.version +
                " is " +
                build.validation +
                (build.error_code ? " (" + build.error_code + ")." : "."),
            )
            .join(" ")
        : "No staged build is available.";
      const candidates = data.builds.filter(
        (build) => build.validation === "valid" && build.id !== service?.build_id,
      );
      if (candidates.length === 1) candidate = candidates[0];
    } catch {
      if (isCurrent()) stage.textContent = "Staged build status is unavailable.";
    }
  }

  /** @param {boolean} explicit @param {AbortSignal} [signal] */
  async function readOutcome(explicit, signal) {
    const operation = canManage ? pendingLifecycle : undefined;
    const latest = await readService(signal);
    if (!operation || !isCurrent() || pendingLifecycle !== operation) return;
    let message = operation.refusal;
    if (!message && latest) {
      const prior = operation.prior;
      const changed = latest.service_instance_id !== prior.service_instance_id;
      if (operation.activation) {
        const { id, buildId } = operation.activation;
        try {
          const result = await readWithSession(
            () => api.getActivation(id, buildId, signal),
            signal,
          );
          if (
            result.state === "activated" &&
            changed &&
            latest.build_id === buildId &&
            latest.port === prior.port &&
            latest.state === "running"
          ) {
            message = "Build activated. Reconnected on the same port.";
          } else if (result.state === "failed") {
            message =
              "Build activation failed" +
              (result.error_code ? " (" + result.error_code + ")." : ".");
          }
        } catch (error) {
          if (
            explicit &&
            !operation.acknowledged &&
            latest.state === "running" &&
            error instanceof ApiError &&
            error.status === 404
          ) {
            message = "Activation could not be confirmed. Review the current service status.";
          }
        }
      } else if (changed && latest.state === "running") {
        message = "The service is running on a new instance.";
      } else if (
        latest.state === "running" &&
        latest.restart_error_code &&
        serviceEtag !== operation.priorEtag
      ) {
        message = "Shutdown did not complete. The service is still running.";
      } else if (explicit && latest.state === "running" && !operation.acknowledged) {
        message = "Shutdown could not be confirmed. The service is running.";
      }
    }
    if (!message || !isCurrent() || pendingLifecycle !== operation) return;
    pendingLifecycle = undefined;
    outcome.textContent = message;
  }

  /** @param {boolean} explicit */
  async function inspect(explicit) {
    if (!isCurrent()) return;
    if (busy) {
      inspectAgain = true;
      return;
    }
    busy = true;
    sync();
    try {
      await readOutcome(explicit);
      await readStage();
      if (!isCurrent() || !canManage || !pendingLifecycle) return;
      if (pendingLifecycle.acknowledged && !pendingLifecycle.activation) {
        outcome.textContent = !service
          ? "Shutdown accepted. The service is unreachable."
          : service.state === "draining"
            ? "Shutdown accepted. The service is draining."
            : "Shutdown accepted. Refresh service status to check progress.";
      } else {
        outcome.textContent = "The outcome is unconfirmed. Refresh service status to inspect it.";
      }
    } finally {
      busy = false;
      sync();
      if (inspectAgain && isCurrent()) {
        inspectAgain = false;
        void inspect(false);
      }
    }
  }

  /** @param {Service} prior @param {string} priorEtag @param {{id: string, buildId: string}} [activation] */
  async function submit(prior, priorEtag, activation) {
    if (writing || busy || pendingLifecycle || !isCurrent()) return;
    /** @type {LifecycleOperation} */
    const operation = { prior, priorEtag, activation, acknowledged: false };
    const submittingCapability = capability;
    startWriting();
    busy = true;
    pendingLifecycle = operation;
    sync();
    outcome.textContent = activation ? "Requesting build activation…" : "Requesting shutdown…";
    try {
      try {
        if (activation) {
          await api.activateBuild(activation.id, activation.buildId, priorEtag);
        } else {
          const draining = await api.stopService(prior, priorEtag);
          if (isCurrent() && pendingLifecycle === operation) service = draining;
        }
        if (pendingLifecycle !== operation) return;
        operation.acknowledged = true;
      } catch (error) {
        if (pendingLifecycle !== operation) return;
        const phase =
          error instanceof ApiError && error.status === 401 && capability !== submittingCapability
            ? "error"
            : writePhase(error);
        if (phase !== "unknown") {
          operation.refusal =
            phase === "conflict"
              ? "Service changed. Review the current status."
              : activation
                ? "The service refused build activation."
                : error instanceof ApiError && error.code === "final_publication_failed"
                  ? "Final publication failed. The service remains running."
                  : "The service refused shutdown.";
          if (isCurrent()) {
            await readOutcome(false);
            await readStage();
          }
          return;
        }
      }
      if (!isCurrent()) return;
      if (activation) {
        outcome.textContent = operation.acknowledged
          ? "Activation accepted. Reconnecting…"
          : "Activation outcome is uncertain. Inspecting service status…";
        const signal = AbortSignal.timeout(8000);
        for (let attempt = 0; attempt < 8 && pendingLifecycle === operation; attempt++) {
          if (attempt > 0)
            await new Promise((resolve) => {
              setTimeout(resolve, 750);
            });
          if (signal.aborted || !isCurrent()) break;
          await readOutcome(false, signal);
        }
        if (isCurrent() && pendingLifecycle === operation) {
          outcome.textContent =
            "Activation outcome is unconfirmed. Refresh service status to inspect it.";
        }
        await readStage();
      } else if (operation.acknowledged) {
        health.textContent = "Service is draining on port " + prior.port + ".";
        stage.textContent = "";
        outcome.textContent = "Shutdown accepted. The service is draining.";
      } else {
        await readOutcome(false);
        if (isCurrent() && pendingLifecycle === operation) {
          outcome.textContent =
            "Shutdown outcome is uncertain. Refresh service status to inspect it.";
        }
      }
    } finally {
      busy = false;
      finishWriting();
      sync();
      if (!isCurrent()) void refreshActiveService?.();
    }
  }

  panel.append(element("h2", "Service"), health);
  if (canManage) panel.append(stage, outcome);
  panel.append(refreshStatus);
  if (canManage) panel.append(activate, stop);
  sync();
  return { panel, inspect: () => inspect(false) };
}

function boardAtLocation() {
  return location.pathname.match(/^\/boards\/([0-9a-f-]{36})\/?$/i)?.[1]?.toLowerCase();
}

/** @param {Pick<Board, "id"|"policy_id">} board @param {unknown} principalId
 * @param {AbortSignal} signal @returns {Promise<Workflow|null>} */
async function readWorkflow(board, principalId, signal) {
  if (typeof principalId !== "string") return null;
  try {
    const reply = await api.getPolicy(board.policy_id, signal);
    const policy = /** @type {{id?: unknown, board_id?: unknown,
      resource_grants?: {user_id?: unknown, capabilities?: unknown}[],
      create_grants?: {user_id?: unknown, lane_id?: unknown}[],
      move_grants?: {user_id?: unknown, from_lane_id?: unknown, to_lane_id?: unknown}[]}} */ (
      reply.data
    );
    if (
      policy.id !== board.policy_id ||
      policy.board_id !== board.id ||
      !Array.isArray(policy.resource_grants) ||
      !Array.isArray(policy.create_grants) ||
      !Array.isArray(policy.move_grants)
    )
      return null;
    const granted = policy.resource_grants.find(
      (grant) => grant?.user_id === principalId,
    )?.capabilities;
    const capabilities = Array.isArray(granted) ? /** @type {unknown[]} */ (granted) : [];
    /** @type {Set<string>} */
    const create = new Set();
    if (capabilities.includes("create_card")) {
      for (const grant of policy.create_grants) {
        if (grant?.user_id === principalId && typeof grant.lane_id === "string")
          create.add(grant.lane_id);
      }
    }
    /** @type {Map<string, Set<string>>} */
    const move = new Map();
    for (const grant of policy.move_grants) {
      if (
        grant?.user_id !== principalId ||
        typeof grant.from_lane_id !== "string" ||
        typeof grant.to_lane_id !== "string"
      )
        continue;
      const destinations = move.get(grant.from_lane_id) ?? new Set();
      destinations.add(grant.to_lane_id);
      move.set(grant.from_lane_id, destinations);
    }
    return {
      actor: principalId,
      create,
      move,
      canClose: capabilities.includes("close_board"),
      canManageRelationships: capabilities.includes("manage_relationships"),
    };
  } catch (error) {
    signal.throwIfAborted();
    if (error instanceof ApiError && error.status === 401) throw error;
    return null;
  }
}

/** @type {readonly {key: keyof Attention, label: string}[]} */
const attentionColumns = [
  { key: "blocked", label: "Blocked" },
  { key: "needs_input", label: "Needs Terry" },
  { key: "ready_for_review", label: "Ready for Review" },
];

const startupBoardError = "The service could not load this board’s saved snapshot at startup.";

/** @param {BoardSummary} board */
function failedAtStartup(board) {
  return board.revision === null && board.error_code === "durable_state_unavailable";
}

/** @param {BoardSummary} board */
function boardSummaryName(board) {
  if (failedAtStartup(board)) return "Unavailable board (" + board.id.slice(0, 8) + ")";
  return board.name ?? "Unavailable board";
}

/** @param {BoardSummary} board @param {number} now */
function attentionRecency(board, now) {
  if (failedAtStartup(board)) return startupBoardError;
  if (board.last_card_edit_at) {
    const editedAt = Date.parse(board.last_card_edit_at.replace(" ", "T"));
    if (Number.isFinite(editedAt)) {
      const minutes = Math.max(0, Math.floor((now - editedAt) / 60_000));
      return "Last card edit " + minutes + (minutes === 1 ? " minute ago" : " minutes ago");
    }
  }
  return board.attention ? "No recorded card edits" : "Card edit history unavailable";
}

/** @param {HTMLElement} sidebar @param {BoardSummary[]} rows @param {string[]} pins
 * @param {string|undefined|null} selected
 * @param {PinControls} [pinControls] */
function renderAttention(sidebar, rows, pins, selected, pinControls) {
  const byId = new Map(rows.map((board) => [board.id, board]));
  const pinIds = new Set(pins);
  const header = element("div");
  header.className = "attention-grid attention-header";
  header.append(element("h2", "Attention"));
  for (const { key, label } of attentionColumns) {
    const heading = element("span", label);
    heading.className = "attention-label";
    heading.dataset.attention = key;
    heading.setAttribute("aria-hidden", "true");
    header.append(heading);
  }

  const focused = [...sidebar.querySelectorAll("a"), ...sidebar.querySelectorAll("button")].find(
    (item) => item === document.activeElement && item.dataset.boardId,
  );
  const pinned = pins.flatMap((id) => byId.get(id) ?? []);
  const groups = [
    { label: "Pinned boards", boards: pinned, pinned: true },
    { label: "Other boards", boards: rows.filter(({ id }) => !pinIds.has(id)), pinned: false },
  ];
  const sections = groups.map(({ label, boards, pinned: isPinned }) => {
    /** @type {Map<HTMLElement, string>} */
    const entries = new Map();
    const order = boards.map(({ id }) => id);
    const section = element("section");
    section.className = "attention-group";
    section.append(element("h3", label));
    for (const board of boards) {
      const name = boardSummaryName(board);
      const item = link("/boards/" + board.id, "");
      item.className = "attention-grid attention-row";
      item.dataset.boardId = board.id;
      if (board.id === selected) item.setAttribute("aria-current", "page");
      const title = element("span", name);
      title.className = "attention-name";
      item.append(title);
      const accessible = [];
      for (const { key, label } of attentionColumns) {
        const count = board.attention?.[key];
        const slot = element("span");
        slot.className = "attention-slot";
        slot.setAttribute("aria-hidden", "true");
        slot.dataset.attention = key;
        if (count == null) {
          slot.textContent = "—";
        } else if (count > 0) {
          const badge = element("span", count > 99 ? "99+" : String(count));
          badge.className = "attention-count";
          slot.append(badge);
        }
        accessible.push(label + " " + (count ?? "unavailable"));
        item.append(slot);
      }
      const summary = element("span");
      summary.hidden = true;
      summary.id = "attention-" + board.id;
      item.setAttribute("aria-describedby", summary.id);
      item.append(summary);
      const countSummary = accessible.join(". ");
      const availability =
        board.availability === "closed"
          ? "Board closed. "
          : board.availability === "unhealthy"
            ? "Board unhealthy. "
            : "";
      const updateRecency = () => {
        item.title = availability + attentionRecency(board, Date.now());
        summary.textContent = countSummary + ". " + item.title;
      };
      updateRecency();
      item.addEventListener("pointerenter", updateRecency);
      item.addEventListener("focus", updateRecency);
      const entry = element("div");
      entry.className = "attention-entry";
      entry.append(item);
      entries.set(entry, board.id);
      if (pinControls) {
        const actions = element("div");
        actions.className = "attention-pin-actions";
        const action = isPinned ? "unpin" : "pin";
        const label = isPinned ? "Unpin" : "Pin";
        const toggle = button(label, () => {
          if (!pinControls.disabled) pinControls.change({ id: board.id, action });
        });
        toggle.setAttribute("aria-label", label + " " + name);
        toggle.dataset.boardId = board.id;
        toggle.dataset.pinControl = "toggle";
        actions.append(toggle);
        if (isPinned && order.length > 1) {
          actions.append(attentionReorder(board.id, name, order, entries, pinControls));
        }
        if (pinControls.disabled) {
          for (const control of actions.querySelectorAll("button"))
            control.setAttribute("aria-disabled", "true");
        }
        entry.append(actions);
      }
      section.append(entry);
    }
    return section;
  });
  sidebar.replaceChildren(header, ...sections);
  if (focused) {
    const focusId = focused.dataset.boardId;
    const action = [...sidebar.querySelectorAll("button")].find(
      (control) =>
        control.dataset.boardId === focusId &&
        control.dataset.pinControl === focused.dataset.pinControl,
    );
    const destination =
      action ?? [...sidebar.querySelectorAll("a")].find((item) => item.dataset.boardId === focusId);
    destination?.focus({ preventScroll: true });
  }
}

/** @param {string} id @param {string} name @param {string[]} order @param {Map<HTMLElement, string>} entries
 * @param {PinControls} controls */
function attentionReorder(id, name, order, entries, controls) {
  const grip = element("button", "⋮⋮");
  grip.type = "button";
  grip.className = "attention-pin-grip";
  grip.dataset.boardId = id;
  grip.dataset.pinControl = "reorder";
  grip.title = "Drag or use the Up and Down arrows to reorder " + name;
  grip.setAttribute("aria-label", "Reorder " + name);
  grip.setAttribute("aria-keyshortcuts", "ArrowUp ArrowDown");
  const index = order.indexOf(id);
  /** @param {string} anchor @returns {PinEdit} */
  function move(anchor) {
    return { id, action: order.indexOf(anchor) < index ? "before" : "after", anchor };
  }
  grip.addEventListener("keydown", (event) => {
    if (controls.disabled || (event.key !== "ArrowUp" && event.key !== "ArrowDown")) return;
    event.preventDefault();
    const anchor = order[index + (event.key === "ArrowUp" ? -1 : 1)];
    if (anchor) controls.change(move(anchor));
  });

  /** @type {number|undefined} */
  let pointer;
  /** @type {HTMLElement|undefined} */
  let target;
  grip.addEventListener("pointerdown", (event) => {
    if (
      controls.disabled ||
      pointer !== undefined ||
      !event.isPrimary ||
      event.button !== 0 ||
      !controls.beginDrag()
    )
      return;
    try {
      grip.setPointerCapture(event.pointerId);
    } catch {
      controls.endDrag();
      return;
    }
    pointer = event.pointerId;
    event.preventDefault();
    grip.focus({ preventScroll: true });
  });
  /** @param {PointerEvent} event */
  function updateTarget(event) {
    if (event.pointerId !== pointer) return;
    target?.removeAttribute("data-pin-drop");
    const hit = document.elementFromPoint(event.clientX, event.clientY);
    target = [...entries].find(([entry, boardId]) => boardId !== id && entry.contains(hit))?.[0];
    target?.setAttribute("data-pin-drop", "true");
  }
  function clearPointer() {
    pointer = undefined;
    target?.removeAttribute("data-pin-drop");
    target = undefined;
  }
  grip.addEventListener("pointermove", updateTarget);
  grip.addEventListener("pointerup", (event) => {
    if (event.pointerId !== pointer) return;
    updateTarget(event);
    const anchor = target && entries.get(target);
    clearPointer();
    controls.endDrag(anchor ? move(anchor) : undefined);
  });
  /** @param {PointerEvent} event */
  function cancelPointer(event) {
    if (event.pointerId !== pointer) return;
    clearPointer();
    controls.endDrag();
  }
  grip.addEventListener("pointercancel", cancelPointer);
  grip.addEventListener("lostpointercapture", cancelPointer);
  return grip;
}

/** @param {string} boardId @param {AbortSignal} signal @param {() => number} revision
 * @param {(automatic: boolean) => Promise<boolean>} read */
function boardUpdates(boardId, signal, revision, read) {
  const panel = element("div");
  const connection = element("p", "Connecting live updates…");
  connection.id = "board-live-connection";
  connection.setAttribute("role", "status");
  const readiness = element("p");
  readiness.id = "board-live-readiness";
  readiness.setAttribute("role", "status");
  const retry = button("Retry board update", () => {
    if (!writing) void updates.request().catch(() => {});
  });
  retry.hidden = true;
  panel.append(connection, readiness, retry);

  /** @type {import("./board-updates.mjs").Connection} */
  let connectionState = "connecting";
  /** @type {import("./board-updates.mjs").Readiness} */
  let readState = "ready";
  const messages = {
    connecting: "Connecting live updates…",
    connected: "Live updates connected.",
    disconnected: "Live updates disconnected. Reconnecting…",
    unavailable: "Live updates for this board are unavailable.",
  };

  function show() {
    const connectionText = messages[connectionState];
    const readinessText =
      readState === "error"
        ? "Board update could not be completed."
        : readState === "refreshing"
          ? "Checking for board updates…"
          : "";
    if (connection.textContent !== connectionText) connection.textContent = connectionText;
    if (readiness.textContent !== readinessText) readiness.textContent = readinessText;
    retry.hidden =
      connectionState !== "disconnected" &&
      connectionState !== "unavailable" &&
      readState !== "error";
    retry.disabled = readState === "refreshing";
  }

  const updates = createBoardRefresh({
    signal,
    read,
    revision,
    canReadAutomatically: () => !writing,
    report: (state) => {
      readState = state;
      show();
    },
  });
  return {
    panel,
    request: updates.request,
    /** @param {(board: BoardWatch) => void} subscribe @param {boolean} [revalidate] */
    start(subscribe, revalidate = false) {
      activeBoardRefresh = updates;
      const firstRefresh = revalidate ? updates.request().catch(() => {}) : undefined;
      show();
      subscribe({
        id: boardId,
        invalidate: updates.invalidate,
        report: (state) => {
          connectionState = state;
          show();
        },
      });
      return firstRefresh;
    },
  };
}

/** @param {BoardAvailability} [savedAvailability] */
async function load(savedAvailability) {
  if (!identity || !navigation || !main || !details) return;
  retainBoardView?.();
  retainBoardView = undefined;
  retainCardDraft?.();
  retainBoardDraft?.();
  releaseCardMedia?.();
  releaseBoardMedia?.();
  releaseCardMedia = undefined;
  releaseBoardMedia = undefined;
  retainCardDraft = undefined;
  retainBoardDraft = undefined;
  retainNewCardDraft = undefined;
  activeNewCardText = undefined;
  activeNewCardFiles = undefined;
  activeBackupControls = undefined;
  activePendingBoard = undefined;
  cardInspection = undefined;
  boardInspection = undefined;
  retrySelectedCard = undefined;
  activeComment = undefined;
  activeDrawer = undefined;
  const view = ++activeView;
  activeBoardLoad?.abort();
  activeBoardRefresh = undefined;
  activeBoardLoad = new AbortController();
  const signal = activeBoardLoad.signal;
  const selectedLoad = new AbortController();
  const boardSignal = AbortSignal.any([signal, selectedLoad.signal]);
  refreshActiveService = undefined;
  ++activeCard;
  details.hidden = true;
  details.replaceChildren();
  main.replaceChildren();
  boardStyle.replaceSync("");
  const boardId = boardAtLocation();
  const durableHealth = element("p");
  durableHealth.id = "board-durable-health";
  durableHealth.setAttribute("role", "status");
  durableHealth.hidden = true;
  let catalogHint = false;
  let revalidatingCatalog = false;
  let navigationReady = false;
  let startupDiagnostic = false;
  let warmNavigationFailed = false;
  /** @type {Promise<void>|undefined} */
  let warmNavigationRead;
  navigation.querySelector("#warm-attention-error")?.remove();
  const warmAttentionError = element("div");
  warmAttentionError.id = "warm-attention-error";
  const warmAttentionStatus = element(
    "p",
    "Attention could not be refreshed. Showing the last saved counts.",
  );
  warmAttentionStatus.setAttribute("role", "status");
  warmAttentionError.append(warmAttentionStatus, button("Retry Attention", invalidateCatalog));
  let refreshAttention = () => {
    if (signal.aborted) return Promise.resolve();
    if (warmNavigationFailed) return loadWarmNavigation(true);
    catalogHint = true;
    return warmNavigationRead ?? Promise.resolve();
  };
  announce("loading", "Loading…");
  /** @type {{board?: BoardWatch, controller: AbortController}|undefined} */
  let watch;

  function invalidateCatalog() {
    void refreshAttention().catch(() => {});
  }

  /** @param {BoardWatch} [board] */
  function subscribe(board) {
    if (signal.aborted || (watch && watch.board === board)) return;
    watch?.controller.abort();
    const controller = new AbortController();
    watch = { board, controller };
    void watchUpdates({
      board,
      signal: AbortSignal.any([signal, controller.signal]),
      invalidateCatalog,
      connect: (watchedBoardId, streamSignal, receive) =>
        readWithSession(async () => {
          for await (const event of api.streamEvents({
            boardId: watchedBoardId,
            signal: streamSignal,
          }))
            receive(event);
        }, streamSignal),
    });
  }
  /** @param {boolean} [refreshing] @param {AbortSignal} [readSignal] */
  async function readCatalog(refreshing = false, readSignal = signal) {
    readSignal = AbortSignal.any([readSignal, AbortSignal.timeout(CATALOG_READ_TIMEOUT_MS)]);
    let continuation = false;
    const read = () =>
      /** @type {Promise<BoardSummary[]>} */ (
        collect(
          (cursor) => {
            continuation = cursor !== undefined;
            return api.listBoards(cursor, readSignal);
          },
          "boards",
          readSignal,
        )
      );
    try {
      return await readWithSession(read, readSignal);
    } catch (error) {
      readSignal.throwIfAborted();
      if (!refreshing || !(error instanceof ApiError)) throw error;
      // Catalog cursors are tied to the full row set. Start over on a refusal.
      const staleCursor = continuation && error.status === 400;
      const temporary = error.code === "transport_failed" || (error.status ?? 0) >= 500;
      if (!staleCursor && !temporary) throw error;
      if (temporary) await waitForRetry(readSignal, CATALOG_RETRY_MS);
      return readWithSession(read, readSignal);
    }
  }

  /** @param {boolean} [retrying] */
  function loadWarmNavigation(retrying = false) {
    if (warmNavigationRead) return warmNavigationRead;
    warmNavigationFailed = false;
    warmNavigationRead = loadNavigation(retrying)
      .then(
        () => {},
        () => {
          if (signal.aborted) return;
          warmNavigationFailed = true;
          if (!warmAttentionError.isConnected) navigation?.append(warmAttentionError);
        },
      )
      .finally(() => {
        warmNavigationRead = undefined;
        if (warmNavigationFailed && catalogHint) {
          catalogHint = false;
          invalidateCatalog();
        }
      });
    return warmNavigationRead;
  }

  try {
    const retained =
      boardId && !savedAvailability && !writing ? readRecentBoard(boardId) : undefined;
    if (boardId && retained) {
      for (const link of navigation.querySelectorAll("a")) {
        if (link.dataset.boardId === boardId) link.setAttribute("aria-current", "page");
        else link.removeAttribute("aria-current");
      }
      void loadWarmNavigation();
      void revalidateCatalog();
      await paintOpenBoard(boardId, retained);
      return;
    }
    const context = await loadNavigation();
    if (!context || view !== activeView || boardSignal.aborted) return;
    if (!boardId) {
      if (location.pathname !== "/") throw new ApiError("http_error", { status: 404 });
      return;
    }
    const result = await readBoard(boardId, true, savedAvailability);
    if (view !== activeView || boardSignal.aborted) return;
    if (result.contents) {
      await paintOpenBoard(boardId, boardDisplay(result.contents[0]), result.contents);
    } else {
      await paintUnavailableBoard(boardId, result.availability, context.principal_id);
    }
  } catch (error) {
    failBoard(error);
  }

  /** @param {unknown} error */
  function failBoard(error) {
    if (view !== activeView || startupDiagnostic) return;
    if (error instanceof ApiError && error.status === 401) capability = "";
    if (boardId && definitivelyUnavailable(error)) forgetRecentBoard(boardId);
    clearSelectedBoard();
    if (boardId) main?.append(button("Retry board update", () => void load()));
    announce("error", readError(error));
  }

  function clearSelectedBoard() {
    selectedLoad.abort();
    if (watch || navigationReady) subscribe();
    activeBoardRefresh = undefined;
    boardInspection = undefined;
    cardInspection = undefined;
    activeDrawer = undefined;
    activeComment = undefined;
    retrySelectedCard = undefined;
    retainBoardView?.();
    retainBoardDraft?.();
    retainCardDraft?.();
    releaseBoardMedia?.();
    releaseCardMedia?.();
    retainBoardView = undefined;
    retainBoardDraft = undefined;
    retainCardDraft = undefined;
    retainNewCardDraft = undefined;
    activeNewCardText = undefined;
    activeNewCardFiles = undefined;
    activeBackupControls = undefined;
    releaseBoardMedia = undefined;
    releaseCardMedia = undefined;
    ++activeCard;
    details?.replaceChildren();
    if (details) details.hidden = true;
    main?.replaceChildren();
    boardStyle.replaceSync("");
  }

  /** @param {BoardSummary[]} rows */
  function applyStartupFailures(rows) {
    if (signal.aborted || view !== activeView) return;
    /** @type {BoardSummary|undefined} */
    let failed;
    for (const row of rows) {
      if (!failedAtStartup(row)) continue;
      forgetRecentBoard(row.id);
      if (row.id === boardId) failed = row;
    }
    if (!failed || startupDiagnostic) return;
    startupDiagnostic = true;
    clearSelectedBoard();
    main?.replaceChildren(
      element("h1", boardSummaryName(failed)),
      element("p", startupBoardError),
      element("p", "Board ID " + failed.id),
      button("Retry board update", () => void load()),
    );
    announce("error", startupBoardError);
  }

  /** @param {BoardSummary[]} rows */
  function applyCatalogHealth(rows) {
    applyStartupFailures(rows);
    if (!boardId || startupDiagnostic) return;
    const row = rows.find(({ id }) => id === boardId);
    if (!row) return;
    // Focused board reads still serve the accepted state and its recovery controls.
    let message = "";
    if (row.availability === "unhealthy") {
      const problem =
        row.error_code === "durable_state_changed"
          ? "The saved snapshot differs from the last accepted state."
          : "The saved snapshot is unavailable.";
      message = problem + " Showing previously loaded board data.";
    }
    if (durableHealth.textContent !== message) durableHealth.textContent = message;
    durableHealth.hidden = !message;
  }

  async function revalidateCatalog() {
    if (revalidatingCatalog) return;
    revalidatingCatalog = true;
    // External snapshot changes do not wake the event stream. Wait after each
    // read so a slow refresh cannot build up a backlog of scheduled requests.
    while (!signal.aborted) {
      await waitForRetry(signal, CATALOG_REVALIDATE_MS);
      if (!signal.aborted) await refreshAttention().catch(() => {});
    }
  }

  /** @param {boolean} [retrying] */
  async function loadNavigation(retrying = false) {
    if (!identity || !navigation || !main) return;
    const group = new AbortController();
    const readSignal = AbortSignal.any([signal, group.signal]);
    const [session, boards] = await Promise.all([
      readWithSession(() => api.getSession(readSignal), readSignal),
      readCatalog(retrying, readSignal),
    ]).finally(() => group.abort());
    if (signal.aborted || view !== activeView) return;
    const context =
      /** @type {{kind?: unknown, principal_id?: unknown, workspace_admin?: unknown}} */ (
        session.data
      );
    if (context.kind !== "human") throw new ApiError("invalid_response");
    identity.textContent = "Human";
    /** @type {string[]} */
    let visiblePins = [];
    let latestBoards = boards;
    let latestCatalog = JSON.stringify(boards);
    let catalogTail = Promise.resolve();
    /** @type {Promise<void>|undefined} */
    let queuedCatalog;
    /** @type {HTMLParagraphElement|undefined} */
    let workspacePrompt;
    const canEditPins = context.workspace_admin === true;
    /** @type {WorkspaceReply|undefined} */
    let savedWorkspace;
    /** @type {((reply: WorkspaceReply) => void)|undefined} */
    let showPublishing;
    let workspaceUnavailable = false;
    let pinDragging = false;
    const pinFeedback = element("div");
    const pinStatus = element("p");
    pinStatus.setAttribute("role", "status");
    const retryPins = button("Retry pin change", () => runPinAction(pendingPinEdit));
    const removeUnavailable = button("Remove unavailable pins", () =>
      runPinAction({ action: "remove-unavailable" }),
    );
    const cancelPins = button("Cancel pending pin change", () => {
      if (view !== activeView || signal.aborted || pinWrite || pinDragging) return;
      pendingPinEdit = undefined;
      pinMessage = "";
      showBoards(latestBoards);
    });
    pinFeedback.append(pinStatus, removeUnavailable, retryPins, cancelPins);
    const attentionStatus = element(
      "p",
      "Attention could not be refreshed. Showing the last saved counts.",
    );
    attentionStatus.setAttribute("role", "status");
    attentionStatus.hidden = true;
    /** @param {BoardSummary[]} rows */
    function showBoards(rows) {
      if (!navigation || signal.aborted || view !== activeView) return;
      const count = rows.reduce(
        (total, { attention }) =>
          total +
          (attention ? attention.blocked + attention.needs_input + attention.ready_for_review : 0),
        0,
      );
      document.title = count > 0 ? "(" + count + ") " + pageTitle : pageTitle;
      if (workspacePrompt)
        workspacePrompt.textContent = rows.length ? "Select a board." : "No boards are available.";
      // Replacing a captured grip would end the drag. Release or cancel paints the latest rows.
      if (pinDragging) return;

      const focused = document.activeElement;
      const missingPins = visiblePins.length - availablePins(visiblePins, rows).length;
      renderAttention(
        navigation,
        rows,
        visiblePins,
        boardId,
        canEditPins
          ? {
              disabled: !savedWorkspace || Boolean(pinWrite || pendingPinEdit || missingPins),
              change: (edit) => runPinAction(edit, savedWorkspace),
              beginDrag() {
                if (signal.aborted || view !== activeView || pinDragging || pinWrite) return false;
                pinDragging = true;
                return true;
              },
              endDrag(edit) {
                pinDragging = false;
                if (edit) runPinAction(edit, savedWorkspace);
                else showBoards(latestBoards);
              },
            }
          : undefined,
      );
      navigation.append(attentionStatus);
      if (canEditPins) {
        const missing =
          missingPins === 1
            ? "1 saved pin is unavailable."
            : missingPins + " saved pins are unavailable.";
        pinStatus.textContent =
          pinMessage ||
          (workspaceUnavailable
            ? "Saved pin order could not be read."
            : missingPins
              ? missing
              : "");
        retryPins.textContent = pendingPinEdit ? "Retry pin change" : "Read pin order";
        const busy = Boolean(pinWrite);
        retryPins.hidden = busy ? focused !== retryPins : !pendingPinEdit && !workspaceUnavailable;
        cancelPins.hidden = busy || !pendingPinEdit;
        removeUnavailable.hidden = busy
          ? focused !== removeUnavailable
          : Boolean(pendingPinEdit) || !missingPins;
        for (const control of [retryPins, removeUnavailable]) {
          if (busy) control.setAttribute("aria-disabled", "true");
          else control.removeAttribute("aria-disabled");
        }
        pinFeedback.hidden = !pinStatus.textContent;
        navigation.append(pinFeedback);
        if (focused instanceof HTMLButtonElement && pinFeedback.contains(focused)) {
          const links = [...navigation.querySelectorAll("a")];
          const destination =
            [focused, retryPins, removeUnavailable].find((control) => !control.hidden) ??
            links.find((link) => link.dataset.boardId === boardId) ??
            links[0] ??
            toggleSidebar;
          destination?.focus({ preventScroll: true });
        }
      }
    }

    /** @param {WorkspaceReply} reply */
    function showWorkspace(reply) {
      if (view !== activeView) return;
      // A delayed read from this service must not replace a newer accepted write.
      const service = reply.etag.replace(/-\d+"$/, '"');
      if (
        savedWorkspace?.etag.replace(/-\d+"$/, '"') === service &&
        savedWorkspace.data.revision > reply.data.revision
      )
        return;
      savedWorkspace = reply;
      workspaceUnavailable = false;
      visiblePins = reply.data.pinned_board_ids;
      showBoards(latestBoards);
      showPublishing?.(reply);
    }
    async function readWorkspace() {
      const reply = await readWithSession(api.getWorkspace);
      showWorkspace(reply);
      return savedWorkspace ?? reply;
    }

    /** @param {PinEdit} [edit] @param {WorkspaceReply} [saved] */
    function runPinAction(edit, saved) {
      if (view !== activeView || signal.aborted || !canEditPins || pinWrite || pinDragging) return;
      if (edit) pendingPinEdit = edit;
      const removing = edit?.action === "remove-unavailable";
      pinMessage = edit ? "Saving pin order…" : "Reading pin order…";
      async function readPinWorkspace() {
        const reply = await readWorkspace();
        if (removing) {
          // A newly pinned board may first appear in the catalog after the previous read.
          signal.throwIfAborted();
          await refreshAttention();
          signal.throwIfAborted();
        }
        return reply;
      }
      const operation = edit
        ? savePinEdit(
            (pins) => editPinOrder(pins, edit, latestBoards),
            removing ? undefined : saved,
            readPinWorkspace,
            showWorkspace,
          )
        : readWorkspace().then(
            () => "",
            () => "Saved pin order could not be read.",
          );
      pinWrite = operation
        .then((message) => {
          pinMessage = message;
          if (!message) pendingPinEdit = undefined;
        })
        .finally(() => {
          pinWrite = undefined;
          if (view === activeView) showBoards(latestBoards);
        });
      showBoards(latestBoards);
    }

    refreshAttention = () => {
      if (signal.aborted) return Promise.resolve();
      if (queuedCatalog) return queuedCatalog;
      queuedCatalog = catalogTail.then(async () => {
        queuedCatalog = undefined;
        if (signal.aborted) return;
        try {
          const rows = await readCatalog(true);
          if (signal.aborted) return;
          attentionStatus.hidden = true;
          const catalog = JSON.stringify(rows);
          if (catalog === latestCatalog) return;
          latestCatalog = catalog;
          latestBoards = rows;
          applyCatalogHealth(latestBoards);
          showBoards(latestBoards);
        } catch (error) {
          if (signal.aborted) return;
          attentionStatus.hidden = false;
          throw error;
        }
      });
      catalogTail = queuedCatalog.catch(() => {});
      return queuedCatalog;
    };
    navigationReady = true;
    applyCatalogHealth(latestBoards);
    showBoards(latestBoards);
    void revalidateCatalog();
    if (catalogHint) {
      catalogHint = false;
      invalidateCatalog();
    }
    const readPreferences = canEditPins
      ? Promise.resolve(pinWrite)
          .then(() => (view === activeView ? readWorkspace() : undefined))
          .catch(() => {
            if (view === activeView) {
              workspaceUnavailable = true;
              showBoards(latestBoards);
            }
            return undefined;
          })
      : undefined;
    if (!boardId && location.pathname === "/") {
      workspacePrompt = element(
        "p",
        latestBoards.length ? "Select a board." : "No boards are available.",
      );
      main.replaceChildren(element("h1", "Workspace"), workspacePrompt);
      subscribe();
      if (context.workspace_admin === true) {
        const preferences = await readPreferences;
        if (view !== activeView) return;
        const boardName = element("input");
        boardName.required = true;
        boardName.maxLength = 256;
        boardName.value = boardDrafts.get("workspace")?.[0] ?? "";
        const inspection = createInspection(
          main,
          () => view === activeView,
          async () => {
            await refreshAttention();
            if (view !== activeView) return;
            await readWorkspace();
          },
          "workspace",
          "workspace",
        );
        const publishing = renderPublishing(
          "workspace",
          "workspace",
          preferences && { data: preferences.data.publishing, etag: preferences.etag },
          async () => {
            const reply = await readWorkspace();
            return { data: reply.data.publishing, etag: reply.etag };
          },
          api.configureWorkspacePublishing,
          async () => {},
          inspection,
        );
        showPublishing = (reply) =>
          publishing.show({ data: reply.data.publishing, etag: reply.etag });
        const workspaceSettings = element("section");
        workspaceSettings.className = "editor";
        workspaceSettings.append(publishing.panel);
        retainBoardDraft = () => {
          boardDrafts.set("workspace", [boardName.value]);
          publishing.retain();
        };
        boardInspection = inspection;
        main.append(
          button(inspection.label, inspection.inspect),
          form("Create board", [field("Board name", boardName)], (submit) => {
            const id = crypto.randomUUID();
            const name = boardName.value;
            void writeResource(
              submit,
              () => api.createBoard({ id, name }),
              async () => {
                try {
                  await api.getBoard(id);
                } catch (error) {
                  if (error instanceof ApiError && error.status === 404) return false;
                  throw error;
                }
                if (view !== activeView) return true;
                if (boardName.value === name) boardName.value = "";
                history.pushState(null, "", "/boards/" + id);
                await load();
                return true;
              },
              "Board created.",
              inspection,
            );
          }),
          workspaceSettings,
        );
        inspection.sync();
      }
      const controls = renderServiceControls(
        context.workspace_admin === true,
        () => view === activeView,
      );
      main.append(renderRecentBoardSettings(), controls.panel);
      refreshActiveService = controls.inspect;
      void refreshActiveService();
      announce("ready", "Workspace loaded.");
      return context;
    }
    return context;
  }

  /** @param {string} boardId */
  function readContents(boardId) {
    return readWithSession(async () => {
      const group = new AbortController();
      const readSignal = AbortSignal.any([boardSignal, group.signal]);
      try {
        const session = await api.getSession(readSignal);
        const principal = /** @type {{kind?: unknown, principal_id?: unknown}} */ (session.data);
        if (principal.kind !== "human") throw new ApiError("invalid_response");
        return await Promise.all([
          readBoardContents(boardId, principal.principal_id, readSignal),
          /** @type {Promise<User[]>} */ (
            collect((cursor) => api.listUsers(boardId, cursor, readSignal), "users", readSignal)
          ),
        ]);
      } finally {
        group.abort();
      }
    }, boardSignal);
  }

  /** @param {string} boardId @param {boolean} checkAvailability @param {BoardAvailability} [saved] */
  async function readBoard(boardId, checkAvailability, saved) {
    const readAvailability = () =>
      readWithSession(() => api.getBoardAvailability(boardId, boardSignal), boardSignal);
    if (checkAvailability) {
      const availability = saved?.data.id === boardId ? saved : await readAvailability();
      if (availability.data.availability !== "open") return { availability };
    }
    try {
      return { contents: await readContents(boardId) };
    } catch (error) {
      if (!(error instanceof ApiError) || error.status !== 503) throw error;
      const availability = await readAvailability();
      if (availability.data.availability === "open") throw error;
      return { availability };
    }
  }

  /** @param {unknown} error */
  function definitivelyUnavailable(error) {
    return (
      error instanceof ApiError &&
      (error.status === 401 || error.status === 403 || error.status === 404)
    );
  }

  /** @param {BoardAvailability} latest */
  async function showAvailability(latest) {
    if (view !== activeView) return;
    forgetRecentBoard(latest.data.id);
    await load(latest);
  }

  /** @param {string} id @param {import("./api.mjs").AvailabilityChange} requested @param {() => string} currentTag
   * @param {(latest: BoardAvailability) => Promise<void>} refreshCurrent @param {Inspection} inspection */
  function availabilityForm(id, requested, currentTag, refreshCurrent, inspection) {
    const action = form(requested === "open" ? "Reopen board" : "Close board", [], (submit) => {
      if (!action.isConnected || view !== activeView) return;
      const expected = currentTag();
      const prior = requested === "open" ? "closed" : "open";
      void writeResource(
        submit,
        () => api.setBoardAvailability(id, requested, expected),
        async () => {
          const latest = await api.getBoardAvailability(id);
          if (latest.data.availability !== prior) {
            if (view === activeView) await showAvailability(latest);
            else if (boardAtLocation() === id) await load();
          } else if (view === activeView) await refreshCurrent(latest);
          return latest.data.availability === requested && latest.etag !== expected;
        },
        requested === "open" ? "Board reopened." : "Board closed.",
        inspection,
      );
    });
    return action;
  }

  /** @param {string} boardId @param {BoardAvailability} metadata @param {unknown} principal */
  async function paintUnavailableBoard(boardId, metadata, principal) {
    forgetRecentBoard(boardId);
    const closedWorkflow =
      metadata.data.availability === "closed"
        ? await readWithSession(
            () => readWorkflow(metadata.data, principal, boardSignal),
            boardSignal,
          )
        : null;
    if (view !== activeView || boardSignal.aborted || !main) return;
    const message =
      metadata.data.availability === "closed"
        ? "This board is closed."
        : "This board is unhealthy.";
    const updates = boardUpdates(
      boardId,
      boardSignal,
      () => metadata.data.revision,
      async (automatic) => {
        const latest = await readWithSession(
          () => api.getBoardAvailability(boardId, boardSignal),
          boardSignal,
        );
        if (boardSignal.aborted || (automatic && writing)) return false;
        if (
          latest.etag !== metadata.etag ||
          latest.data.availability !== metadata.data.availability
        ) {
          await showAvailability(latest);
        }
        return true;
      },
    );
    const inspection = createInspection(
      main,
      () => view === activeView,
      updates.request,
      "board",
      "board:" + boardId,
    );
    boardInspection = inspection;
    main.replaceChildren(
      element("h1", metadata.data.name),
      element("p", message),
      durableHealth,
      updates.panel,
      button(inspection.label, inspection.inspect),
    );
    if (closedWorkflow?.canClose) {
      main.append(
        availabilityForm(
          boardId,
          "open",
          () => metadata.etag,
          async (latest) => {
            metadata = latest;
          },
          inspection,
        ),
      );
    }
    inspection.sync();
    announce(metadata.data.availability === "closed" ? "ready" : "error", message);
    void updates.start(subscribe);
  }

  /** @param {string} boardId @param {BoardDisplay} display
   * @param {Awaited<ReturnType<typeof readContents>>} [confirmed] */
  async function paintOpenBoard(boardId, { board, lanes, cards, priorities }, confirmed) {
    if (!main || !details) return;
    const drawer = details;
    const users = confirmed?.[1] ?? [];
    let workflow = confirmed?.[0].workflow ?? null;
    const currentWorkflow = () => workflow;
    let boardTag = confirmed ? etag(confirmed[0].board) : "";
    let paintedRevision = board.revision;
    let retained = !confirmed;
    const updates = boardUpdates(boardId, boardSignal, () => paintedRevision, applyBoardRefresh);
    const reloadBoard = updates.request;
    let currentCards = cards;
    let paintedPending = false;
    let showOlderCompleted = false;
    const columns = element("div");
    columns.className = "lanes";
    const state = boardViewState(boardId);
    const filter = element("input");
    filter.id = "board-filter";
    filter.type = "search";
    filter.value = typeof state.filter === "string" ? state.filter : "";
    filter.setAttribute("aria-keyshortcuts", "/");
    const filterCount = element("span");
    filterCount.setAttribute("role", "status");
    const filterBar = element("div");
    filterBar.className = "board-filter";
    filterBar.append(field("Filter by title or ticket", filter), filterCount);

    function updateActionColors() {
      const actor = users.find(({ id }) => id === workflow?.actor);
      const configured = board.actor_presentation?.human?.accent ?? actor?.color;
      const fill = configured && /^#[0-9a-f]{6}$/i.test(configured) ? configured : "#2463a0";
      const ink = actionInk(fill);
      for (const region of [columns, drawer]) {
        region.style.setProperty("--action-fill", fill);
        region.style.setProperty("--action-ink", ink);
      }
    }

    function applyFilter() {
      if (view !== activeView || !filter.isConnected) return;
      state.filter = filter.value;
      renderCards(currentCards, boardTag);
      if (!retained && hasUnresolvedInspection(inspection.scope)) inspection.sync();
      persistBoardView(board.id);
    }
    filter.addEventListener("input", applyFilter);
    filter.addEventListener("keydown", (event) => {
      if (event.defaultPrevented || event.isComposing || event.key !== "Escape" || !filter.value)
        return;
      event.preventDefault();
      filter.value = "";
      applyFilter();
    });

    /** @type {Map<string, HTMLElement>} */
    const laneColumns = new Map();
    /** @type {CardDrag|undefined} */
    let dragged;
    /** @type {HTMLElement|undefined} */
    let dropTarget;
    /** @type {Set<() => void>} */
    const deferredCovers = new Set();

    /** @param {HTMLElement} [target] */
    function markDropTarget(target) {
      if (dropTarget === target) return;
      if (dropTarget) delete dropTarget.dataset.dropTarget;
      dropTarget = target;
      if (target) target.dataset.dropTarget = "true";
    }

    function clearDrag() {
      if (dragged) delete dragged.opener.dataset.dragging;
      dragged = undefined;
      markDropTarget();
    }

    function finishDrag() {
      clearDrag();
      for (const reveal of deferredCovers) reveal();
      deferredCovers.clear();
    }

    /** @param {CardDrag} drag @param {Lane} lane @param {HTMLElement} column */
    function canDrop(drag, lane, column) {
      const { card, opener } = drag;
      return (
        view === activeView &&
        opener.isConnected &&
        column.isConnected &&
        laneColumns.get(lane.id) === column &&
        drag.boardTag === boardTag &&
        card.board_id === boardId &&
        !card.archived &&
        lane.id !== card.lane_id &&
        workflow?.move.get(card.lane_id)?.has(lane.id) === true
      );
    }

    /** @param {HTMLElement} column @param {Lane} lane */
    function bindDrop(column, lane) {
      column.addEventListener("dragover", (event) => {
        const transfer = event.dataTransfer;
        if (
          writing ||
          !dragged ||
          !transfer?.types.includes(CARD_DRAG_TYPE) ||
          !canDrop(dragged, lane, column)
        ) {
          markDropTarget();
          return;
        }
        event.preventDefault();
        transfer.dropEffect = "move";
        markDropTarget(column);
      });
      column.addEventListener("dragleave", (event) => {
        if (
          dropTarget === column &&
          !(event.relatedTarget instanceof Node && column.contains(event.relatedTarget))
        )
          markDropTarget();
      });
      column.addEventListener("drop", (event) => {
        const drag = dragged;
        finishDrag();
        const transfer = event.dataTransfer;
        if (!drag || !transfer?.types.includes(CARD_DRAG_TYPE)) return;
        event.preventDefault();
        if (
          writing ||
          transfer.getData(CARD_DRAG_TYPE) !== drag.payload ||
          !canDrop(drag, lane, column)
        )
          return;
        const { card, opener } = drag;
        if (
          !currentCards.some(
            (entry) =>
              entry.id === card.id &&
              entry.board_id === board.id &&
              entry.lane_id === card.lane_id &&
              entry.revision === card.revision &&
              !entry.archived,
          )
        )
          return;
        const scope = cardScope(board.id, card.id);
        if (hasUnresolvedInspection(scope)) {
          announce("error", "Open the affected card and use Inspect saved card before moving it.");
          return;
        }
        const inspection = createInspection(
          opener,
          () => view === activeView && opener.isConnected,
          reloadBoard,
          "card",
          scope,
        );
        moveCard(
          card,
          lane.id,
          async () => {
            const { saved, tag } = await readWithSession(
              () => readCardVersion(board.id, card.id),
              signal,
            );
            if (!canDrop(drag, lane, column)) throw new ApiError("move_view_changed");
            if (
              saved.revision !== card.revision ||
              saved.lane_id !== card.lane_id ||
              saved.archived
            ) {
              throw new ApiError("card_changed", { status: 412 });
            }
            return tag;
          },
          null,
          reloadBoard,
          inspection,
        );
      });
    }

    /** @param {HTMLButtonElement} opener @param {Card} card @param {string} renderedTag */
    function bindDrag(opener, card, renderedTag) {
      opener.draggable = true;
      opener.addEventListener("dragstart", (event) => {
        clearDrag();
        const transfer = event.dataTransfer;
        if (
          writing ||
          !transfer ||
          view !== activeView ||
          !opener.isConnected ||
          renderedTag !== boardTag
        ) {
          event.preventDefault();
          return;
        }
        const payload = JSON.stringify({
          board_id: boardId,
          card_id: card.id,
          lane_id: card.lane_id,
          revision: card.revision,
        });
        transfer.setData(CARD_DRAG_TYPE, payload);
        transfer.effectAllowed = "move";
        dragged = { card, opener, payload, boardTag: renderedTag };
        opener.dataset.dragging = "true";
      });
      opener.addEventListener("dragend", () => {
        if (dragged?.opener === opener) finishDrag();
      });
    }

    /** @type {WeakMap<HTMLElement, {requested: number, actual: number}>} */
    const clampedLaneScroll = new WeakMap();

    /** @param {HTMLElement} column @param {number} top */
    function restoreLaneTop(column, top) {
      column.scrollTop = top;
      const actual = column.scrollTop;
      if (actual < top) clampedLaneScroll.set(column, { requested: top, actual });
      else clampedLaneScroll.delete(column);
    }

    /** @param {HTMLElement} column */
    function retainedLaneTop(column) {
      const top = column.scrollTop;
      const clamped = clampedLaneScroll.get(column);
      if (clamped?.actual === top) return clamped.requested;
      clampedLaneScroll.delete(column);
      return top;
    }

    function retainScroll() {
      if (!columns.isConnected) return;
      state.scroll = {
        left: columns.scrollLeft,
        lanes: Object.fromEntries(
          [...laneColumns].map(([id, column]) => [id, retainedLaneTop(column)]),
        ),
      };
    }

    function restoreScroll() {
      if (!columns.isConnected) return;
      const saved = state.scroll;
      if (typeof saved?.left === "number" && Number.isFinite(saved.left))
        columns.scrollLeft = saved.left;
      for (const [id, column] of laneColumns) {
        const top = saved?.lanes?.[id];
        if (typeof top === "number" && Number.isFinite(top)) restoreLaneTop(column, top);
      }
    }

    const archived = element("details");
    const archivedHeading = element("summary", "Archived cards");
    const archivedList = element("div");
    archived.append(archivedHeading, archivedList);
    let archivedRead = 0;
    let coverReads = new AbortController();
    /** @type {Set<string>} */
    const covers = new Set();
    function clearCovers() {
      coverReads.abort();
      deferredCovers.clear();
      for (const url of covers) URL.revokeObjectURL(url);
      covers.clear();
    }
    releaseBoardMedia = clearCovers;

    /** @param {Card} card @param {HTMLElement} column @param {HTMLElement} slot @param {AbortSignal} readSignal */
    async function showCover(card, column, slot, readSignal) {
      const explicitId = card.cover_attachment_id;
      const current = () => view === activeView && !readSignal.aborted && slot.isConnected;
      /** @param {Attachment} entry */
      const eligible = (entry) => activeAttachment(board.id, card.id, entry) && entry.previewable;
      try {
        const blob = await readWithSession(async () => {
          if (!current()) return;
          /** @type {Attachment|undefined} */
          let selected;
          if (explicitId) {
            const entry = /** @type {Attachment} */ (
              (await api.getAttachment(explicitId, readSignal)).data
            );
            if (entry.id === explicitId && eligible(entry)) selected = entry;
          } else {
            for await (const page of collectionPages(
              (cursor) => api.listAttachments(card.id, cursor, readSignal),
              "attachments",
              readSignal,
            )) {
              selected = /** @type {Attachment[]} */ (page).find(eligible);
              if (selected) break;
            }
          }
          if (!current() || !selected) return;
          return api.getAttachmentContent(
            selected.id,
            "inline",
            selected.size,
            selected.sha2_256,
            readSignal,
          );
        }, readSignal);
        if (!current() || !blob) return;
        const url = URL.createObjectURL(blob);
        covers.add(url);
        const image = element("img");
        image.draggable = false;
        image.className = "card-cover";
        image.alt = "";
        function reveal() {
          if (!current() || !covers.has(url)) return;
          const top = retainedLaneTop(column);
          slot.hidden = false;
          restoreLaneTop(column, top);
        }
        if (!explicitId)
          image.addEventListener(
            "load",
            () => {
              if (!current()) return;
              if (dragged) deferredCovers.add(reveal);
              else reveal();
            },
            { once: true },
          );
        image.addEventListener(
          "error",
          () => {
            deferredCovers.delete(reveal);
            image.remove();
            if (covers.delete(url)) URL.revokeObjectURL(url);
          },
          { once: true },
        );
        image.src = url;
        slot.replaceChildren(image);
      } catch {
        // The card remains usable when an attachment cannot be read.
      }
    }

    /** @param {number} count @param {string} noun */
    function countLabel(count, noun) {
      return count + " " + noun + (count === 1 ? "" : "s");
    }

    let dismissedRetainedCard = false;
    /** @param {Pick<Card, "id"> & Partial<Card>} card @param {HTMLElement} opener @param {boolean} [restoring] */
    function selectCard(card, opener, restoring = false) {
      if (view !== activeView || (writing && !restoring)) return;
      if (!retained) {
        void openCard(
          board.id,
          card,
          lanes,
          users,
          priorities,
          () => boardTag,
          reloadBoard,
          currentWorkflow,
          opener,
          () => board.actor_presentation,
          restoring,
        );
        return;
      }
      const selection = ++activeCard;
      rememberCard(boardId, card.id);
      const heading = element(
        "h2",
        card.title === undefined ? "Checking saved card…" : "#" + card.ticket + " " + card.title,
      );
      heading.tabIndex = -1;
      const close = button("Close card", () => {
        if (view !== activeView || selection !== activeCard) return;
        dismissedRetainedCard = true;
        rememberCard(boardId);
        ++activeCard;
        drawer.hidden = true;
        drawer.replaceChildren();
        restoreCardFocus(card.id, opener);
      });
      const body = cardProse(card.description ?? "", boardId);
      drawer.hidden = false;
      delete drawer.dataset.unavailable;
      drawer.replaceChildren(close, heading, body);
      if (!restoring) heading.focus();
    }

    /** @param {Card} card */
    function cardOpener(card) {
      const opener = button("#" + card.ticket + " " + card.title, () => selectCard(card, opener));
      opener.className = "card";
      opener.dataset.cardId = card.id;
      const description = [];
      const priority = priorities.find(({ id }) => id === card.priority_id);
      if (priority) {
        const label = element("small", priority.label);
        label.className = "card-priority";
        label.setAttribute("aria-hidden", "true");
        description.push("Priority: " + priority.label);
        opener.append(label);
      }
      if (card.comment_count > 0 || card.attachment_count > 0) {
        const counts = element("span");
        counts.className = "card-counts";
        counts.setAttribute("aria-hidden", "true");
        if (card.comment_count > 0) {
          const comments = element("span", "\u{1f4ac} " + card.comment_count);
          comments.className = "card-comment-count";
          counts.append(comments);
          description.push(countLabel(card.comment_count, "comment"));
        }
        if (card.attachment_count > 0) {
          const attachments = element("span", "\u{1f4ce} " + card.attachment_count);
          attachments.className = "card-attachment-count";
          counts.append(attachments);
          description.push(countLabel(card.attachment_count, "attachment"));
        }
        opener.append(counts);
      }
      if (description.length) opener.setAttribute("aria-description", description.join(", "));
      return opener;
    }

    /** @param {HTMLButtonElement} control @param {Lane} lane @param {"add"|"preview"|"apply"|"approve"} action
     * @param {string} name */
    function nameLaneAction(control, lane, action, name) {
      control.className = "board-action";
      control.dataset.laneAction = lane.id + ":" + action;
      control.setAttribute("aria-label", name + " " + lane.label);
      if (action !== "preview") {
        control.dataset.laneWrite = "true";
        syncLaneWriteControl(control);
      }
    }

    /** @param {string} action */
    function focusLaneAction(action) {
      const actions = [...columns.querySelectorAll("button")].filter(
        (control) => !control.disabled,
      );
      const replacement =
        actions.find((control) => control.dataset.laneAction === action) ??
        actions.find(
          (control) => control.dataset.laneAction === action.replace(/:apply$/, ":preview"),
        );
      (replacement ?? filter).focus({ preventScroll: true });
    }

    /** @param {HTMLButtonElement} submit */
    function restoreLaneActionAfterWrite(submit) {
      const action = submit === document.activeElement ? submit.dataset.laneAction : undefined;
      return () => {
        if (action && view === activeView && focusWasLost()) focusLaneAction(action);
      };
    }

    /** @param {Card[]} visibleCards @param {string} renderedTag */
    function renderCards(visibleCards, renderedTag) {
      clearDrag();
      retainScroll();
      laneColumns.clear();
      const focused = document.activeElement;
      const focusedId =
        focused instanceof HTMLButtonElement && columns.contains(focused)
          ? focused.dataset.cardId
          : undefined;
      const focusedAction =
        focused instanceof HTMLButtonElement && columns.contains(focused)
          ? focused.dataset.laneAction
          : undefined;
      const focusedPreview =
        focused instanceof HTMLInputElement && columns.contains(focused)
          ? focused.dataset.previewLane
          : undefined;
      const previews = new Map(
        [...columns.querySelectorAll("input")].map((input) => [
          input.dataset.previewLane,
          input.value,
        ]),
      );
      clearCovers();
      coverReads = new AbortController();
      const coverSignal = coverReads.signal;
      const active = activeLanes(lanes);
      const completed = active.find(({ role }) => role === "completed");
      const query = filter.value.trim().toLowerCase();
      const operation = pendingCard?.card.board_id === boardId ? pendingCard : undefined;
      const original = operation && visibleCards.find(({ id }) => id === operation.card.id);
      const fields = original && pendingFields(original);
      const destination = fields?.lane_id ?? original?.lane_id;
      const edited =
        fields && original && active.some(({ id }) => id === destination)
          ? { ...original, ...fields, completed_at: fields.lane_id ? null : original.completed_at }
          : undefined;
      const created =
        operation?.kind === "create" &&
        !original &&
        active.some(({ id }) => id === operation.card.lane_id)
          ? operation.card
          : undefined;
      paintedPending = Boolean(edited || created);
      // Project only the paint. The retained board and request validators use the confirmed list.
      const displayedCards = edited
        ? visibleCards.map((card) => (card === original ? edited : card))
        : visibleCards;
      const displayedTotal = displayedCards.length + (created ? 1 : 0);
      let matchingCount = 0;
      const completionCutoff = Date.now() - 24 * 60 * 60 * 1000;
      columns.replaceChildren(
        ...active.map((lane) => {
          const column = element("section");
          column.className = "lane";
          laneColumns.set(lane.id, column);
          bindDrop(column, lane);
          const members = displayedCards.filter(({ lane_id }) => lane_id === lane.id);
          const creating = created?.lane_id === lane.id ? created : undefined;
          const creatingMatches =
            creating && (!query || creating.title.toLowerCase().includes(query));
          const matches = query
            ? members.filter(
                ({ title, ticket }) =>
                  title.toLowerCase().includes(query) || ("#" + ticket).includes(query),
              )
            : members;
          const older =
            lane.role === "completed" && !showOlderCompleted
              ? new Set(
                  matches.filter(({ completed_at }) =>
                    completedBefore(completed_at, completionCutoff),
                  ),
                )
              : new Set();
          const wholeLaneVisible = () =>
            matches.length === members.length && (older.size === 0 || showOlderCompleted);
          const totalCount = members.length + (creating ? 1 : 0);
          const matchingTotal = matches.length + (creatingMatches ? 1 : 0);
          matchingCount += matchingTotal;
          const heading = element("h2", lane.label);
          heading.className = "lane-heading";
          const total = element(
            "small",
            query ? matchingTotal + " of " + totalCount + " cards" : countLabel(totalCount, "card"),
          );
          total.className = "lane-total";
          heading.append(total);
          column.append(heading);
          if (lane.notes) column.append(laneNotes(lane));
          if (canCreateIn(lane.id)) {
            const add = button("Add card", () => {
              if (writing || view !== activeView || !column.isConnected || !canCreateIn(lane.id))
                return;
              addLane = lane.id;
              retainNewCardText();
              updateNewCardForm();
              addTitle.focus();
            });
            add.dataset.createLane = lane.id;
            nameLaneAction(add, lane, "add", "Add card in");
            column.append(add);
          }
          /** @type {(() => void)|undefined} */
          let updatePreview;
          if (!retained && lane.reorder_enabled) {
            const canPreview = () => !paintedPending && wholeLaneVisible() && members.length >= 2;
            const preview = element("div");
            const selectedTickets = element("input");
            selectedTickets.dataset.previewLane = lane.id;
            selectedTickets.value = previews.get(lane.id) ?? "";
            selectedTickets.placeholder = "All active cards";
            selectedTickets.setAttribute("aria-label", "Tickets to preview in " + lane.label);
            let previewing = false;
            const previewButton = button("Preview ticket order", () => {
              if (previewing || !canPreview() || !preview.isConnected || view !== activeView)
                return;
              preview.replaceChildren();
              let ids;
              try {
                ids =
                  selectedTickets.value === ""
                    ? undefined
                    : ticketSelection(selectedTickets.value, members).map(({ id }) => id);
                if (ids && ids.length < 2) {
                  preview.textContent = "Choose at least two active tickets from this lane.";
                  return;
                }
              } catch {
                preview.textContent = "Enter distinct active ticket numbers from this lane.";
                return;
              }
              previewing = true;
              previewButton.setAttribute("aria-disabled", "true");
              void readWithSession(() => api.previewTicketOrder(board.id, lane.id, ids))
                .then((reply) => {
                  if (!preview.isConnected || view !== activeView) return;
                  const originalTag = etag(reply);
                  if (originalTag !== renderedTag) {
                    preview.textContent = "The board changed. Refresh the board and preview again.";
                    return;
                  }
                  const result =
                    /** @type {{board_revision: number, current: {card_id: string, ticket: number}[], proposed: {card_id: string, ticket: number}[], changed: boolean}} */ (
                      reply.data
                    );
                  const currentTickets = new Map(
                    result.current.map(({ card_id, ticket }) => [card_id, ticket]),
                  );
                  preview.replaceChildren(
                    element("p", "Preview at board revision " + result.board_revision),
                  );
                  for (const entry of result.proposed) {
                    preview.append(
                      element(
                        "p",
                        "#" +
                          currentTickets.get(entry.card_id) +
                          " → #" +
                          entry.ticket +
                          " " +
                          (members.find(({ id }) => id === entry.card_id)?.title ?? entry.card_id),
                      ),
                    );
                  }
                  if (!result.proposed.length)
                    preview.append(element("p", "No active cards in this lane."));
                  if (result.changed) {
                    const renumbered = result.proposed
                      .filter(({ card_id, ticket }) => currentTickets.get(card_id) !== ticket)
                      .map(({ card_id }) => card_id);
                    const apply = form("Apply ticket order", [], (submit) => {
                      if (
                        !preview.isConnected ||
                        view !== activeView ||
                        hasUnresolvedInspection(inspection.scope)
                      )
                        return;
                      const restoreFocus = restoreLaneActionAfterWrite(submit);
                      void writeResource(
                        submit,
                        async () => {
                          requireAttachmentUploadsSettled(board.id, renumbered);
                          await api.applyTicketOrder(
                            board.id,
                            lane.id,
                            originalTag,
                            result.proposed,
                          );
                        },
                        async () => {
                          await reloadBoard();
                          return result.proposed.every((entry) =>
                            currentCards.some(
                              (card) => card.id === entry.card_id && card.ticket === entry.ticket,
                            ),
                          );
                        },
                        "Ticket order applied.",
                        inspection,
                      ).finally(restoreFocus);
                    });
                    const submit = apply.querySelector("button");
                    if (submit) nameLaneAction(submit, lane, "apply", "Apply ticket order in");
                    preview.append(apply);
                    if (hasUnresolvedInspection(inspection.scope)) inspection.sync();
                  }
                })
                .catch(() => {
                  if (preview.isConnected)
                    preview.textContent =
                      "Preview refused or unavailable. A selected dependency cycle cannot be reordered.";
                })
                .finally(() => {
                  previewing = false;
                  previewButton.removeAttribute("aria-disabled");
                });
            });
            previewButton.dataset.action = "preview-ticket-order";
            nameLaneAction(previewButton, lane, "preview", "Preview ticket order in");
            updatePreview = () => {
              const disabled = !canPreview();
              previewButton.disabled = disabled;
              selectedTickets.disabled = disabled;
            };
            updatePreview();
            column.append(selectedTickets, previewButton, preview);
          }
          const canApprove =
            !retained &&
            !paintedPending &&
            completed &&
            lane.role === "ready_for_review" &&
            lane.approval_enabled &&
            members.length > 0 &&
            members.length <= MAX_LANE_APPROVAL_CARDS;
          if (canApprove && wholeLaneVisible()) {
            const versions = members.map(({ id, revision }) => ({ id, revision }));
            const cardIds = versions.map(({ id }) => id);
            const approval = form("Approve all in lane", [], (submit) => {
              if (view !== activeView || !column.isConnected) return;
              const restoreFocus = restoreLaneActionAfterWrite(submit);
              const id = newOperationId();
              const drawer = activeDrawer;
              let approved = false;
              void writeResource(
                submit,
                async () => {
                  requireAttachmentUploadsSettled(board.id, cardIds);
                  await api.approveLane(
                    id,
                    board.id,
                    renderedTag,
                    lane.id,
                    completed.id,
                    versions,
                    lane.bulk_approval_comment,
                  );
                  approved = true;
                },
                async () => {
                  if (!approved) {
                    try {
                      await api.inspectLaneApproval(
                        id,
                        board.id,
                        renderedTag,
                        lane.id,
                        completed.id,
                        cardIds,
                        lane.bulk_approval_comment,
                      );
                      approved = true;
                    } catch (error) {
                      if (!(error instanceof ApiError) || error.status !== 404) throw error;
                    }
                  }
                  await reloadBoard();
                  if (
                    approved &&
                    drawer &&
                    activeDrawer === drawer &&
                    drawer.boardId === boardId &&
                    cardIds.includes(drawer.id)
                  )
                    drawer.close();
                  return approved;
                },
                "Lane approved and moved to Completed.",
                inspection,
              ).finally(restoreFocus);
            });
            const submit = approval.querySelector("button");
            if (submit) nameLaneAction(submit, lane, "approve", "Approve all in lane");
            column.append(approval);
          }
          if (
            !retained &&
            !paintedPending &&
            matches.length < members.length &&
            (canApprove || (lane.reorder_enabled && members.length >= 2))
          ) {
            column.append(element("p", "Clear the filter to use lane-wide actions."));
          }
          const mayDrag = moveDestinations(lanes, workflow, lane.id).length > 0;
          /** @param {Card} card */
          function cardFace(card) {
            const opener = cardOpener(card);
            if (card === edited) {
              opener.dataset.pending = "true";
              opener.append(pendingLabel(fields?.lane_id ? "Moving…" : "Saving…"));
            } else if (mayDrag) bindDrag(opener, card, renderedTag);
            if (!retained && (card.cover_attachment_id || card.attachment_count > 0)) {
              const slot = element("span");
              slot.className = "card-cover-space";
              slot.hidden = !card.cover_attachment_id;
              slot.setAttribute("aria-hidden", "true");
              opener.prepend(slot);
              void showCover(card, column, slot, coverSignal);
            }
            return opener;
          }
          if (creatingMatches) {
            const placeholder = element("div", creating.title);
            placeholder.className = "card";
            placeholder.dataset.cardId = creating.id;
            placeholder.dataset.pending = "true";
            placeholder.append(pendingLabel("Creating…"));
            column.append(placeholder);
          }
          if (older.size === 0) {
            for (const card of matches) column.append(cardFace(card));
            return column;
          }
          const faces = element("div");
          const reveal = button("Show " + countLabel(older.size, "older card"), () => {
            if (view !== activeView || !column.isConnected || !reveal.isConnected) return;
            showOlderCompleted = true;
            updatePreview?.();
            const openers = new Map(
              [...faces.querySelectorAll("button")].map((face) => [face.dataset.cardId, face]),
            );
            faces.replaceChildren();
            /** @type {HTMLButtonElement|undefined} */
            let firstOlder;
            for (const card of matches) {
              const opener = openers.get(card.id) ?? cardFace(card);
              if (older.has(card)) firstOlder ??= opener;
              faces.append(opener);
            }
            reveal.remove();
            firstOlder?.focus();
          });
          reveal.className = "show-older-completed";
          for (const card of matches) {
            if (!older.has(card)) faces.append(cardFace(card));
          }
          column.append(reveal, faces);
          return column;
        }),
      );
      const count = query
        ? matchingCount + " of " + displayedTotal + " cards match"
        : countLabel(displayedTotal, "card");
      if (filterCount.textContent !== count) filterCount.textContent = count;
      restoreScroll();
      if (focusedId) {
        const opener = [...columns.querySelectorAll("button")].find(
          (control) => control.dataset.cardId === focusedId,
        );
        (opener ?? filter).focus({ preventScroll: true });
      }
      if (focusedAction) focusLaneAction(focusedAction);
      if (focusedPreview) {
        const input = [...columns.querySelectorAll("input")].find(
          (control) => !control.disabled && control.dataset.previewLane === focusedPreview,
        );
        (input ?? filter).focus({ preventScroll: true });
      }
    }

    /** @param {string} label */
    function pendingLabel(label) {
      const marker = element("small", label);
      marker.className = "card-pending-label";
      return marker;
    }

    async function reloadArchived() {
      if (!archived.open || view !== activeView) return;
      const read = ++archivedRead;
      try {
        const cards = /** @type {Card[]} */ (
          await readWithSession(() =>
            collect((cursor) => api.listCards(boardId, { cursor, archived: "only" }), "cards"),
          )
        );
        if (view !== activeView || read !== archivedRead || !archived.open) return;
        const focused = document.activeElement;
        const focusedId =
          focused instanceof HTMLButtonElement && archivedList.contains(focused)
            ? focused.dataset.cardId
            : undefined;
        archivedList.replaceChildren(...cards.map(cardOpener));
        if (!cards.length) archivedList.textContent = "No archived cards.";
        if (focusedId)
          (
            [...archivedList.querySelectorAll("button")].find(
              (control) => control.dataset.cardId === focusedId,
            ) ?? archivedHeading
          ).focus();
      } catch {
        if (view === activeView && read === archivedRead && archived.open) {
          archivedList.textContent = "Archived cards could not be read. Close and reopen to retry.";
        }
      }
    }
    archived.addEventListener("toggle", () => {
      archivedList.replaceChildren();
      if (archived.open) {
        archivedList.textContent = "Reading archived cards…";
        void reloadArchived();
      }
    });

    const addTitle = element("input");
    addTitle.required = true;
    const addDescription = element("textarea");
    const seeds = renderRelationshipSeeds(board.id);
    const scope = newCardScope(board.id);
    const previous = boardDrafts.get(board.id);
    const persisted = textDrafts.get(scope);
    addTitle.value = previous?.[0] ?? persisted?.title ?? "";
    addDescription.value = previous?.[1] ?? persisted?.description ?? "";
    let addLane = previous?.[2] ?? persisted?.lane ?? "";
    function retainNewCardText() {
      if (view !== activeView) return;
      boardDrafts.set(board.id, [addTitle.value, addDescription.value, addLane]);
      storeTextDraft(scope, {
        ...(addTitle.value ? { title: addTitle.value } : {}),
        ...(addDescription.value ? { description: addDescription.value } : {}),
        ...(addLane ? { lane: addLane } : {}),
      });
    }
    for (const control of [addTitle, addDescription])
      control.addEventListener("input", retainNewCardText);
    const editor = element("section");
    editor.className = "editor";
    const inspection = createInspection(
      main,
      () => view === activeView && !retained,
      reloadBoard,
      "board",
      scope,
      undefined,
      async (submission) => {
        if (submission.kind !== "create-card") return false;
        const created = await readCreatedCard(board.id, submission.id);
        if (created) {
          const draft = { title: addTitle.value, description: addDescription.value };
          if (
            (await newCardTextFingerprint(draft)) === submission.fingerprint &&
            savedSubmission(scope) === submission
          ) {
            clearCreatedCardText(board.id, draft);
          }
        }
        await reloadBoard();
        return created !== undefined;
      },
    );
    editor.append(button(inspection.label, inspection.inspect), inspection.recovery);
    const newCard = element("section");
    const newCardHeading = element("h2");

    /** @param {string} id */
    function canCreateIn(id) {
      return (
        workflow?.create.has(id) === true && lanes.some((lane) => lane.id === id && !lane.retired)
      );
    }

    /** @param {string} id */
    function findAddButton(id) {
      return [...columns.querySelectorAll("button")].find(
        (control) => control.dataset.createLane === id,
      );
    }

    function updateNewCardForm() {
      const destination = canCreateIn(addLane) ? lanes.find(({ id }) => id === addLane) : undefined;
      newCard.hidden = !destination;
      files.sync();
      seeds.setAllowed(workflow?.canManageRelationships === true);
      if (destination) newCardHeading.textContent = "New card in " + destination.label;
    }

    const canSubmitNewCard = () => view === activeView && !newCard.hidden && canCreateIn(addLane);
    const files = renderNewCardFiles(board.id, canSubmitNewCard);
    newCard.addEventListener("paste", files.paste);
    newCard.append(
      newCardHeading,
      form(
        "Create card",
        [
          field("New title", addTitle),
          field("New description", addDescription),
          seeds.panel,
          files.field,
          files.list,
        ],
        (submit) => {
          if (!canSubmitNewCard()) return;
          const id = crypto.randomUUID();
          const cardSelection = activeCard;
          let staged = newCardFiles.get(board.id) ?? [];
          let showCreatedCard = staged.length > 0;
          /** @type {string|undefined} */
          let createdTag;
          let created = false;
          let reconciled = false;
          const draft = {
            title: addTitle.value,
            description: addDescription.value,
            lane_id: addLane,
          };
          /** @type {PendingCard} */
          const projection = {
            kind: "create",
            card: { id, board_id: board.id, title: draft.title, lane_id: draft.lane_id },
          };
          const submitted = workflow?.canManageRelationships === true ? seeds.capture() : [];
          const expected = boardTag;
          retainNewCardText();
          void writeResource(
            submit,
            async (record) => {
              showPendingCard(projection, true);
              const relationships = submitted.length
                ? await resolveRelationshipSeeds(board.id, submitted, () => view === activeView)
                : undefined;
              const fingerprint = await newCardTextFingerprint(draft);
              if (view !== activeView) throw new ApiError("invalid_target");
              if (relationships)
                requireAttachmentUploadsSettled(
                  board.id,
                  relationships.map(({ target_card_id }) => target_card_id),
                );
              record({ kind: "create-card", id, fingerprint });
              const reply = await api.createCard({
                ...draft,
                id,
                board_id: board.id,
                expected_board_etag: expected,
                relationships,
              });
              if (staged.length) {
                const saved = /** @type {Card|undefined} */ (reply?.data);
                if (saved?.id !== id || saved.board_id !== board.id || !reply.etag) {
                  throw new ApiError("invalid_response", { outcomeUnknown: true });
                }
                createdTag = reply.etag;
              }
              created = true;
            },
            async () => {
              if (!created) {
                if (staged.length) {
                  try {
                    createdTag = (await readCardVersion(board.id, id)).tag;
                    created = true;
                  } catch (error) {
                    if (!(error instanceof ApiError) || error.status !== 404) throw error;
                  }
                } else {
                  created = (await readCreatedCard(board.id, id)) !== undefined;
                }
              }
              if (created && !reconciled) {
                clearCreatedCardText(board.id, draft);
                seeds.clearSubmitted(submitted);
                reconciled = true;
                if (createdTag && staged.length) {
                  newCardFiles.delete(board.id);
                  if (activeNewCardFiles?.boardId === board.id) activeNewCardFiles.paint();
                  void uploadFiles(board.id, id, createdTag, staged);
                  staged = [];
                }
              }
              await reloadBoard();
              if (
                createdTag &&
                showCreatedCard &&
                view === activeView &&
                cardSelection === activeCard
              ) {
                showCreatedCard = false;
                const opener = columns.querySelector('button[data-card-id="' + id + '"]');
                void openCard(
                  board.id,
                  { id },
                  lanes,
                  users,
                  priorities,
                  () => boardTag,
                  reloadBoard,
                  currentWorkflow,
                  opener instanceof HTMLButtonElement ? opener : filter,
                  () => board.actor_presentation,
                );
              }
              return created;
            },
            "Card created.",
            inspection,
            projection,
          );
        },
      ),
      button("Close new card", () => {
        if (writing) return;
        const opener = findAddButton(addLane);
        addLane = "";
        retainNewCardText();
        updateNewCardForm();
        (opener ?? refresh)?.focus();
      }),
    );
    editor.append(newCard);
    updateNewCardForm();
    const recent = element("details");
    const recentList = element("div");
    recent.append(element("summary", "Newest comments"), recentList);
    /** @type {CommentListEntry[]|undefined} */
    let recentComments;
    function showRecentComments() {
      if (!recentComments) return;
      recentList.replaceChildren(
        ...recentComments.map((entry) => {
          const item = element("article");
          const body = commentProse(entry, board.id, users);
          const byline = element("small");
          byline.append(
            actorAttribution(users, board.actor_presentation, entry.author_id),
            element("span", " · " + entry.created_at),
          );
          item.append(byline, body);
          return item;
        }),
      );
    }
    recent.addEventListener("toggle", () => {
      if (!recent.open) return;
      void api
        .listComments({ board_id: board.id }, 2)
        .then((reply) => {
          recentComments = /** @type {CommentListEntry[]} */ (items(reply, "comments"));
          showRecentComments();
        })
        .catch(() => {
          recentComments = undefined;
          recentList.textContent = "Comments could not be read.";
        });
    });

    /** @param {boolean} automatic */
    async function applyBoardRefresh(automatic) {
      const canApply = () => !boardSignal.aborted && (!automatic || !writing);
      let result;
      try {
        result = await readBoard(boardId, retained);
      } catch (error) {
        if (canApply() && definitivelyUnavailable(error)) {
          forgetRecentBoard(boardId);
          if (retained) {
            failBoard(error);
            return true;
          }
        }
        throw error;
      }
      if (!canApply()) return false;
      if (!result.contents) {
        await showAvailability(result.availability);
        return true;
      }
      const [latest, latestUsers] = result.contents;
      const updatedBoard = /** @type {Board} */ (latest.board.data);
      const latestTag = etag(latest.board);
      const publishing = validPublishing(updatedBoard.publishing)
        ? { data: updatedBoard.publishing, etag: latestTag }
        : undefined;
      if (!retained && publishing) boardPublishing.show(publishing);
      if (
        !retained &&
        automatic &&
        latestTag === boardTag &&
        paintedRevision === updatedBoard.revision &&
        workflow?.actor === latest.workflow?.actor
      )
        return true;
      workflow = latest.workflow;
      updateCloseBoard();
      users.splice(0, users.length, ...latestUsers);
      board.name = updatedBoard.name;
      board.actor_presentation = updatedBoard.actor_presentation;
      if (recent.open) showRecentComments();
      const heading = main?.querySelector("h1");
      if (heading) heading.textContent = board.name;
      lanes.splice(0, lanes.length, ...latest.lanes);
      currentCards = latest.cards;
      priorities.splice(0, priorities.length, ...latest.priorities);
      const firstConfirmation = retained;
      const boardChanged = retained
        ? paintedRevision !== updatedBoard.revision
        : latestTag !== boardTag;
      boardTag = latestTag;
      retained = false;
      boardStyle.replaceSync(updatedBoard.user_css ?? "");
      updateActionColors();
      updateNewCardForm();
      laneEditor.showLanes(firstConfirmation);
      search.refreshBoard(boardChanged);
      const batchPriority = boardActions.querySelector("#batch-priority-value");
      if (batchPriority instanceof HTMLSelectElement)
        setOptions(batchPriority, priorities, batchPriority.value);
      renderCards(latest.cards, boardTag);
      if (firstConfirmation) {
        enableConfirmedControls();
        if (publishing) boardPublishing.show(publishing);
        announce("ready", "Board loaded.");
        void restoreCardSelection();
      }
      inspection.sync();
      if (!automatic) invalidateCatalog();
      if (!firstConfirmation && activeDrawer?.boardId === board.id)
        await activeDrawer.refreshWorkflow(canApply, automatic);
      if (!canApply()) return false;
      await reloadArchived();
      if (!canApply()) return false;
      paintedRevision = updatedBoard.revision;
      retainRecentBoard(boardDisplay(latest));
      return true;
    }
    const laneEditor = renderLaneEditor(
      board.id,
      lanes,
      users,
      () => boardTag,
      reloadBoard,
      inspection,
    );
    const boardActions = renderBoardActions(
      board.id,
      priorities,
      () => currentCards,
      () => boardTag,
      reloadBoard,
      inspection,
    );
    const boardPublishing = renderPublishing(
      "board",
      board.id,
      validPublishing(confirmed?.[0].board.data.publishing)
        ? { data: confirmed[0].board.data.publishing, etag: boardTag }
        : undefined,
      () => api.getBoardPublishing(board.id),
      (enabled, tag) => api.configureBoardPublishing(board.id, enabled, tag),
      reloadBoard,
      inspection,
    );
    boardActions.append(boardPublishing.panel);
    const closeBoard = availabilityForm(boardId, "closed", () => boardTag, reloadBoard, inspection);
    function updateCloseBoard() {
      if (!workflow?.canClose) closeBoard.remove();
      else if (!boardActions.contains(closeBoard)) boardActions.append(closeBoard);
    }
    updateCloseBoard();
    const search = renderCardSearch(board.id, lanes, selectCard, boardSignal);
    function enableConfirmedControls() {
      main?.insertBefore(editor, search.panel);
      for (const panel of [recent, laneEditor.editor, boardActions])
        main?.insertBefore(panel, filterBar);
      main?.append(archived);
      boardInspection = inspection;
      activeNewCardText = { boardId: board.id, title: addTitle, description: addDescription };
      activeNewCardFiles = { boardId: board.id, paint: files.paint, sync: files.sync };
      retainNewCardDraft = retainNewCardText;
      retainBoardDraft = () => {
        retainNewCardText();
        seeds.retain();
        laneEditor.retainDraft();
        boardPublishing.retain();
      };
    }
    updateActionColors();
    activePendingBoard = {
      id: board.id,
      paint: () => {
        if (!boardSignal.aborted && columns.isConnected && (pendingCard || paintedPending)) {
          renderCards(currentCards, boardTag);
          if (!retained && hasUnresolvedInspection(inspection.scope)) inspection.sync();
        }
      },
    };
    renderCards(cards, boardTag);
    main.replaceChildren(
      element("h1", board.name),
      durableHealth,
      updates.panel,
      search.panel,
      filterBar,
      columns,
    );
    retainBoardView = () => {
      clearDrag();
      retainScroll();
      search.retain();
    };
    restoreScroll();
    search.restore();
    boardStyle.replaceSync(board.user_css ?? "");
    if (!retained) {
      enableConfirmedControls();
      inspection.sync();
      retainRecentBoard({ board, lanes, cards, priorities });
      announce("ready", "Board loaded.");
    } else {
      announce("ready", "Showing the last confirmed board.");
    }
    const firstRefresh = updates.start(subscribe, retained);
    await restoreCardSelection();
    await firstRefresh;

    async function restoreCardSelection() {
      const ticketValue = new URLSearchParams(location.search).get("ticket");
      const selectedCard = rememberedCard(boardId, ticketValue);
      if (selectedCard) {
        const opener = [...columns.querySelectorAll("button")].find(
          (control) => control.dataset.cardId === selectedCard,
        );
        const card = currentCards.find(({ id }) => id === selectedCard) ?? { id: selectedCard };
        const target = opener ?? filter;
        if (!retained && document.activeElement && drawer.contains(document.activeElement)) {
          target.focus({ preventScroll: true });
        }
        selectCard(card, target, true);
        restoreScroll();
        return;
      }
      if (retained || ticketValue === null || dismissedRetainedCard) return;
      const ticket = Number(ticketValue);
      const cardView = activeCard;
      let selected;
      try {
        if (/^[1-9][0-9]*$/.test(ticketValue) && Number.isSafeInteger(ticket)) {
          selected = await readWithSession(() => findCardByTicket(board.id, ticket));
        }
      } catch (error) {
        if (view === activeView && cardView === activeCard) announce("error", readError(error));
        return;
      }
      if (view !== activeView || cardView !== activeCard) return;
      if (selected) selectCard(selected, filter);
      else announce("error", "This ticket is unavailable.");
    }
  }
}

/** @param {Lane[]} lanes */
function activeLanes(lanes) {
  return lanes.filter(({ retired }) => !retired).sort((a, b) => a.position - b.position);
}

/** @param {string|null} completedAt @param {number} cutoff */
function completedBefore(completedAt, cutoff) {
  if (typeof completedAt !== "string") return false;
  const completion = new Date(completedAt.replace(" ", "T"));
  return (
    completion.getTime() < cutoff &&
    completion.toISOString().replace("T", " ").replace(".000Z", "+00:00") === completedAt
  );
}

/** @param {string} boardId @param {Lane[]} lanes @param {Labeled[]} users
 * @param {() => string} boardTag @param {() => Promise<void>} reloadBoard @param {Inspection} inspection */
function renderLaneEditor(boardId, lanes, users, boardTag, reloadBoard, inspection) {
  const editor = element("details");
  editor.className = "editor";
  const addName = element("input");
  addName.required = true;
  addName.maxLength = 256;
  const after = select(activeLanes(lanes), null);
  const createFor = select(users, null);
  const connectFor = select(users, null);
  for (const grants of [createFor, connectFor]) {
    grants.multiple = true;
    for (const option of grants.options) option.selected = false;
  }
  const target = select(activeLanes(lanes), null);
  const reviewLane = select(
    activeLanes(lanes).filter(({ role }) => role === "ready_for_review"),
    null,
  );
  const retireLane = select(
    activeLanes(lanes).filter(({ role }) => role !== "completed"),
    null,
  );
  for (const control of [after, target, reviewLane, retireLane]) control.required = true;
  const renameName = element("input");
  renameName.required = true;
  renameName.maxLength = 256;
  const keyLane = select(
    activeLanes(lanes).filter(({ role }) => role !== "completed"),
    null,
  );
  keyLane.id = "lane-key-target";
  keyLane.required = true;
  const keyValue = element("input");
  keyValue.id = "lane-key-value";
  keyValue.required = true;
  keyValue.maxLength = 256;
  const names = element("p");
  const draftKey = boardId + ":lanes";
  const previous = boardDrafts.get(draftKey);
  addName.value = previous?.[0] ?? "";
  renameName.value = previous?.[1] ?? "";
  if (previous?.[2] !== undefined) after.value = previous[2];
  if (previous?.[3] !== undefined) target.value = previous[3];
  if (previous?.[6] !== undefined) reviewLane.value = previous[6];
  if (previous?.[7] !== undefined) retireLane.value = previous[7];
  if (previous?.[8] !== undefined) keyLane.value = previous[8];
  keyValue.value = previous?.[9] ?? "";
  /** @param {boolean} [restoreGrantDraft] */
  function showLanes(restoreGrantDraft = false) {
    const grants = /** @type {const} */ ([
      [createFor, previous?.[4]],
      [connectFor, previous?.[5]],
    ]);
    for (const [control, draft] of grants) {
      const selectedIds = new Set(restoreGrantDraft ? draft?.split(",") : selected(control));
      setOptions(control, users, null);
      for (const option of control.options) option.selected = selectedIds.has(option.value);
    }
    const active = activeLanes(lanes);
    setOptions(after, active, after.value);
    setOptions(target, active, target.value);
    setOptions(
      keyLane,
      active.filter(({ role }) => role !== "completed"),
      keyLane.value,
    );
    setOptions(
      reviewLane,
      active.filter(({ role }) => role === "ready_for_review"),
      reviewLane.value,
    );
    setOptions(
      retireLane,
      active.filter(({ role }) => role !== "completed"),
      retireLane.value,
    );
    updateApprovalAction();
    retireForm.hidden = !retireLane.options.length;
    names.textContent =
      "Saved lanes: " +
      lanes
        .map(({ label, key, retired }) => label + " [" + key + "]" + (retired ? " (retired)" : ""))
        .join(", ");
  }

  const approvalEnabled = element("input");
  approvalEnabled.type = "checkbox";
  const bulkApprovalComment = element("textarea");
  bulkApprovalComment.placeholder = DEFAULT_APPROVAL_COMMENT;
  let savedLane = "";
  let savedEnabled = false;
  let savedComment = "";
  let displayedComment = "";

  function updateApprovalAction() {
    const lane = activeLanes(lanes).find(({ id }) => id === reviewLane.value);
    approvalForm.hidden = !reviewLane.options.length;
    if (
      lane?.id === savedLane &&
      (approvalEnabled.checked !== savedEnabled || bulkApprovalComment.value !== displayedComment)
    )
      return;
    savedLane = lane?.id ?? "";
    savedEnabled = lane?.approval_enabled ?? false;
    approvalEnabled.checked = savedEnabled;
    savedComment = lane?.bulk_approval_comment ?? "";
    bulkApprovalComment.value = savedComment;
    displayedComment = bulkApprovalComment.value;
    bulkApprovalComment.setCustomValidity("");
  }
  bulkApprovalComment.addEventListener("input", () => bulkApprovalComment.setCustomValidity(""));
  const approvalForm = form(
    "Save lane approval settings",
    [
      field("Review lane", reviewLane),
      field("Enable lane approval", approvalEnabled),
      field("Bulk approval comment", bulkApprovalComment),
    ],
    (submit) => {
      const lane = activeLanes(lanes).find(
        ({ id, role }) => id === reviewLane.value && role === "ready_for_review",
      );
      if (!lane) return;
      const enabled = approvalEnabled.checked;
      const display = bulkApprovalComment.value;
      const text = display === displayedComment ? savedComment : display;
      if ([...text].length > MAX_BULK_APPROVAL_COMMENT_LENGTH) {
        bulkApprovalComment.setCustomValidity(
          "Use up to " + MAX_BULK_APPROVAL_COMMENT_LENGTH + " characters.",
        );
        bulkApprovalComment.reportValidity();
        return;
      }
      void writeResource(
        submit,
        () =>
          api.updateLaneSettings(boardId, lane.id, lane.revision, boardTag(), {
            approval_enabled: enabled,
            bulk_approval_comment: text,
          }),
        async () => {
          await reloadBoard();
          const saved = lanes.find(({ id }) => id === lane.id);
          const committed =
            saved?.approval_enabled === enabled && (saved.bulk_approval_comment ?? "") === text;
          if (committed && savedLane === lane.id && approvalEnabled.checked === enabled) {
            savedEnabled = enabled;
            if (bulkApprovalComment.value === display) {
              savedComment = text;
              displayedComment = bulkApprovalComment.value;
            }
          }
          return committed;
        },
        "Lane approval settings saved.",
        inspection,
      );
    },
  );
  reviewLane.addEventListener("change", updateApprovalAction);
  const retireForm = form("Retire lane", [field("Lane to retire", retireLane)], (submit) => {
    const lane = activeLanes(lanes).find(
      ({ id, role }) => id === retireLane.value && role !== "completed",
    );
    if (!lane) return;
    void writeResource(
      submit,
      () => api.updateLaneSettings(boardId, lane.id, lane.revision, boardTag(), { retired: true }),
      async () => {
        await reloadBoard();
        return lanes.find(({ id }) => id === lane.id)?.retired === true;
      },
      "Lane retired.",
      inspection,
    );
  });

  /** @param {HTMLSelectElement} control */
  const selected = (control) => Array.from(control.selectedOptions, ({ value }) => value);
  showLanes(true);
  editor.append(
    element("summary", "Manage lanes"),
    names,
    form(
      "Add lane",
      [
        field("Lane label", addName),
        field("Insert after", after),
        field("Create rights", createFor),
        field("Connect rights", connectFor),
      ],
      (submit) => {
        if (!after.value) return;
        const request = {
          label: addName.value,
          after_id: after.value,
          create_for: selected(createFor),
          connect_for: selected(connectFor),
        };
        void writeResource(
          submit,
          () => api.createLane(boardId, boardTag(), request),
          reloadBoard,
          "Lane added.",
          inspection,
        );
      },
    ),
    form(
      "Rename lane",
      [field("Lane to rename", target), field("New lane label", renameName)],
      (submit) => {
        const lane = lanes.find(({ id }) => id === target.value);
        if (!lane) return;
        void writeResource(
          submit,
          () => api.renameLane(lane.id, lane.revision, boardTag(), renameName.value),
          reloadBoard,
          "Lane renamed.",
          inspection,
        );
      },
    ),
    form(
      "Change lane key",
      [field("Lane key target", keyLane), field("New lane key", keyValue)],
      (submit) => {
        const lane = lanes.find(({ id, role }) => id === keyLane.value && role !== "completed");
        if (!lane) return;
        const key = keyValue.value;
        void writeResource(
          submit,
          () => api.updateLaneSettings(boardId, lane.id, lane.revision, boardTag(), { key }),
          async () => {
            await reloadBoard();
            return (
              /** @type {Lane & {key?: string} | undefined} */ (
                lanes.find(({ id }) => id === lane.id)
              )?.key === key
            );
          },
          "Lane key changed.",
          inspection,
        );
      },
    ),
    approvalForm,
    retireForm,
  );
  return {
    editor,
    showLanes,
    retainDraft() {
      boardDrafts.set(draftKey, [
        addName.value,
        renameName.value,
        after.value,
        target.value,
        selected(createFor).join(","),
        selected(connectFor).join(","),
        reviewLane.value,
        retireLane.value,
        keyLane.value,
        keyValue.value,
      ]);
    },
  };
}

/** @param {string} boardId @param {Lane[]} lanes @param {(card: Card, opener: HTMLButtonElement) => void} open
 * @param {AbortSignal} signal */
function renderCardSearch(boardId, lanes, open, signal) {
  const view = activeView;
  const state = boardViewState(boardId);
  /** @param {unknown} value @returns {value is SearchFields} */
  function validFields(value) {
    if (!value || typeof value !== "object") return false;
    const fields = /** @type {Partial<SearchFields>} */ (value);
    return (
      typeof fields.query === "string" &&
      typeof fields.content === "boolean" &&
      (fields.order === "triage" || fields.order === "ticket") &&
      typeof fields.lane === "string" &&
      typeof fields.limit === "string"
    );
  }

  const saved = state.search;
  const fields = validFields(saved?.fields) ? saved.fields : undefined;
  const laneChoices = () => [{ id: "", label: "All lanes" }, ...activeLanes(lanes)];
  const choices = laneChoices();
  const panel = element("details");
  panel.className = "editor";
  panel.dataset.cardSearch = "true";
  panel.open = saved?.open === true;
  panel.append(element("summary", "Find cards"));
  const query = element("input");
  query.type = "search";
  query.value = fields?.query ?? "";
  const content = element("input");
  content.type = "checkbox";
  content.checked = fields?.content ?? false;
  const contentOption = field("Search descriptions and comments", content);
  contentOption.className = "search-content-option";
  const order = select(
    [
      { id: "triage", label: "Next work" },
      { id: "ticket", label: "Ticket number" },
    ],
    fields?.order ?? "triage",
  );
  const lane = select(
    choices,
    choices.some(({ id }) => id === fields?.lane) ? (fields?.lane ?? "") : "",
  );
  const limit = element("input");
  limit.type = "number";
  limit.min = "1";
  limit.max = "200";
  limit.value = fields?.limit ?? "20";
  limit.required = true;
  const results = element("div");
  results.setAttribute("aria-live", "polite");
  /** @param {...(Node|string)} children */
  function paintResults(...children) {
    const focused = document.activeElement;
    const cardId =
      focused instanceof HTMLButtonElement && results.contains(focused)
        ? focused.dataset.cardId
        : undefined;
    results.replaceChildren(...children);
    if (cardId && focused instanceof HTMLButtonElement) restoreCardFocus(cardId, focused);
  }
  let read = 0;
  let submitted = validFields(saved?.submitted) ? saved.submitted : undefined;
  /** @type {AbortController|undefined} */
  let inFlight;
  const isCurrent = () => !signal.aborted && view === activeView && panel.isConnected;
  /** @returns {SearchFields} */
  function controls() {
    return {
      query: query.value,
      content: content.checked,
      order: order.value === "ticket" ? "ticket" : "triage",
      lane: lane.value,
      limit: limit.value,
    };
  }
  function retain() {
    if (!isCurrent()) return;
    state.search = { open: panel.open, fields: controls(), submitted };
    persistBoardView(boardId);
  }
  /** @param {SearchFields} requested */
  function run(requested) {
    inFlight?.abort();
    const request = new AbortController();
    inFlight = request;
    const readSignal = AbortSignal.any([signal, request.signal]);
    const generation = ++read;
    const current = () => isCurrent() && generation === read;
    submitButton.disabled = true;
    const filters = {
      q: requested.query.trim() === "" ? undefined : requested.query,
      searchContent: requested.content,
      sort: requested.order,
      laneId: requested.lane || undefined,
      limit: Number(requested.limit),
    };
    void readWithSession(() => api.listCards(boardId, filters, readSignal), readSignal)
      .then((reply) => {
        if (!current()) return;
        const cards = /** @type {Card[]} */ (items(reply, "cards"));
        paintResults(
          ...(cards.length
            ? cards.map((card) => {
                const opener = button("#" + card.ticket + " " + card.title, () => {
                  if (current()) open(card, opener);
                });
                opener.dataset.cardId = card.id;
                return opener;
              })
            : ["No matching cards."]),
        );
      })
      .catch(() => {
        if (current()) paintResults("Cards could not be read. Submit the search again.");
      })
      .finally(() => {
        if (inFlight === request) inFlight = undefined;
        if (current()) submitButton.disabled = false;
      });
  }
  const searchForm = form(
    "Find cards",
    [
      field("Title or ticket", query),
      contentOption,
      field("Result order", order),
      field("Search lane", lane),
      field("Maximum results", limit),
    ],
    () => {
      if (!isCurrent()) return;
      submitted = controls();
      retain();
      run(submitted);
    },
  );
  const submitButton = /** @type {HTMLButtonElement} */ (searchForm.querySelector("button"));
  panel.append(searchForm, results);
  return {
    panel,
    retain,
    restore() {
      if (!isCurrent() || !submitted) return;
      const count = Number(submitted.limit);
      if (
        Number.isInteger(count) &&
        count >= 1 &&
        count <= 200 &&
        choices.some(({ id }) => id === submitted?.lane)
      ) {
        run(submitted);
      } else {
        submitted = undefined;
        retain();
      }
    },
    /** @param {boolean} changed */
    refreshBoard(changed) {
      const selectedLane = lane.value;
      const choices = laneChoices();
      setOptions(lane, choices, choices.some(({ id }) => id === selectedLane) ? selectedLane : "");
      if (!changed || !submitted) return;
      submitted = undefined;
      ++read;
      inFlight?.abort();
      paintResults("Board changed. Run the search again for current results.");
      submitButton.disabled = false;
      retain();
    },
  };
}

/** @param {string} value @param {Card[]} cards */
function ticketSelection(value, cards) {
  const tickets = value.split(/[\s,]+/);
  if (!tickets.length || tickets.some((ticket) => !/^[1-9][0-9]*$/.test(ticket))) throw new Error();
  const selected = tickets.map((ticket) => cards.find((card) => card.ticket === Number(ticket)));
  if (selected.some((card) => !card) || new Set(selected).size !== selected.length)
    throw new Error();
  return /** @type {Card[]} */ (selected);
}

/** @param {string} boardId @param {() => string} boardTag */
function renderBackupControls(boardId, boardTag) {
  const state = boardBackups.get(boardId) ?? {};
  boardBackups.set(boardId, state);
  const panel = element("div");
  const current = () => activeBackupControls?.panel === panel && panel.isConnected;
  const captureStatus = element("p");
  const backupSummary = element("p");
  const downloadStatus = element("p");
  const publicationStatus = element("p");
  for (const status of [captureStatus, backupSummary, downloadStatus, publicationStatus]) {
    status.setAttribute("aria-live", "polite");
  }

  function paintCurrent() {
    const controls = activeBackupControls;
    if (controls?.boardId === boardId && controls.panel.isConnected) controls.paint();
  }

  /** @param {() => Promise<unknown>} request */
  async function submitOnce(request) {
    let submittingCapability = capability;
    try {
      if (!capability) await ensureCapability();
      submittingCapability = capability;
      await request();
      return "ready";
    } catch (error) {
      return writePhase(error, submittingCapability);
    }
  }

  /** @param {BackupOperation} operation */
  async function readCapture(operation) {
    try {
      const reply = await readWithSession(() => api.getBackup(operation.id, boardId));
      state.backup = /** @type {CapturedBackup} */ (reply.data);
      state.capture = undefined;
      state.captureMessage = "";
    } catch {
      // A 404 can precede the reservation or publication of this capture.
      state.captureMessage =
        "Backup capture could not be confirmed. Refresh status to check the original capture.";
    } finally {
      operation.busy = false;
      paintCurrent();
    }
  }

  async function createCapture() {
    const expected = boardTag();
    const operation = { id: newOperationId(), busy: true };
    state.capture = operation;
    state.captureMessage = "Capturing backup…";
    paintCurrent();
    const phase = await submitOnce(() => api.createBackup(operation.id, boardId, expected));
    if (phase === "ready" || phase === "unknown") {
      await readCapture(operation);
      return;
    }
    state.capture = undefined;
    state.captureMessage =
      phase === "conflict"
        ? "The board changed before capture. Refresh the board before creating another backup."
        : "The service refused to create the backup.";
    paintCurrent();
  }

  const capture = button("Create backup", () => {
    if (current() && !state.capture) void createCapture();
  });
  const refreshCapture = button("Refresh backup status", () => {
    const operation = state.capture;
    if (!current() || !operation || operation.busy) return;
    operation.busy = true;
    state.captureMessage = "Checking backup status…";
    paintCurrent();
    void readCapture(operation);
  });

  const download = button("Download backup", () => {
    const backup = state.backup;
    if (!current() || !backup || state.downloading) return;
    state.downloading = true;
    state.downloadMessage = "";
    paintCurrent();
    void readWithSession(() => api.getBackupContent(backup.id, backup.size, backup.sha2_256))
      .then((blob) => {
        const url = URL.createObjectURL(blob);
        const anchor = element("a");
        anchor.href = url;
        anchor.download = "localswim-" + boardId + "-r" + backup.board_revision + ".zip";
        anchor.click();
        setTimeout(() => URL.revokeObjectURL(url), 1000);
      })
      .catch(() => {
        state.downloadMessage = "Backup download could not be verified. Try downloading again.";
      })
      .finally(() => {
        state.downloading = false;
        paintCurrent();
      });
  });
  download.id = "download-backup";

  /** @param {BackupPublication} operation */
  async function readPublication(operation) {
    try {
      const reply = await readWithSession(() =>
        api.getPublication(operation.id, boardId, operation.backupId),
      );
      const item = /** @type {{state: string, error_code: string|null}} */ (reply.data);
      state.publicationMessage =
        "Publication " +
        item.state +
        " for revision " +
        operation.boardRevision +
        (item.error_code ? " (" + item.error_code + ")" : "") +
        ".";
      if (item.state === "succeeded" || item.state === "failed") state.publication = undefined;
    } catch {
      state.publicationMessage =
        "Publication status for revision " +
        operation.boardRevision +
        " could not be read. Refresh status before publishing again.";
    } finally {
      operation.busy = false;
      paintCurrent();
    }
  }

  /** @param {CapturedBackup} backup */
  async function startPublication(backup) {
    const operation = {
      id: newOperationId(),
      backupId: backup.id,
      boardRevision: backup.board_revision,
      busy: true,
    };
    state.publication = operation;
    state.publicationMessage =
      "Submitting publication for revision " + operation.boardRevision + "…";
    paintCurrent();
    const phase = await submitOnce(() =>
      api.publishBackup(operation.id, boardId, operation.backupId),
    );
    if (phase === "ready" || phase === "unknown") {
      await readPublication(operation);
      return;
    }
    state.publication = undefined;
    state.publicationMessage =
      "The service refused to publish revision " + operation.boardRevision + ".";
    paintCurrent();
  }

  const publish = button("Publish backup", () => {
    if (current() && state.backup && !state.publication) void startPublication(state.backup);
  });
  const refreshPublication = button("Refresh publication status", () => {
    const operation = state.publication;
    if (!current() || !operation || operation.busy) return;
    operation.busy = true;
    state.publicationMessage =
      "Checking publication status for revision " + operation.boardRevision + "…";
    paintCurrent();
    void readPublication(operation);
  });

  function paint() {
    const backup = state.backup;
    capture.disabled = Boolean(state.capture);
    refreshCapture.hidden = !state.capture;
    refreshCapture.disabled = state.capture?.busy === true;
    captureStatus.textContent = state.captureMessage ?? "";
    backupSummary.textContent = backup
      ? "Captured revision " +
        backup.board_revision +
        " with " +
        backup.attachment_ids.length +
        (backup.attachment_ids.length === 1 ? " attachment file." : " attachment files.")
      : "";
    download.hidden = !backup;
    download.disabled = state.downloading === true;
    downloadStatus.textContent = state.downloadMessage ?? "";
    publish.hidden = !backup;
    publish.disabled = Boolean(state.publication);
    refreshPublication.hidden = !state.publication;
    refreshPublication.disabled = state.publication?.busy === true;
    publicationStatus.textContent = state.publicationMessage ?? "";
  }

  panel.append(
    capture,
    refreshCapture,
    captureStatus,
    backupSummary,
    download,
    downloadStatus,
    publish,
    refreshPublication,
    publicationStatus,
  );
  activeBackupControls = { boardId, panel, paint };
  paint();
  return panel;
}

/** @param {string} boardId @param {Labeled[]} priorities @param {() => Card[]} cards
 * @param {() => string} boardTag @param {() => Promise<void>} reloadBoard @param {Inspection} inspection */
function renderBoardActions(boardId, priorities, cards, boardTag, reloadBoard, inspection) {
  const panel = element("details");
  panel.className = "editor";
  panel.append(element("summary", "Board actions"));
  const tickets = element("input");
  tickets.id = "batch-priority-tickets";
  tickets.required = true;
  tickets.placeholder = "1, 2, 3";
  const priority = select(priorities, null);
  priority.id = "batch-priority-value";
  priority.required = true;
  const result = element("div");
  result.setAttribute("aria-live", "polite");
  panel.append(
    form(
      "Set batch priority",
      [field("Ticket numbers", tickets), field("Priority", priority)],
      (submit) => {
        const requestedTickets = tickets.value;
        const requestedPriority = priority.value;
        const requestedTag = boardTag();
        /** @type {{cards: Card[], changes: {resource_id: string, before: string, after: string}[]} | undefined} */
        let saved;
        void writeResource(
          submit,
          async () => {
            const allCards = /** @type {Card[]} */ (
              await collect(
                (cursor) => api.listCards(boardId, { cursor, archived: "include" }),
                "cards",
              )
            );
            let selected;
            try {
              selected = ticketSelection(requestedTickets, allCards);
            } catch {
              result.textContent = "Enter distinct ticket numbers from this board.";
              throw new ApiError("invalid_target");
            }
            const selectedIds = selected.map(({ id }) => id);
            requireAttachmentUploadsSettled(boardId, selectedIds);
            const reply = await api.setPriorities(
              boardId,
              requestedTag,
              selectedIds,
              requestedPriority,
            );
            saved =
              /** @type {{cards: Card[], changes: {resource_id: string, before: string, after: string}[]}} */ (
                reply.data
              );
          },
          async () => {
            await reloadBoard();
            if (!saved) return false;
            result.replaceChildren(
              element("p", saved.changes.length + " priority changes committed."),
            );
            for (const card of saved.cards)
              result.append(
                element(
                  "p",
                  "#" +
                    card.ticket +
                    " " +
                    (priorities.find(({ id }) => id === card.priority_id)?.label ??
                      card.priority_id),
                ),
              );
            for (const change of saved.changes) {
              const changedCard =
                cards().find(({ id }) => id === change.resource_id) ??
                saved.cards.find(({ id }) => id === change.resource_id);
              result.append(
                element(
                  "p",
                  (changedCard ? "#" + changedCard.ticket : change.resource_id) +
                    " " +
                    (priorities.find(({ id }) => id === change.before)?.label ?? change.before) +
                    " → " +
                    (priorities.find(({ id }) => id === change.after)?.label ?? change.after),
                ),
              );
            }
            return true;
          },
          "Batch priorities saved.",
          inspection,
        );
      },
    ),
    result,
  );
  panel.append(renderBackupControls(boardId, boardTag));
  const restore = element("input");
  restore.type = "file";
  restore.accept = ".zip,application/zip";
  restore.required = true;
  panel.append(
    form("Restore native backup", [field("Native backup ZIP", restore)], (submit) => {
      const file = restore.files?.[0];
      if (!file) return;
      const requestedTag = boardTag();
      let restored = false;
      void writeResource(
        submit,
        async () => {
          requireAttachmentUploadsSettled(boardId);
          const snapshot = await readWithSession(() => api.getSnapshot(boardId));
          const captured = /** @type {{board?: {id?: string, revision?: number}}} */ (snapshot.data)
            .board;
          if (
            captured?.id !== boardId ||
            typeof captured.revision !== "number" ||
            requestedTag !== JSON.stringify(boardId + "-" + captured.revision)
          )
            throw new ApiError("http_error", { status: 412 });
          try {
            await api.restoreSnapshot(
              boardId,
              etag(snapshot),
              new Blob([file], { type: "application/zip" }),
            );
          } catch (error) {
            if (error instanceof ApiError && error.status === 413)
              throw new ApiError("snapshot_too_large", { status: 413 });
            throw error;
          }
          restored = true;
        },
        async () => {
          await reloadBoard();
          return restored;
        },
        "Native backup restored.",
        inspection,
      );
    }),
  );
  return panel;
}
/** @param {HTMLElement} sidebar @param {HTMLElement} boards @param {HTMLElement} drawer
 * @param {HTMLElement} toggle @param {HTMLElement} separator @param {HTMLElement} layout */
function setupSidebar(sidebar, boards, drawer, toggle, separator, layout) {
  const storageKey = "localswim:attention-sidebar";
  const minimumWidth = 312;
  const boardReserve = 256;
  let preferredWidth = 336;
  let collapsed = false;
  try {
    /** @type {unknown} */
    const saved = JSON.parse(window.sessionStorage.getItem(storageKey) ?? "null");
    if (saved && typeof saved === "object") {
      const { width, collapsed: savedCollapsed } = /** @type {Record<string, unknown>} */ (saved);
      if (typeof width === "number" && Number.isSafeInteger(width) && width > 0)
        preferredWidth = width;
      collapsed = savedCollapsed === true;
    }
  } catch {
    // Sidebar controls remain available if tab storage is inaccessible.
  }
  let minimum = minimumWidth;
  let maximum = preferredWidth;
  /** @type {{pointer: number, startX: number, startWidth: number, width: number}|undefined} */
  let drag;

  function save() {
    try {
      window.sessionStorage.setItem(
        storageKey,
        JSON.stringify({ width: preferredWidth, collapsed }),
      );
    } catch {
      // Keep the new setting in memory for this page.
    }
  }

  /** @param {number} width */
  function clamp(width) {
    return Math.max(minimum, Math.min(maximum, Math.round(width)));
  }

  function paintWidth() {
    const width = clamp(drag?.width ?? preferredWidth);
    sidebar.style.setProperty("--sidebar-width", width + "px");
    separator.setAttribute("aria-valuemin", String(minimum));
    separator.setAttribute("aria-valuemax", String(maximum));
    separator.setAttribute("aria-valuenow", String(width));
    separator.setAttribute("aria-valuetext", width + " pixels");
  }

  function measure() {
    const style = window.getComputedStyle(layout);
    const available = Math.floor(
      layout.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight),
    );
    minimum = Math.max(0, Math.min(minimumWidth, available));
    if (style.flexDirection === "column") {
      maximum = Math.max(minimum, available);
    } else {
      const gap = parseFloat(style.columnGap);
      const drawerWidth = drawer.hidden ? 0 : drawer.getBoundingClientRect().width + gap;
      maximum = Math.max(minimum, Math.floor(available - drawerWidth - gap - boardReserve));
    }
    paintWidth();
  }

  function paintVisibility() {
    sidebar.dataset.collapsed = String(collapsed);
    boards.hidden = collapsed;
    separator.hidden = collapsed;
    toggle.textContent = collapsed ? "Show Attention" : "Hide Attention";
    toggle.setAttribute("aria-expanded", String(!collapsed));
  }

  function toggleVisibility() {
    if (drag) {
      const { pointer } = drag;
      drag = undefined;
      if (separator.hasPointerCapture(pointer)) separator.releasePointerCapture(pointer);
      paintWidth();
    }
    collapsed = !collapsed;
    if (
      collapsed &&
      (boards.contains(document.activeElement) || document.activeElement === separator)
    ) {
      toggle.focus({ preventScroll: true });
    }
    paintVisibility();
    save();
  }

  toggle.addEventListener("click", toggleVisibility);
  separator.addEventListener("keydown", (event) => {
    if (drag || event.isComposing) return;
    if (event.key === "Enter") {
      event.preventDefault();
      toggleVisibility();
      return;
    }
    const width = clamp(preferredWidth);
    let next;
    switch (event.key) {
      case "ArrowLeft":
        next = width - 16;
        break;
      case "ArrowRight":
        next = width + 16;
        break;
      case "Home":
        next = minimum;
        break;
      case "End":
        next = maximum;
        break;
      default:
        return;
    }
    event.preventDefault();
    const resized = clamp(next);
    if (resized === width) return;
    preferredWidth = resized;
    paintWidth();
    save();
  });

  separator.addEventListener("pointerdown", (event) => {
    if (drag || !event.isPrimary || event.button !== 0) return;
    const width = clamp(preferredWidth);
    separator.setPointerCapture(event.pointerId);
    drag = { pointer: event.pointerId, startX: event.clientX, startWidth: width, width };
    separator.focus({ preventScroll: true });
    event.preventDefault();
  });
  separator.addEventListener("pointermove", (event) => {
    if (drag?.pointer !== event.pointerId) return;
    drag.width = clamp(drag.startWidth + event.clientX - drag.startX);
    paintWidth();
  });
  separator.addEventListener("pointerup", (event) => {
    if (drag?.pointer !== event.pointerId) return;
    const width = clamp(drag.startWidth + event.clientX - drag.startX);
    if (width !== clamp(drag.startWidth)) {
      preferredWidth = width;
      save();
    }
    drag = undefined;
    paintWidth();
  });
  /** @param {PointerEvent} event */
  function cancelResize(event) {
    if (drag?.pointer !== event.pointerId) return;
    drag = undefined;
    paintWidth();
  }
  separator.addEventListener("pointercancel", cancelResize);
  separator.addEventListener("lostpointercapture", cancelResize);

  toggle.hidden = false;
  paintVisibility();
  measure();
  const bounds = new ResizeObserver(measure);
  bounds.observe(layout);
  bounds.observe(drawer);
}

const sidebar = document.getElementById("board-sidebar");
const toggleSidebar = document.getElementById("toggle-board-sidebar");
const resizeSidebar = document.getElementById("resize-board-sidebar");
if (sidebar?.parentElement && toggleSidebar && resizeSidebar) {
  setupSidebar(sidebar, navigation, details, toggleSidebar, resizeSidebar, sidebar.parentElement);
}

document.querySelector("header a")?.addEventListener("click", (event) => {
  if (event instanceof MouseEvent) followLink(event, "/");
});
refresh.addEventListener("click", () => {
  if (writing) return;
  if (retrySelectedCard) {
    retrySelectedCard();
    return;
  }
  const inspection = cardInspection ?? boardInspection;
  if (inspection) inspection.inspect();
  else if (activeBoardRefresh) void activeBoardRefresh.request().catch(() => {});
  else void load();
});
details.addEventListener("paste", (event) => activeDrawer?.pasteAttachments(event));
window.addEventListener("keydown", (event) => {
  if (
    event.defaultPrevented ||
    event.isComposing ||
    event.repeat ||
    event.key !== "/" ||
    event.altKey ||
    event.ctrlKey ||
    event.metaKey
  )
    return;
  const target = event.target;
  if (
    target instanceof HTMLInputElement ||
    target instanceof HTMLTextAreaElement ||
    target instanceof HTMLSelectElement ||
    (target instanceof HTMLElement && target.isContentEditable)
  )
    return;
  const filter = main?.querySelector("#board-filter");
  if (!(filter instanceof HTMLInputElement)) return;
  event.preventDefault();
  filter.focus();
  filter.select();
});
window.addEventListener("popstate", () => void load());
window.addEventListener("pagehide", () => {
  retainCardDraft?.();
  retainNewCardDraft?.();
  retainBoardView?.();
});
void load();
