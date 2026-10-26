import { BoardView } from "../board/view.js";
import { RecoveryView, element } from "../recovery-ui/view.js";
import { RecoveryAdminView } from "../recovery-ui/admin.js";
import { ArchiveView } from "../archive-ui/view.js";
// Composition stays separate from entry/assets. No automatic original lookup,
// recovery confirmation, attachment replay, export, restore, or native bootstrap.
export class SessionWorkspace {
    root;
    board;
    recovery;
    admin;
    archive;
    constructor(root, client, session) {
        this.root = root;
        const board = element("section"), recovery = element("section"), admin = element("section"), archive = element("section");
        root.replaceChildren(board, recovery, admin, archive);
        this.board = new BoardView(board, client, session.grant.boardId);
        const observe = (outcome) => this.board.observeOutcome(outcome);
        this.recovery = new RecoveryView(recovery, client, observe);
        this.admin = new RecoveryAdminView(admin, client, session.grant.boardId, observe);
        this.archive = new ArchiveView(archive, client, session.grant.boardId, observe);
        void this.board.open();
        void this.recovery.open();
    }
    destroy() { this.board.destroy(); this.recovery.destroy(); this.admin.destroy(); this.archive.destroy(); this.root.replaceChildren(); }
}
