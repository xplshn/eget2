package eget2

type Metadata struct {
	ETag         string `json:"etag"`
	LastModified string `json:"last_modified"`
	Offset       int64  `json:"offset"`
}
