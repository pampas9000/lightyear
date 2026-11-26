import type { Ref } from "vue";
import { useAuth } from "~/composables/useAuth";
import { sanitize } from "~/lib/image-options";
import type { QueueItem } from "~/composables/useUploadQueue";
import type { CompressionTarget } from "~/lib/types/task";
import type { DraftFile } from "~/lib/types/s3";
import type { ImageOptions } from "~/lib/types/image-options";

/** A submission sent but not confirmed: retrying it reuses its idempotency key. */
export interface PendingSubmission {
    idempotencyKey: string;
    requestBody: string;
}

interface TaskDraftState {
    files: Ref<QueueItem[]>;
    uploadedFileIds: Ref<Record<string, string>>;
    options: Ref<ImageOptions>;
    target: Ref<CompressionTarget | undefined>;
    /** Fetches the user's stored drafts, so the saved queue can be checked against them. */
    fetchServerFiles: () => Promise<{ data?: DraftFile[]; error: unknown }>;
}

/**
 * The new-task form saved in localStorage per user: the uploaded files in
 * the queue, the options and target, and a submission that may not have
 * reached the server. Saved on every change and restored on load.
 */
export const useTaskDraft = ({ files, uploadedFileIds, options, target, fetchServerFiles }: TaskDraftState) => {
    const { isAuthenticated, user } = useAuth();

    const pendingSubmission = ref<PendingSubmission | null>(null);
    const idempotencyKey = ref("");
    const isReconciling = ref(false);

    const draftKey = () => {
        const uId = user.value?.id || "anonymous";
        return `task_new_draft:${uId}:v2`;
    };

    const save = () => {
        if (!isAuthenticated.value) return;
        const selectedServerFileIds = files.value
            .filter((f) => f.status === "uploaded" && f.serverFileId)
            .map((f) => f.serverFileId as string);

        const draft = {
            selectedServerFileIds,
            options: options.value,
            target: target.value,
            updatedAt: Temporal.Now.instant().epochMilliseconds,
            pendingSubmission: pendingSubmission.value,
        };
        localStorage.setItem(draftKey(), JSON.stringify(draft));
    };

    const clear = () => {
        localStorage.removeItem(draftKey());
    };

    /** Restores the saved form, keeping only queued files the server still has. */
    const loadAndReconcile = async () => {
        if (!isAuthenticated.value) return;
        const stored = localStorage.getItem(draftKey());
        if (!stored) return;
        try {
            const draft = JSON.parse(stored);
            if (!draft) return;
            if (draft.options?.format) options.value = sanitize(draft.options);
            if (draft.target?.max_bytes) target.value = { max_bytes: draft.target.max_bytes };
            if (draft.pendingSubmission) {
                pendingSubmission.value = draft.pendingSubmission;
                idempotencyKey.value = draft.pendingSubmission.idempotencyKey;
            }

            isReconciling.value = true;
            const { data: serverFiles = [], error } = await fetchServerFiles();
            if (error) throw error;
            const serverFileMap = new Map(serverFiles.map((f) => [f.id, f]));

            if (draft.selectedServerFileIds && draft.selectedServerFileIds.length > 0) {
                const reconciledFiles: QueueItem[] = [];
                for (const serverId of draft.selectedServerFileIds) {
                    const sf = serverFileMap.get(serverId);
                    if (sf) {
                        reconciledFiles.push({
                            id: `recent-${sf.id}`,
                            source: "recent",
                            serverFileId: sf.id,
                            name: sf.name,
                            size: sf.size,
                            progress: 100,
                            status: "uploaded",
                            type: sf.mime_type || "",
                        });
                        uploadedFileIds.value[`recent-${sf.id}`] = sf.id;
                    } else if (draft.pendingSubmission) {
                        // Reconstruct from draft for pending submissions
                        reconciledFiles.push({
                            id: `recent-${serverId}`,
                            source: "recent",
                            serverFileId: serverId,
                            name: "Pending file",
                            size: 0,
                            progress: 100,
                            status: "uploaded",
                            type: "",
                        });
                        uploadedFileIds.value[`recent-${serverId}`] = serverId;
                    }
                }
                files.value = reconciledFiles;
            }
        } catch (e) {
            console.error("Failed to load draft:", e);
        } finally {
            isReconciling.value = false;
        }
    };

    watch(
        [files, options],
        () => {
            save();
        },
        { deep: true },
    );

    return {
        pendingSubmission,
        idempotencyKey,
        isReconciling,
        save,
        clear,
        loadAndReconcile,
    };
};
