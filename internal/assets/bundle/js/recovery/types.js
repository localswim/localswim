export class RecoveryError extends Error {
    code;
    constructor(code) {
        super(`Recovery ${code}; retain the caller's input and original identity.`);
        this.code = code;
        this.name = "RecoveryError";
    }
}
