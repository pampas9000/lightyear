/**
 * Unified API response envelope from the backend.
 * Mirrors: server/internal/api/response/response.go
 */
export interface ApiResponse<T> {
    success: boolean;
    code: string;
    message: string;
    data: T;
    details?: unknown;
}
