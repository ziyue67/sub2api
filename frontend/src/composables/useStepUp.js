/**
 * Step-up (sudo) 2FA composable.
 *
 * Wraps a sensitive admin action so that when the backend responds with a
 * STEP_UP_REQUIRED error, the caller can prompt for a TOTP code, obtain a
 * short-lived grant, and transparently retry the original action.
 *
 * Usage in a view:
 *   const stepUp = useStepUp()
 *   async function exportData() {
 *     await stepUp.run(() => adminAPI.accounts.exportData(...))
 *   }
 *   // template: <TotpStepUpDialog :controller="stepUp" />
 */
import { ref } from 'vue';
/** Error codes the backend uses to signal step-up state. */
const STEP_UP_REQUIRED = 'STEP_UP_REQUIRED';
const STEP_UP_TOTP_NOT_ENABLED = 'STEP_UP_TOTP_NOT_ENABLED';
const STEP_UP_ADMIN_API_KEY_FORBIDDEN = 'STEP_UP_ADMIN_API_KEY_FORBIDDEN';
/**
 * Thrown by run() when the user dismisses the TOTP dialog.
 * Callers should treat it as a silent no-op, not an error to toast.
 */
export class StepUpCancelledError extends Error {
    constructor() {
        super('step-up verification cancelled by user');
        Object.defineProperty(this, "code", {
            enumerable: true,
            configurable: true,
            writable: true,
            value: 'STEP_UP_CANCELLED'
        });
        this.name = 'StepUpCancelledError';
    }
}
export function isStepUpCancelled(err) {
    return err instanceof StepUpCancelledError;
}
/** Extract the semantic error marker from either envelope shape (code or reason). */
function markerOf(err) {
    const e = (err ?? {});
    const candidates = [e.code, e.reason].map((v) => (typeof v === 'string' ? v : ''));
    return candidates.find((v) => v.startsWith('STEP_UP')) || '';
}
export function isStepUpRequired(err) {
    return markerOf(err) === STEP_UP_REQUIRED;
}
export function isStepUpBlocked(err) {
    const m = markerOf(err);
    return m === STEP_UP_TOTP_NOT_ENABLED || m === STEP_UP_ADMIN_API_KEY_FORBIDDEN;
}
export function stepUpBlockReason(err) {
    return markerOf(err);
}
export function useStepUp() {
    const visible = ref(false);
    const blockedReason = ref('');
    let resolver = null;
    let pendingPrompt = null;
    /** Open the TOTP dialog and resolve true once a grant is obtained. */
    function prompt() {
        if (pendingPrompt)
            return pendingPrompt;
        visible.value = true;
        pendingPrompt = new Promise((resolve) => {
            resolver = resolve;
        });
        return pendingPrompt;
    }
    function onVerified() {
        visible.value = false;
        resolver?.(true);
        resolver = null;
        pendingPrompt = null;
    }
    function onCancel() {
        visible.value = false;
        resolver?.(false);
        resolver = null;
        pendingPrompt = null;
    }
    /**
     * Run a sensitive action. On STEP_UP_REQUIRED, prompt for a TOTP code and
     * retry once. STEP_UP_TOTP_NOT_ENABLED / admin-api-key errors are surfaced
     * to the caller (they cannot be resolved by entering a code). If the user
     * cancels the prompt, a StepUpCancelledError is thrown so callers can
     * distinguish "user changed their mind" from real failures.
     */
    async function run(action) {
        try {
            return await action();
        }
        catch (err) {
            if (isStepUpBlocked(err)) {
                blockedReason.value = markerOf(err);
                throw err;
            }
            if (!isStepUpRequired(err)) {
                throw err;
            }
            const ok = await prompt();
            if (!ok) {
                throw new StepUpCancelledError();
            }
            // Retry once now that the session holds a step-up grant.
            return await action();
        }
    }
    return {
        visible,
        blockedReason,
        prompt,
        onVerified,
        onCancel,
        run
    };
}
