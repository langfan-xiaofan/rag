package service

import (
	"context"
	"fmt"
	"mime/multipart"

	"rag/internal/ingest"
)

type FileService struct {
	pipeline *ingest.Pipeline
}

func NewFileService(pipeline *ingest.Pipeline) *FileService {
	return &FileService{pipeline: pipeline}
}

// Upload 逐个文件走索引流水线，单个文件失败不影响其余文件。
// 返回失败列表（元素形如 "文件名: 原因"），空列表表示全部成功。
func (svc *FileService) Upload(ctx context.Context, files []*multipart.FileHeader, username, prefix string) []string {
	failed := make([]string, 0)
	target := ingest.Target{Username: username, Prefix: prefix}
	for _, f := range files {
		if err := svc.pipeline.IngestFile(ctx, f, target); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", f.Filename, err))
		}
	}
	return failed
}
