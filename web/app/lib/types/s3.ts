// Data of the console's upload endpoints (server/internal/api/handlers/upload.go),
// each wrapped in ApiResponse.

export interface InitUploadData {
  file_id: string
  key: string
  /** The content is already stored; nothing to upload. */
  fast_upload?: boolean
  // Single PUT (files up to 10 MB): the presigned URL.
  url?: string
  // Multipart: the upload ID and one presigned URL per part, in order.
  upload_id?: string
  part_urls?: string[]
}

export interface CompleteUploadData {
  file_id: string
  key: string
  location?: string
}

export interface UploadPart {
  part_number: number
  size: number
  etag: string
}

export interface ListPartsData {
  parts: UploadPart[]
}

/** A draft: an uploaded file not yet submitted in a task (GET /drafts/list). */
export interface DraftFile {
  id: string
  name: string
  size: number
  mime_type: string
  uploaded_at: string
  status: string
}

export interface SignPartData {
  part_number: number
  url: string
  expires_at: string
}
