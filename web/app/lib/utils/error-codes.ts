export interface ErrorDiagnostic {
    code: string
    title: string
    category: 'input' | 'engine' | 'storage' | 'system' | 'cancelled'
    badgeClass: string
    description: string
    suggestion: string
    retryable: boolean
    actionType: 'retry' | 'tune' | 'reupload' | 'none'
}

export const ERROR_CODE_REGISTRY: Record<string, Omit<ErrorDiagnostic, 'code'>> = {
    invalid_request: {
        title: '参数配置不合法',
        category: 'input',
        badgeClass: 'bg-amber-500/10 text-amber-600 dark:text-amber-400 border-amber-500/20',
        description: '转码参数（如尺寸缩放、质量系数或目标格式编码器）不符合规范或与输入源不兼容。',
        suggestion: '建议点击「调参重试」重新检查并配置合适的转码参数。',
        retryable: false,
        actionType: 'tune',
    },
    source_unavailable: {
        title: '源文件丢失或不可读取',
        category: 'storage',
        badgeClass: 'bg-rose-500/10 text-rose-600 dark:text-rose-400 border-rose-500/20',
        description: '对象存储服务中未找到指定的原始文件，或源文件读取权限已被撤销或已过期被清理。',
        suggestion: '请检查存储桶策略或重新上传源文件并发起新任务。',
        retryable: false,
        actionType: 'reupload',
    },
    unsupported_input: {
        title: '源文件无法按要求处理',
        category: 'input',
        badgeClass: 'bg-rose-500/10 text-rose-600 dark:text-rose-400 border-rose-500/20',
        description: '源文件无法识别或已损坏、尺寸超过上限，或是暂不支持的 HDR（PQ/HLG）图像。',
        suggestion: '确认原图能正常打开；HDR 源图目前请先在本地转为 SDR 再上传。',
        retryable: false,
        actionType: 'reupload',
    },
    processing_failed: {
        title: '图像处理失败',
        category: 'engine',
        badgeClass: 'bg-orange-500/10 text-orange-600 dark:text-orange-400 border-orange-500/20',
        description: '处理引擎（libvips / cjxl）在解码、缩放或编码时出错。',
        suggestion: '可尝试换一种输出格式或调整参数后重试。',
        retryable: false,
        actionType: 'tune',
    },
    storage_failed: {
        title: '存储写入异常',
        category: 'storage',
        badgeClass: 'bg-blue-500/10 text-blue-600 dark:text-blue-400 border-blue-500/20',
        description: '转码完成后向 S3/MinIO 上传成果物时遇到临时网络抖动、连接超时或存储凭据刷新延迟。',
        suggestion: '此错误通常为临时网络波动，建议直接点击「一键重试」。',
        retryable: true,
        actionType: 'retry',
    },
    execution_timeout: {
        title: '执行超时',
        category: 'storage',
        badgeClass: 'bg-amber-500/10 text-amber-600 dark:text-amber-400 border-amber-500/20',
        description: '执行节点在处理过程中失去响应（可能已重启或负载过高），已用完自动重试次数。',
        suggestion: '稍后直接重试即可。',
        retryable: true,
        actionType: 'retry',
    },
    cancelled: {
        title: '任务已被用户取消',
        category: 'cancelled',
        badgeClass: 'bg-zinc-500/10 text-zinc-600 dark:text-zinc-400 border-zinc-500/20',
        description: '该作业已被用户主动取消，已终止计算资源占用。',
        suggestion: '若需要重新处理，可点击右侧「重试」重新调度。',
        retryable: false,
        actionType: 'retry',
    },
    internal_error: {
        title: '计算节点内部异常',
        category: 'system',
        badgeClass: 'bg-rose-500/10 text-rose-600 dark:text-rose-400 border-rose-500/20',
        description: '计算节点在处理作业时发生未知异常或非预期 panic。',
        suggestion: '可先尝试「一键重试」，若持续失败请查看错误日志。',
        retryable: true,
        actionType: 'retry',
    },
}

export function getErrorDiagnostic(code?: string, rawMessage?: string): ErrorDiagnostic {
    if (code) {
        const item = ERROR_CODE_REGISTRY[code]
        if (item) {
            return {
                code,
                title: item.title,
                category: item.category,
                badgeClass: item.badgeClass,
                description: item.description,
                suggestion: item.suggestion,
                retryable: item.retryable,
                actionType: item.actionType,
            }
        }
    }

    // Fallback heuristic based on raw message if code is missing
    const msg = (rawMessage || '').toLowerCase()
    let matchedKey: string | null = null
    if (msg.includes('cancel')) {
        matchedKey = 'cancelled'
    } else if (msg.includes('timeout') || msg.includes('lease expired') || msg.includes('max retries')) {
        matchedKey = 'max_retries_exceeded'
    } else if (msg.includes('storage') || msg.includes('s3') || msg.includes('minio') || msg.includes('upload')) {
        matchedKey = 'storage_failed'
    } else if (msg.includes('decode') || msg.includes('corrupt') || msg.includes('invalid image')) {
        matchedKey = 'decode_failed'
    } else if (msg.includes('encode')) {
        matchedKey = 'encode_failed'
    } else if (msg.includes('invalid') || msg.includes('parameter') || msg.includes('unsupported format')) {
        matchedKey = 'invalid_request'
    }

    if (matchedKey) {
        const item = ERROR_CODE_REGISTRY[matchedKey]
        if (item) {
            return {
                code: matchedKey,
                title: item.title,
                category: item.category,
                badgeClass: item.badgeClass,
                description: item.description,
                suggestion: item.suggestion,
                retryable: item.retryable,
                actionType: item.actionType,
            }
        }
    }

    return {
        code: code || 'unknown_error',
        title: code ? `执行失败 (${code})` : '作业处理失败',
        category: 'system',
        badgeClass: 'bg-rose-500/10 text-rose-600 dark:text-rose-400 border-rose-500/20',
        description: rawMessage || '执行过程中遇到未捕获的异常错误。',
        suggestion: '您可以尝试直接重试，或调整参数后重新执行。',
        retryable: true,
        actionType: 'retry',
    }
}
