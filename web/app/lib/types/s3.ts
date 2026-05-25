export interface MultipartUploadResponse {
  uploadId: string
  key: string
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
