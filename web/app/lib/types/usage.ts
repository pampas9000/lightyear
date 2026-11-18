/**
 * A user's usage in one calendar month (UTC) and their quotas.
 * Mirrors: server/internal/services/usage.Summary (GET /api/usage, GET /v1/usage).
 * A unit is one encode round: an ordinary convert is 1, a target search
 * encodes several times.
 */
export interface UsageSummary {
    /** "YYYY-MM" */
    month: string;
    period_start: string;
    period_end: string;
    units: {
        used: number;
        /** Estimated units held by unfinished jobs (current month only). */
        reserved: number;
        /** null means unlimited. */
        limit: number | null;
    };
    storage: {
        used_bytes: number;
        /** null means unlimited. */
        limit_bytes: number | null;
    };
    totals: {
        outputs: number;
        reused_outputs: number;
        failed_attempts: number;
        encode_rounds: number;
        compute_ms: number;
        output_bytes: number;
        output_pixels: number;
        fetched_bytes: number;
    };
    daily: UsageDay[];
}

export interface UsageDay {
    /** "YYYY-MM-DD" (UTC) */
    date: string;
    units: number;
    outputs: number;
    reused_outputs: number;
    failed_attempts: number;
}
