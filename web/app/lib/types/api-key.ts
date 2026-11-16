/**
 * API keys for the public /v1 API.
 * Mirrors: server/internal/models.APIKey and server/internal/api/handlers/apikey.go
 */
export type ApiKeyScope = "tasks:read" | "tasks:write";

export interface ApiKey {
    id: string;
    name: string;
    /** First characters of the key, enough to tell keys apart. */
    prefix: string;
    scopes: ApiKeyScope[];
    last_used_at: string | null;
    revoked_at: string | null;
    created_at: string;
    updated_at: string;
}

export interface ListApiKeysData {
    api_keys: ApiKey[];
    scopes: ApiKeyScope[];
}

export interface CreateApiKeyData {
    api_key: ApiKey;
    /** The plaintext key. Returned only once, at creation. */
    key: string;
}
