import { toast } from "vue-sonner";
import { useApi } from "~/composables/useApi";
import { isOutputStored } from "~/lib/task-detail";
import type { ApiResponse } from "~/lib/types/api";
import type { Job } from "~/lib/types/task";

/**
 * Downloads and previews of job outputs. Each asks the API for a fresh
 * presigned URL when the user acts, so these are plain calls rather than
 * cached queries.
 */
export const useJobOutputs = () => {
    const { t } = useI18n();
    const api = useApi();

    const downloadingJobs = ref<Record<string, boolean>>({});
    const isDownloadingAll = ref(false);

    const previewJob = ref<Job | null>(null);
    const previewUrl = ref<string | null>(null);
    const isPreviewLoading = ref(false);

    const downloadUrl = (job: Job) =>
        api<ApiResponse<{ url: string }>>(`/files/${job.output_file_id}/download-url`);

    const download = async (job: Job) => {
        if (!isOutputStored(job)) return;

        downloadingJobs.value[job.id] = true;
        try {
            const res = await downloadUrl(job);
            if (res.success && res.data.url) {
                const a = document.createElement("a");
                a.href = res.data.url;
                a.download = job.output_file?.name || "transcoded-file";
                a.target = "_blank";
                document.body.appendChild(a);
                a.click();
                document.body.removeChild(a);
                toast.success(t("task_detail.toast_download_success"));
            } else {
                toast.error(t("task_detail.toast_download_link_failed"));
            }
        } catch (err: any) {
            console.error("Failed to download file:", err);
            toast.error(err.message || "Download request failed");
        } finally {
            downloadingJobs.value[job.id] = false;
        }
    };

    /** Downloads every stored output one after another. */
    const downloadAll = async (jobs: Job[] | undefined) => {
        if (!jobs) return;
        const stored = jobs.filter(isOutputStored);
        if (stored.length === 0) return;

        isDownloadingAll.value = true;
        try {
            for (const job of stored) {
                await download(job);
                await new Promise((resolve) => setTimeout(resolve, 300));
            }
        } finally {
            isDownloadingAll.value = false;
        }
    };

    const openPreview = async (job: Job) => {
        if (!isOutputStored(job)) return;
        previewJob.value = job;
        previewUrl.value = null;
        isPreviewLoading.value = true;
        try {
            const res = await downloadUrl(job);
            if (res.success && res.data.url) {
                previewUrl.value = res.data.url;
            } else {
                toast.error(t("task_detail.toast_download_link_failed"));
                previewJob.value = null;
            }
        } catch (err) {
            console.error("Failed to get preview URL:", err);
            toast.error("Failed to load preview image");
            previewJob.value = null;
        } finally {
            isPreviewLoading.value = false;
        }
    };

    const closePreview = () => {
        previewJob.value = null;
        previewUrl.value = null;
    };

    return {
        downloadingJobs,
        isDownloadingAll,
        download,
        downloadAll,
        previewJob,
        previewUrl,
        isPreviewLoading,
        openPreview,
        closePreview,
    };
};
