export interface MultipartUploadResponse {
  uploadId: string
  key: string
  fileId?: string
  fastUpload?: boolean
}

export interface PartSignatureResponse {
  url: string
}

export interface CompleteMultipartUploadRequest {
  key: string
  parts: {
    PartNumber: number
    ETag: string
  }[]
}
