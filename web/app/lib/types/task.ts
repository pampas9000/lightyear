export const enum TaskStatus {
    PENDING = "PENDING",
    PROCESSING = "PROCESSING",
    COMPLETED = "COMPLETED",
    FAILED = "FAILED",
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
}

export interface Task extends Base {
    status: TaskStatus;
    owner_id: string;
    jobs?: Job[];
    workflow_id?: string;
}

export interface TaskStats {
    PENDING: number;
    PROCESSING: number;
    COMPLETED: number;
    FAILED: number;
}

export interface ListTasksData {
    tasks: Task[];
    total: number;
}
