// Every TanStack Query key in one place. Keys are nested so a prefix
// invalidates a whole family: `queryKeys.tasks.all` covers lists, details
// and stats.

export interface TaskListParams {
    page: number;
    limit: number;
    /** A TaskStatus, or undefined for every status. */
    status?: string;
}

export const queryKeys = {
    tasks: {
        all: ["tasks"] as const,
        lists: () => ["tasks", "list"] as const,
        list: (params: TaskListParams) => ["tasks", "list", params] as const,
        details: () => ["tasks", "detail"] as const,
        detail: (id: string) => ["tasks", "detail", id] as const,
        stats: () => ["tasks", "stats"] as const,
    },
    drafts: {
        all: ["drafts"] as const,
    },
    workflows: {
        all: ["workflows"] as const,
    },
    apiKeys: {
        all: ["api-keys"] as const,
    },
    usage: {
        all: ["usage"] as const,
    },
};
