package constants

const (
	MaxRequestBodyBytes     int64 = 10 * 1024 * 1024 // 10 MB
	MaxUploadBytes          int64 = 50 * 1024 * 1024 // 50 MB for knowledge ingest
	MaxEncryptedObjectBytes int64 = 16 * 1024 * 1024 // 16 MB
)
