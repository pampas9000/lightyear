import type { ApiResponse } from "~/lib/types/api";
import { useAuth } from "./useAuth";

/**
 * Typed API composable.
 *
 * Creates a `$fetch` instance pre-configured with the API base URL and
 * automatic 401 handling. All backend endpoints return an `ApiResponse<T>`
 * envelope, so consumers should type the generic as:
 *
 *   api<ApiResponse<MyData>>('/endpoint')
 */
export interface ApiError {
    userMessage: string;
    status: number;
    code: string;
    raw: unknown;
}

/**
 * Parses an error from FetchError or generic Error into a structured ApiError.
 * Logs details to console.error but keeps the userMessage clean of system/DB level details.
 */
export const parseApiError = (err: unknown): ApiError => {
    let userMessage = 'An unexpected error occurred';
    let status = 0;
    let code = 'error.unknown';

    if (!err || typeof err !== 'object') {
        return { userMessage, status, code, raw: err };
    }

    const fetchErr = err as Record<string, any>;
    status = fetchErr.statusCode || fetchErr.status || 0;

    const data = fetchErr.data as Record<string, any> | undefined;
    if (data && typeof data === 'object') {
        if (data.code) {
            code = String(data.code);
        }
        if (data.message) {
            userMessage = String(data.message);
        }
        
        // Log detailed/machine-readable details to console.error
        if (data.details) {
            console.error('[API Error Details]', {
                code,
                message: data.message,
                details: data.details,
            });
        }
    } else {
        console.error('[API Error Raw]', err);
    }

    const friendlyMessages: Record<string, string> = {
        'error.request.invalid': 'The request is invalid. Please check your input.',
        'error.body.invalid': 'The request body could not be parsed.',
        'error.param.invalid': 'One or more parameters are invalid.',
        'error.param.required': 'A required parameter is missing.',
        'error.param.malformed': 'The parameters are malformed.',
        'error.auth.unauthorized': 'You are not logged in. Please sign in.',
        'error.auth.forbidden': 'You do not have permission to perform this action.',
        'error.auth.invalid_credentials': 'The username or password you entered is incorrect.',
        'error.job.not_found': 'The requested job was not found.',
        'error.user.already_exists': 'A user with this email or username already exists.',
        'error.queue.enqueue_failed': 'Failed to queue the transcoding task. Please try again.',
        'error.database.unavailable': 'The database is temporarily unavailable. Please try again later.',
        'error.queue.unavailable': 'The processing queue is temporarily offline. Please try again later.',
        'error.internal': 'A server-side error occurred. Please try again.',
        'error.gateway': 'The server is temporarily unreachable. Please check your connection.',
        'error.not_found': 'The requested resource was not found.',
    };

    // If there is no backend-supplied message, try to map from the code or status
    if (!data || !data.message) {
        if (code && friendlyMessages[code]) {
            userMessage = friendlyMessages[code];
        } else {
            // Fallback for standard HTTP status codes
            if (status === 401) {
                code = 'error.auth.unauthorized';
                userMessage = friendlyMessages[code];
            } else if (status === 403) {
                code = 'error.auth.forbidden';
                userMessage = friendlyMessages[code];
            } else if (status === 404) {
                code = 'error.not_found';
                userMessage = friendlyMessages[code];
            } else if (status === 500) {
                code = 'error.internal';
                userMessage = friendlyMessages[code];
            } else if (status >= 502 && status <= 504) {
                code = 'error.gateway';
                userMessage = friendlyMessages[code];
            } else if (fetchErr.message) {
                // Clean up FetchError message formatting prefix if present
                let cleanMsg = String(fetchErr.message);
                if (cleanMsg.startsWith('[') && cleanMsg.includes(']')) {
                    const colonIdx = cleanMsg.lastIndexOf(':');
                    if (colonIdx !== -1 && colonIdx < cleanMsg.length - 1) {
                        cleanMsg = cleanMsg.substring(colonIdx + 1).trim();
                    }
                }
                userMessage = cleanMsg || 'An unexpected error occurred';
            }
        }
    }

    return {
        userMessage,
        status,
        code,
        raw: err,
    };
};

export const useApi = () => {
    const apiFetch = $fetch.create({
        baseURL: "/api",
        async onResponseError({ response }) {
            if (response.status === 401) {
                const { logout, showAuthModal } = useAuth();
                logout();
                showAuthModal.value = true;
            }
        },
    });

    return apiFetch;
};
