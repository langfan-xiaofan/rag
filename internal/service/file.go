package service

import (
	"context"
	"fmt"
	"mime/multipart"
	"strings"

	"rag/internal/ingest"
)

type FileService struct {
	pipeline *ingest.Pipeline
	kb       *KnowledgeBaseService
}

func NewFileService(pipeline *ingest.Pipeline, kb *KnowledgeBaseService) *FileService {
	return &FileService{pipeline: pipeline, kb: kb}
}

// Upload 逐个文件走索引流水线，单个文件失败不影响其余文件。
// 返回失败列表（元素形如 "文件名: 原因"），空列表表示全部成功。
func (svc *FileService) Upload(ctx context.Context, files []*multipart.FileHeader, userID uint, username string, kbID uint, prefix string) ([]string, error) {
	kb, err := svc.kb.GetOwned(ctx, userID, kbID)
	if err != nil {
		return nil, err
	}
	target := ingest.Target{
		UserID:          userID,
		Username:        username,
		KnowledgeBaseID: kb.ID,
		DocCollection:   kb.DocCollection,
		FileCollection:  kb.FileCollection,
		ObjectPrefix:    kb.ObjectPrefix,
		Prefix:          cleanArchivePrefix(prefix),
	}
	failed := make([]string, 0)
	for _, f := range files {
		if err := svc.pipeline.IngestFile(ctx, f, target); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", f.Filename, err))
		}
	}
	return failed, nil
}

// cleanArchivePrefix 只保留安全的目录段，避免 ../ 或绝对路径把对象写出知识库前缀。
func cleanArchivePrefix(prefix string) string {
	parts := strings.FieldsFunc(prefix, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || part == "." || part == ".." {
			continue
		}
		clean = append(clean, part)
	}
	return strings.Join(clean, "/")
}
