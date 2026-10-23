package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"rag/internal/dao"
	"rag/internal/dto"
	"rag/internal/model"
	"rag/internal/silo"

	"github.com/google/uuid"
	"github.com/qdrant/go-client/qdrant"
	"gorm.io/gorm"
)

var (
	ErrKnowledgeBaseNotFound  = errors.New("知识库不存在")
	ErrKnowledgeBaseNameEmpty = errors.New("知识库名称不能为空")
	ErrKnowledgeBaseDuplicate = errors.New("同名知识库已存在")
	ErrFileNameEmpty          = errors.New("文件名不能为空")
)

type KnowledgeBaseService struct {
	dao    *dao.KnowledgeBaseDao
	qdrant *qdrant.Client
	silo   *silo.Silo
}

func NewKnowledgeBaseService(db *gorm.DB, qdrantClient *qdrant.Client, siloClient *silo.Silo) *KnowledgeBaseService {
	return &KnowledgeBaseService{
		dao:    dao.NewKnowledgeBaseDao(db),
		qdrant: qdrantClient,
		silo:   siloClient,
	}
}

func (s *KnowledgeBaseService) Create(ctx context.Context, userID uint, req dto.CreateKBReq) (*dto.KBRes, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, ErrKnowledgeBaseNameEmpty
	}
	if _, err := s.dao.GetByUserAndName(ctx, userID, name); err == nil {
		return nil, ErrKnowledgeBaseDuplicate
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	storageID := strings.ReplaceAll(uuid.NewString(), "-", "")
	kb := &model.KnowledgeBase{
		UserID:         userID,
		Name:           name,
		Description:    strings.TrimSpace(req.Description),
		DocCollection:  "kb_" + storageID,
		FileCollection: "kb_" + storageID + "_file",
		ObjectPrefix:   "kb_" + storageID,
	}
	if err := s.dao.Create(ctx, kb); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) || strings.Contains(err.Error(), "Duplicate entry") {
			return nil, ErrKnowledgeBaseDuplicate
		}
		return nil, err
	}
	return toKBRes(kb, 0, 0), nil
}

func (s *KnowledgeBaseService) List(ctx context.Context, userID uint) ([]dto.KBRes, error) {
	kbs, err := s.dao.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	res := make([]dto.KBRes, 0, len(kbs))
	for i := range kbs {
		fileCount := s.countPoints(ctx, kbs[i].FileCollection)
		chunkCount := s.countPoints(ctx, kbs[i].DocCollection)
		res = append(res, *toKBRes(&kbs[i], fileCount, chunkCount))
	}
	return res, nil
}

// GetOwned 按“知识库 ID + 当前用户”加载，客户端无法通过猜 ID 访问别人的知识库。
func (s *KnowledgeBaseService) GetOwned(ctx context.Context, userID, kbID uint) (*model.KnowledgeBase, error) {
	kb, err := s.dao.GetByIDAndUser(ctx, kbID, userID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrKnowledgeBaseNotFound
	}
	if err != nil {
		return nil, err
	}
	return kb, nil
}

func (s *KnowledgeBaseService) Update(ctx context.Context, userID, kbID uint, req dto.UpdateKBReq) (*dto.KBRes, error) {
	kb, err := s.GetOwned(ctx, userID, kbID)
	if err != nil {
		return nil, err
	}

	name := kb.Name
	description := kb.Description
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
		if name == "" {
			return nil, ErrKnowledgeBaseNameEmpty
		}
		if name != kb.Name {
			existing, err := s.dao.GetByUserAndName(ctx, userID, name)
			if err == nil && existing.ID != kbID {
				return nil, ErrKnowledgeBaseDuplicate
			}
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, err
			}
		}
	}
	if req.Description != nil {
		description = strings.TrimSpace(*req.Description)
	}

	if err := s.dao.Update(ctx, kbID, userID, name, description); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrKnowledgeBaseNotFound
		}
		if errors.Is(err, gorm.ErrDuplicatedKey) || strings.Contains(err.Error(), "Duplicate entry") {
			return nil, ErrKnowledgeBaseDuplicate
		}
		return nil, err
	}
	kb.Name = name
	kb.Description = description
	if updated, err := s.dao.GetByIDAndUser(ctx, kbID, userID); err == nil {
		kb = updated
	}
	return toKBRes(kb, s.countPoints(ctx, kb.FileCollection), s.countPoints(ctx, kb.DocCollection)), nil
}

// Delete 物理删除知识库：Qdrant 集合、对象存储文件、关联会话与消息、MySQL 记录。
// 先删外部资源再删 MySQL，任何一步失败都保留知识库记录，方便重试。
func (s *KnowledgeBaseService) Delete(ctx context.Context, userID uint, username string, kbID uint) error {
	kb, err := s.GetOwned(ctx, userID, kbID)
	if err != nil {
		return err
	}

	// 默认知识库的旧文件可能不在新前缀下，先按文件记录收集真实 key。
	records, err := s.fileRecords(ctx, kb.FileCollection)
	if err != nil {
		return fmt.Errorf("读取文件记录失败: %w", err)
	}

	// 先删外部资源再删 MySQL/Qdrant：任何一步失败都保留知识库记录，
	// 文件记录也还在，重试时才能拿到旧对象的真实 key。
	if kb.ObjectPrefix != "" {
		if err := s.silo.DeletePrefix(username, kb.ObjectPrefix); err != nil {
			return fmt.Errorf("删除知识库文件失败: %w", err)
		}
	}
	for _, record := range records {
		if record.Key == "" {
			continue
		}
		if err := s.silo.DeleteObject(username, record.Key); err != nil {
			return fmt.Errorf("删除文件对象 %s 失败: %w", record.Key, err)
		}
	}
	if err := s.dropCollection(ctx, kb.DocCollection); err != nil {
		return fmt.Errorf("删除文档集合失败: %w", err)
	}
	if err := s.dropCollection(ctx, kb.FileCollection); err != nil {
		return fmt.Errorf("删除文件集合失败: %w", err)
	}

	if err := s.dao.DeleteWithSessions(ctx, kbID, userID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrKnowledgeBaseNotFound
		}
		return err
	}
	return nil
}

func (s *KnowledgeBaseService) ListFiles(ctx context.Context, userID, kbID uint) ([]dto.KBFileRes, error) {
	kb, err := s.GetOwned(ctx, userID, kbID)
	if err != nil {
		return nil, err
	}
	return s.fileRecords(ctx, kb.FileCollection)
}

func (s *KnowledgeBaseService) DeleteFile(ctx context.Context, userID uint, username string, kbID uint, fileName string) error {
	fileName = baseFileName(fileName)
	if fileName == "" {
		return ErrFileNameEmpty
	}
	kb, err := s.GetOwned(ctx, userID, kbID)
	if err != nil {
		return err
	}

	records, err := s.fileRecords(ctx, kb.FileCollection)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(records)+2)
	for _, record := range records {
		if record.FileName == fileName && record.Key != "" {
			keys = append(keys, record.Key)
		}
	}
	if kb.ObjectPrefix != "" {
		keys = append(keys, path.Join(kb.ObjectPrefix, fileName))
	}
	// 只有沿用旧集合名的默认知识库，才可能把对象直接放在桶根目录。
	// 新知识库的对象都在自己的前缀下，不能按裸文件名兜底，避免误删同名文件。
	if kb.DocCollection == username {
		keys = append(keys, fileName)
	}

	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if err := s.silo.DeleteObject(username, key); err != nil {
			return fmt.Errorf("删除文件对象 %s 失败: %w", key, err)
		}
	}

	if err := s.deletePointsByFilename(ctx, kb.DocCollection, fileName); err != nil {
		return fmt.Errorf("删除文档分块失败: %w", err)
	}
	if err := s.deletePointsByFilename(ctx, kb.FileCollection, fileName); err != nil {
		return fmt.Errorf("删除文件记录失败: %w", err)
	}
	return nil
}

// DeleteFileVectors 供上传流水线在写入新版本前清理同名旧数据。
func (s *KnowledgeBaseService) DeleteFileVectors(ctx context.Context, docCollection, fileCollection, fileName string) error {
	fileName = baseFileName(fileName)
	if fileName == "" {
		return nil
	}
	if err := s.deletePointsByFilename(ctx, docCollection, fileName); err != nil {
		return fmt.Errorf("清理旧文档分块失败: %w", err)
	}
	if err := s.deletePointsByFilename(ctx, fileCollection, fileName); err != nil {
		return fmt.Errorf("清理旧文件记录失败: %w", err)
	}
	return nil
}

func (s *KnowledgeBaseService) fileRecords(ctx context.Context, collection string) ([]dto.KBFileRes, error) {
	if collection == "" {
		return []dto.KBFileRes{}, nil
	}
	exists, err := s.qdrant.CollectionExists(ctx, collection)
	if err != nil {
		return nil, err
	}
	if !exists {
		return []dto.KBFileRes{}, nil
	}

	it := s.qdrant.ScrollAll(ctx, &qdrant.ScrollPoints{
		CollectionName: collection,
		WithPayload:    qdrant.NewWithPayload(true),
		Limit:          new(uint32(256)),
	})
	records := make([]dto.KBFileRes, 0, 16)
	for {
		points, err := it.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		for _, point := range points {
			payload := point.GetPayload()
			fileName := payload["filename"].GetStringValue()
			fileType := payload["filetype"].GetStringValue()
			if fileType == "" {
				// 早期文件记录没有 filetype，按扩展名补一个展示值
				fileType = strings.TrimPrefix(strings.ToLower(path.Ext(fileName)), ".")
			}
			records = append(records, dto.KBFileRes{
				FileName:   fileName,
				FileType:   fileType,
				Summary:    payload["summary"].GetStringValue(),
				Key:        payload["key"].GetStringValue(),
				UploadedAt: payload["uploaded_at"].GetStringValue(),
			})
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].UploadedAt == records[j].UploadedAt {
			return records[i].FileName < records[j].FileName
		}
		return records[i].UploadedAt > records[j].UploadedAt
	})
	return records, nil
}

func (s *KnowledgeBaseService) countPoints(ctx context.Context, collection string) uint64 {
	if collection == "" {
		return 0
	}
	exists, err := s.qdrant.CollectionExists(ctx, collection)
	if err != nil || !exists {
		return 0
	}
	exact := true
	count, err := s.qdrant.Count(ctx, &qdrant.CountPoints{
		CollectionName: collection,
		Exact:          &exact,
	})
	if err != nil {
		return 0
	}
	return count
}

func (s *KnowledgeBaseService) dropCollection(ctx context.Context, collection string) error {
	if collection == "" {
		return nil
	}
	exists, err := s.qdrant.CollectionExists(ctx, collection)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	return s.qdrant.DeleteCollection(ctx, collection)
}

func (s *KnowledgeBaseService) deletePointsByFilename(ctx context.Context, collection, fileName string) error {
	if collection == "" {
		return nil
	}
	exists, err := s.qdrant.CollectionExists(ctx, collection)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	wait := true
	_, err = s.qdrant.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: collection,
		Wait:           &wait,
		Points: &qdrant.PointsSelector{
			PointsSelectorOneOf: &qdrant.PointsSelector_Filter{
				Filter: &qdrant.Filter{
					Must: []*qdrant.Condition{
						qdrant.NewMatchKeyword("filename", fileName),
					},
				},
			},
		},
	})
	return err
}

func toKBRes(kb *model.KnowledgeBase, fileCount, chunkCount uint64) *dto.KBRes {
	return &dto.KBRes{
		ID:          kb.ID,
		Name:        kb.Name,
		Description: kb.Description,
		FileCount:   fileCount,
		ChunkCount:  chunkCount,
		CreatedAt:   kb.CreatedAt,
		UpdatedAt:   kb.UpdatedAt,
	}
}

func baseFileName(name string) string {
	return path.Base(strings.ReplaceAll(strings.TrimSpace(name), "\\", "/"))
}
