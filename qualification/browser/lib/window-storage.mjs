// Qualification-only observation of the exact standalone privacy journey.
// Window metadata may reference its current recovery draft, never carry prose,
// base snapshots, operations, credentials or arbitrary persisted UI properties.
const uuidV4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
export function safeStandaloneWindowStorage(entries, { context, draftId, forbiddenValues }) {
  if (!Array.isArray(entries) || entries.length !== 1 || !Array.isArray(entries[0]) ||
      entries[0].length !== 2 || entries[0][0] !== 'localswim.window.v1') return false;
  const raw = entries[0][1];
  if (typeof raw !== 'string' || raw.length > 65_536 || !raw.length ||
      !Array.isArray(forbiddenValues) || forbiddenValues.some(value => typeof value !== 'string' || !value.length || raw.includes(value))) return false;
  if (!context || Object.keys(context).sort().join() !== 'actorId,boardId,incarnation,installationId' ||
      !Object.values(context).every(value => typeof value === 'string' && uuidV4.test(value)) ||
      typeof draftId !== 'string' || !uuidV4.test(draftId)) return false;
  let value;
  try { value = JSON.parse(raw); } catch { return false; }
  if (!value || typeof value.id !== 'string' || !uuidV4.test(value.id)) return false;
  const expected = { id: value.id, boards: {
    ['standalone:' + context.boardId + ':' + context.incarnation]: {
      editor: { context: { installationId: context.installationId, boardId: context.boardId,
        incarnation: context.incarnation, actorId: context.actorId }, draftId },
    },
  }, layout: { collapsed: false, width: 280 } };
  // The product emits this exact canonical JSON. Equality also rejects duplicate
  // keys, extra fields, non-finite values, unknown boards and hidden raw strings.
  return raw === JSON.stringify(expected);
}
