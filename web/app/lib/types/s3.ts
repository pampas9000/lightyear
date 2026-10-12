export interface InitUploadResponse {
  key: string
  fileId: string
  fastUpload?: boolean
  // Single PUT (files up to 10 MB): the presigned URL.
  url?: string
  // Multipart: the upload ID and one presigned URL per part, in order.
  uploadId?: string
  partUrls?: string[]
}

export interface CompleteUploadResponse {
  fileId: string
  key: string
  location?: string
}

export interface PartSignatureResponse {
  url: string
}
