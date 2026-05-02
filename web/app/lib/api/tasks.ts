export type TaskStatus = "queued" | "running" | "succeeded" | "failed" | "canceled";
export type SourceKind = "upload" | "object" | "url";
export type ExecutionMode = "async" | "sync";

export interface EncodeOptionsDto {
  quality?: number;
  [key: string]: unknown;
}

export interface TaskSourceDto {
  kind: SourceKind;
  value: string;
  mime_type?: string;
  size_bytes?: number;
  checksum_sha256?: string;
}

export interface TaskTargetDto {
  format: string;
  options?: EncodeOptionsDto;
}

export interface TaskExecutionDto {
  mode?: ExecutionMode;
}

export interface CreateTaskRequest {
  source: TaskSourceDto;
  target: TaskTargetDto;
  execution?: TaskExecutionDto;
  client_request_id?: string;
  metadata?: Record<string, unknown>;
}

export interface CreateTaskResponse {
  task_id: string;
  status: TaskStatus;
  contract_version: string;
  accepted_at: string;
  poll_url: string;
  result_url?: string | null;
}

export interface ArtifactReference {
  provider: string;
  bucket?: string;
  key: string;
  mime_type: string;
  size_bytes?: number;
  checksum_sha256?: string;
  public_url?: string;
  expires_at?: string;
}

export interface ApiErrorPayload {
  code: string;
  message: string;
  retryable?: boolean;
  details?: Record<string, unknown>;
}

export interface TaskRecord {
  task_id: string;
  status: TaskStatus;
  input?: TaskSourceDto;
  target?: TaskTargetDto;
  result?: ArtifactReference | null;
  error?: ApiErrorPayload | null;
  submitted_at?: string;
  started_at?: string | null;
  finished_at?: string | null;
  trace_id?: string | null;
  metadata?: Record<string, unknown>;
  [key: string]: unknown;
}

export interface ListTasksParams {
  status?: TaskStatus;
  limit?: number;
  cursor?: string;
}

export interface ListTasksResponse {
  items: TaskRecord[];
  next_cursor?: string | null;
}

export interface TasksApiClientOptions {
  baseUrl?: string;
  fetch?: typeof fetch;
  headers?: HeadersInit;
}

export class TasksApiError extends Error {
  readonly status: number;
  readonly payload: unknown;

  constructor(message: string, status: number, payload: unknown) {
    super(message);
    this.name = "TasksApiError";
    this.status = status;
    this.payload = payload;
  }
}

const DEFAULT_BASE_URL = "/api/v1";

function trimTrailingSlash(value: string): string {
  return value.replace(/\/+$/, "");
}

function joinUrl(baseUrl: string, path: string): string {
  const normalizedBase = trimTrailingSlash(baseUrl);
  const normalizedPath = path.startsWith("/") ? path : `/${path}`;

  if (!normalizedBase) {
    return normalizedPath;
  }

  return `${normalizedBase}${normalizedPath}`;
}

function buildQueryString(query?: Record<string, string | number | boolean | null | undefined>): string {
  if (!query) {
    return "";
  }

  const searchParams = new URLSearchParams();

  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === null || value === "") {
      continue;
    }

    searchParams.set(key, String(value));
  }

  const result = searchParams.toString();
  return result ? `?${result}` : "";
}

async function parseResponseBody(response: Response): Promise<unknown> {
  const text = await response.text();

  if (!text) {
    return undefined;
  }

  const contentType = response.headers.get("content-type") ?? "";

  if (contentType.includes("application/json")) {
    return JSON.parse(text) as unknown;
  }

  return text;
}

function getErrorMessage(payload: unknown, fallback: string): string {
  if (payload && typeof payload === "object" && "message" in payload) {
    const message = (payload as { message?: unknown }).message;
    if (typeof message === "string" && message.length > 0) {
      return message;
    }
  }

  return fallback;
}

export function isTerminalTaskStatus(status: TaskStatus): boolean {
  return status === "succeeded" || status === "failed" || status === "canceled";
}

export function createTasksApiClient(options: TasksApiClientOptions = {}) {
  const baseUrl = options.baseUrl ?? DEFAULT_BASE_URL;
  const fetchImpl = options.fetch ?? fetch;
  const defaultHeaders = options.headers;

  async function request<T>(
    path: string,
    init: RequestInit = {},
    query?: Record<string, string | number | boolean | null | undefined>,
  ): Promise<T> {
    const headers = new Headers(defaultHeaders);

    if (init.headers) {
      const incomingHeaders = new Headers(init.headers);
      incomingHeaders.forEach((value, key) => headers.set(key, value));
    }

    const url = `${joinUrl(baseUrl, path)}${buildQueryString(query)}`;
    const response = await fetchImpl(url, {
      ...init,
      headers,
    });

    const payload = await parseResponseBody(response);

    if (!response.ok) {
      throw new TasksApiError(
        getErrorMessage(payload, `Tasks API request failed with status ${response.status}`),
        response.status,
        payload,
      );
    }

    return payload as T;
  }

  return {
    async createTask(input: CreateTaskRequest): Promise<CreateTaskResponse> {
      return request<CreateTaskResponse>("/tasks", {
        method: "POST",
        headers: {
          "content-type": "application/json",
        },
        body: JSON.stringify(input),
      });
    },

    async getTask(taskId: string): Promise<TaskRecord> {
      return request<TaskRecord>(`/tasks/${encodeURIComponent(taskId)}`);
    },

    async listTasks(params: ListTasksParams = {}): Promise<ListTasksResponse> {
      return request<ListTasksResponse>("/tasks", {}, {
        status: params.status,
        limit: params.limit,
        cursor: params.cursor,
      });
    },

    async cancelTask(taskId: string): Promise<TaskRecord> {
      return request<TaskRecord>(`/tasks/${encodeURIComponent(taskId)}/cancel`, {
        method: "POST",
      });
    },
  };
}

export const tasksApi = createTasksApiClient();
