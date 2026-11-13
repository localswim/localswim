import type { ConfirmedApproval } from "./approve-lane.js";
import type { ConfirmedTicketOrder } from "./ticket-order.js";
import type { BoardData } from "../contracts.js";

export function applyConfirmedBoardWrite(
  base: BoardData,
  receipt: ConfirmedApproval | ConfirmedTicketOrder,
): BoardData | null {
  const { board, cards, lanes } = base.view;
  if (base.boardEtag !== receipt.baseTag || board.revision + 1 !== receipt.revision) return null;

  if ("assignments" in receipt) {
    const numbers = new Map(receipt.assignments.map(({ card_id, ticket }) => [card_id, ticket]));
    const known = cards.filter(({ id }) => numbers.has(id));
    if (known.length !== receipt.assignments.length) return null;
    return {
      ...base,
      view: {
        ...base.view,
        board: { ...board, revision: receipt.revision },
        cards: cards.map((card) => {
          const ticket = numbers.get(card.id);
          return ticket === undefined || ticket === card.ticket
            ? card
            : { ...card, ticket, revision: receipt.revision };
        }),
      },
    };
  }

  const approved = new Set(receipt.cardIds);
  const source = cards.filter(
    ({ id, lane_id }) => approved.has(id) && lane_id === receipt.fromLaneId,
  );
  if (
    source.length !== approved.size ||
    !lanes.some(({ id, role }) => id === receipt.toLaneId && role === "completed")
  )
    return null;
  return {
    ...base,
    view: {
      ...base.view,
      board: {
        ...board,
        revision: receipt.revision,
        updated_at: receipt.at,
        last_card_edit_at: receipt.at,
        attention: {
          ...board.attention,
          ready_for_review: Math.max(0, board.attention.ready_for_review - approved.size),
        },
      },
      cards: cards.map((card) =>
        approved.has(card.id)
          ? {
              ...card,
              lane_id: receipt.toLaneId,
              completed_at: receipt.at,
              updated_at: receipt.at,
              revision: receipt.revision,
              comment_count: card.comment_count + 1,
            }
          : card,
      ),
    },
  };
}
