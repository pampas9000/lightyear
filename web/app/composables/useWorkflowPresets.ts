import type { Ref } from "vue";
import { toast } from "vue-sonner";
import { parseApiError } from "~/composables/useApi";
import { useWorkflowMutations, useWorkflows } from "~/composables/queries/workflows";
import { sanitize } from "~/lib/image-options";
import type { CompressionTarget, Workflow } from "~/lib/types/task";
import type { ImageOptions } from "~/lib/types/image-options";

/**
 * Saved option sets (workflows) on the new-task page. Applying one fills the
 * form; while the form still matches it the task references the workflow,
 * and any edit marks it dirty so the task submits explicit options instead.
 */
export const useWorkflowPresets = (
    options: Ref<ImageOptions>,
    target: Ref<CompressionTarget | undefined>,
    /** Returns false (and asks the user to sign in) when signed out. */
    requireAuth: () => boolean,
) => {
    const workflowsQuery = useWorkflows();
    const workflows = computed<Workflow[]>(() => workflowsQuery.data.value ?? []);
    const workflowMutations = useWorkflowMutations();

    const selectedWorkflowId = ref("none");
    const selectedWorkflow = computed(() => workflows.value.find((w) => w.id === selectedWorkflowId.value));
    const isMultiOutputWorkflow = computed(() => (selectedWorkflow.value?.outputs?.length ?? 0) > 1);
    const workflowDirty = ref(false);
    const isApplyingWorkflow = ref(false);
    const showPresetSave = ref(false);
    const presetName = ref("");
    const isSavingPreset = ref(false);

    /** True when the task should reference the selected workflow instead of sending options. */
    const usesWorkflow = computed(() => selectedWorkflowId.value !== "none" && !workflowDirty.value);

    const applyWorkflow = async (id: string) => {
        const wf = workflows.value.find((w) => w.id === id);
        const first = wf?.outputs?.[0];
        if (!wf || !first) {
            selectedWorkflowId.value = "none";
            workflowDirty.value = false;
            return;
        }
        isApplyingWorkflow.value = true;
        try {
            options.value = sanitize(first.options);
            target.value = first.target?.max_bytes ? { max_bytes: first.target.max_bytes } : undefined;
            selectedWorkflowId.value = wf.id;
            workflowDirty.value = false;
            toast.success(`已应用预设「${wf.name}」`);
        } finally {
            await nextTick();
            isApplyingWorkflow.value = false;
        }
    };

    const currentOutputs = () => [
        {
            options: sanitize(options.value),
            ...(target.value?.max_bytes ? { target: target.value } : {}),
        },
    ];

    const savePreset = async () => {
        if (!requireAuth()) return;
        const name = presetName.value.trim();
        if (!name) {
            toast.error("请输入预设名称");
            return;
        }
        isSavingPreset.value = true;
        try {
            await workflowMutations.create.mutateAsync({ name, outputs: currentOutputs() });
            toast.success(`预设「${name}」已保存`);
            presetName.value = "";
            showPresetSave.value = false;
        } catch (err) {
            console.error("Failed to save workflow:", err);
            toast.error("保存预设失败", { description: parseApiError(err).userMessage });
        } finally {
            isSavingPreset.value = false;
        }
    };

    const updatePreset = async () => {
        if (selectedWorkflowId.value === "none") return;
        const wf = workflows.value.find((w) => w.id === selectedWorkflowId.value);
        if (!wf || (wf.outputs?.length ?? 0) > 1) return;
        isSavingPreset.value = true;
        try {
            await workflowMutations.update.mutateAsync({
                id: wf.id,
                body: { name: wf.name, outputs: currentOutputs() },
            });
            workflowDirty.value = false;
            toast.success(`预设「${wf.name}」已更新为当前参数`);
        } catch (err) {
            console.error("Failed to update workflow:", err);
            toast.error("更新预设失败", { description: parseApiError(err).userMessage });
        } finally {
            isSavingPreset.value = false;
        }
    };

    const deletePreset = async () => {
        if (selectedWorkflowId.value === "none") return;
        const wf = workflows.value.find((w) => w.id === selectedWorkflowId.value);
        if (!wf) return;
        if (!window.confirm(`确认删除预设「${wf.name}」？已创建的任务不受影响。`)) return;
        isSavingPreset.value = true;
        try {
            await workflowMutations.remove.mutateAsync(wf.id);
            selectedWorkflowId.value = "none";
            workflowDirty.value = false;
            toast.success(`预设「${wf.name}」已删除`);
        } catch (err) {
            console.error("Failed to delete workflow:", err);
            toast.error("删除预设失败", { description: parseApiError(err).userMessage });
        } finally {
            isSavingPreset.value = false;
        }
    };

    // Mark the applied workflow as modified as soon as any parameter is tweaked.
    // The task still submits explicit params in that case; the link only survives
    // while the form matches the stored preset.
    watch(
        [options, target],
        () => {
            if (isApplyingWorkflow.value) return;
            if (selectedWorkflowId.value !== "none") {
                workflowDirty.value = true;
            }
        },
        { deep: true },
    );

    return {
        workflows,
        selectedWorkflowId,
        selectedWorkflow,
        isMultiOutputWorkflow,
        workflowDirty,
        usesWorkflow,
        showPresetSave,
        presetName,
        isSavingPreset,
        applyWorkflow,
        savePreset,
        updatePreset,
        deletePreset,
    };
};
