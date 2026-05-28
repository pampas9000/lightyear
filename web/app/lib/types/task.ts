export const enum TaskStatus {
    PENDING = "PENDING",
    PROCESSING = "PROCESSING",
    COMPLETED = "COMPLETED",
    FAILED = "FAILED",
    PARTIALLY_FAILED = "PARTIALLY_FAILED",
}

export interface Base {
    id: string;
    created_at: string;
    updated_at: string;
}

export interface TranscodeParams {
    engine: string;
    engine_params: Record<string, any>;
}

export interface File extends Base {
    name: string;
    path: string;
    size: number;
    mime_type: string;
    status: string;
    owner_id: string;
}

export interface Job extends Base {
    status: TaskStatus;
    input_path: string;
    output_path: string;
    target_format: string;
    params: TranscodeParams;
    progress: number;
    error_message?: string;
    owner_id: string;
    task_id?: string;
    workflow_id?: string;
    input_file_id?: string;
    input_file?: File;
    output_file_id?: string;
    output_file?: File;
}

export interface Task extends Base {
    status: TaskStatus;
    owner_id: string;
    jobs?: Job[];
    workflow_id?: string;
}

export interface TaskListItem {
    id: string;
    status: TaskStatus;
    created_at: string;
    updated_at: string;
    file_count: number;
    completed_count: number;
    failed_count: number;
    progress: number;
    summary_label: string;
    media_flow: {
        source: 'image' | 'video' | 'mixed' | 'unknown';
        target: 'image' | 'video' | 'mixed' | 'unknown';
    };
}

export interface TaskStats {
    PENDING: number;
    PROCESSING: number;
    COMPLETED: number;
    FAILED: number;
    PARTIALLY_FAILED?: number;
    storage_used?: number;
    storage_limit?: number;
    active_workers?: number;
    daily_chart?: number[];
    growth_rate?: number;
}

export interface ListTasksData {
    tasks: Task[];
    total: number;
}
