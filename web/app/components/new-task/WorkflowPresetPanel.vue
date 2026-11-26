<script setup lang="ts">
import { Layers, Loader2 } from "@lucide/vue";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { describe } from "~/lib/image-options";
import type { Workflow } from "~/lib/types/task";

// Pick, save, update or delete a saved option set (workflow).
defineProps<{
    workflows: Workflow[];
    selectedWorkflowId: string;
    selectedWorkflow?: Workflow;
    isMultiOutputWorkflow: boolean;
    workflowDirty: boolean;
    isSavingPreset: boolean;
    disabled: boolean;
}>();

const showPresetSave = defineModel<boolean>("showPresetSave", { required: true });
const presetName = defineModel<string>("presetName", { required: true });

const emit = defineEmits<{
    apply: [id: string];
    update: [];
    remove: [];
    save: [];
}>();

const describeWorkflow = (wf: Workflow) => {
    const first = wf.outputs?.[0];
    if (!first) return "";
    const base = describe(first.options, first.target);
    return wf.outputs.length > 1 ? `${base} (+${wf.outputs.length - 1})` : base;
};
</script>

<template>
    <div
        class="p-3.5 rounded-xl bg-surface-2 border border-hairline space-y-2.5"
    >
        <div class="flex items-center justify-between">
            <Label class="text-xs font-semibold text-ink flex items-center gap-1.5">
                <Layers class="w-3.5 h-3.5 text-primary" />
                我的转码预设 (Workflows)
            </Label>
            <button
                type="button"
                :disabled="disabled"
                @click="showPresetSave = !showPresetSave"
                class="text-xs font-semibold text-primary hover:text-primary-hover cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
            >
                {{ showPresetSave ? "取消保存" : "保存当前参数为预设" }}
            </button>
        </div>
        <div class="flex gap-2">
            <Select
                :model-value="selectedWorkflowId"
                @update:model-value="val => emit('apply', String(val ?? 'none'))"
                :disabled="disabled || workflows.length === 0"
            >
                <SelectTrigger
                    class="h-9 flex-1 rounded-md text-ink text-xs"
                >
                    <SelectValue
                        :placeholder="
                            workflows.length === 0
                                ? '暂无预设，可先保存当前参数'
                                : '选择预设以一键套用'
                        "
                    />
                </SelectTrigger>
                <SelectContent class="bg-surface-3 border-hairline rounded-lg">
                    <SelectItem value="none">不使用预设（手动参数）</SelectItem>
                    <SelectItem v-for="wf in workflows" :key="wf.id" :value="wf.id">
                        {{ wf.name }} · {{ describeWorkflow(wf) }}
                    </SelectItem>
                </SelectContent>
            </Select>
            <Button
                v-if="selectedWorkflowId !== 'none' && !isMultiOutputWorkflow"
                type="button"
                size="sm"
                variant="outline"
                :disabled="isSavingPreset || disabled"
                @click="emit('update')"
                class="h-9 px-3 rounded-lg border-hairline text-xs font-semibold cursor-pointer"
            >
                <Loader2 v-if="isSavingPreset" class="w-3.5 h-3.5 animate-spin" />
                <span v-else>更新预设</span>
            </Button>
            <Button
                v-if="selectedWorkflowId !== 'none'"
                type="button"
                size="sm"
                variant="outline"
                :disabled="isSavingPreset || disabled"
                @click="emit('remove')"
                class="h-9 px-3 rounded-lg border-hairline text-xs font-semibold text-red-500 hover:text-red-600 hover:bg-red-500/5 cursor-pointer"
            >
                删除
            </Button>
        </div>
        <p
            v-if="selectedWorkflowId !== 'none' && isMultiOutputWorkflow"
            class="text-[11px] font-medium text-amber-600 dark:text-amber-400"
        >
            {{
                workflowDirty
                    ? "多输出预设的参数已修改：提交将按当前单输出参数执行；如需保存修改可另存为新预设。"
                    : `多输出预设（共 ${selectedWorkflow?.outputs?.length} 份输出）：当前仅预览第 1 份，直接提交仍生成全部 ${selectedWorkflow?.outputs?.length} 份；网页端暂不支持就地更新多输出预设。`
            }}
        </p>
        <p
            v-else-if="selectedWorkflowId !== 'none' && workflowDirty"
            class="text-[11px] font-medium text-amber-600 dark:text-amber-400"
        >
            参数已修改：提交将使用当前参数；可点击「更新预设」覆盖原预设。
        </p>
        <div v-if="showPresetSave" class="flex gap-2 animate-in fade-in duration-200">
            <Input
                v-model="presetName"
                :disabled="isSavingPreset"
                placeholder="预设名称，例如：AVIF 画质优先"
                class="h-9 flex-1 rounded-md text-xs"
                @keyup.enter="emit('save')"
            />
            <Button
                type="button"
                size="sm"
                :disabled="isSavingPreset || !presetName.trim()"
                @click="emit('save')"
                class="h-9 px-4 rounded-lg bg-primary text-primary-foreground hover:bg-primary-hover text-xs font-semibold cursor-pointer gap-1.5"
            >
                <Loader2 v-if="isSavingPreset" class="w-3.5 h-3.5 animate-spin" />
                <span>保存</span>
            </Button>
        </div>
    </div>
</template>
