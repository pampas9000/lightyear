package storage

// FreshEnough is the SQL condition (on "files") for a stored file that may
// still be handed out again, for reuse or fast upload: retention is not
// scheduled, or at least half of it is left. A reused file therefore lives on
// for a while, and objects never outlive their retention, which keeps a
// storage-side lifecycle rule safe. Its one argument is the current time.
const FreshEnough = `(files.expires_at IS NULL OR files.expires_at - ? > (files.expires_at - COALESCE(files.uploaded_at, files.created_at)) / 2)`
