const boardID = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
export function parseRoute(pathname) {
    if (pathname === "/")
        return { kind: "workspace" };
    const id = /^\/boards\/([^/]+)$/.exec(pathname)?.[1];
    return id && boardID.test(id) ? { kind: "board", id: id.toLowerCase() } : { kind: "missing" };
}
export function parseBoardPage(payload) {
    if (!("boards" in payload) || !Array.isArray(payload.boards) || !("next_cursor" in payload)) {
        throw new TypeError("Invalid board catalog.");
    }
    const nextCursor = payload.next_cursor;
    if (nextCursor !== null && (typeof nextCursor !== "string" || nextCursor.length === 0)) {
        throw new TypeError("Invalid board catalog cursor.");
    }
    const boards = payload.boards.map((entry) => {
        if (typeof entry !== "object" || entry === null
            || !("id" in entry) || typeof entry.id !== "string" || !boardID.test(entry.id)
            || !("name" in entry) || (entry.name !== null && typeof entry.name !== "string")) {
            throw new TypeError("Invalid board catalog entry.");
        }
        return { id: entry.id.toLowerCase(), name: entry.name };
    });
    return { boards, nextCursor };
}
