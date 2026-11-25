/**
 * Unified API response envelope from the backend.
 * Mirrors: server/internal/api/response/response.go
 */
export interface ApiResponse<T> {
    success: boolean;
    code: string;
    message: string;
    data: T;
    /** Machine-readable error context; null when there is none. */
    details: unknown;
}

/**
 * The data of every list response: one page, newest first, and the total
 * across pages. Lists take `?page=` (from 1) and `?limit=` (1-100, default 20).
 * Mirrors: server/internal/api/response/list.go
 */
export interface ListData<T> {
    items: T[];
    page: number;
    limit: number;
    total: number;
}
