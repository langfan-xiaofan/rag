package silo

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net/http"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type ConfigSilo struct {
	S3Client   *s3.Client
	BucketName string
}

type Silo struct {
	s3Client   *s3.Client
	bucketName string
}

func NewSilo(config ConfigSilo) *Silo {
	return &Silo{
		s3Client:   config.S3Client,
		bucketName: config.BucketName,
	}
}

func (s *Silo) UploadFile(files []*multipart.FileHeader, bucketName string, prefix string) error {
	ctx := context.Background()
	//检查这个桶是否存在了，如果不存在就新建一个
	output, err := s.s3Client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: new(bucketName),
	})
	if err != nil || output == nil {
		_, err := s.s3Client.CreateBucket(ctx, &s3.CreateBucketInput{
			Bucket: aws.String(bucketName),
			CreateBucketConfiguration: &types.CreateBucketConfiguration{
				LocationConstraint: types.BucketLocationConstraint("cn-north-1"),
			},
		})
		if err != nil {
			return err
		}
	}
	for _, file := range files {
		fileStream, err := file.Open()
		if err != nil {
			return err
		}
		filetype, err := GetFileType(fileStream)
		if err != nil {
			return err
		}
		// 以魔数嗅探为准，防止伪造扩展名（如 mp4 改成 .zip）；
		// 只有嗅探结果是 zip / octet-stream 这种无法区分的类型时，
		// 才用扩展名补充判断（docx/xlsx/pptx 本质是 zip 包）
		if filetype == "application/zip" || filetype == "application/octet-stream" {
			if t := mime.TypeByExtension(filepath.Ext(file.Filename)); t != "" {
				filetype = t
			}
		}
		key := ObjectKey(prefix, UploadFileName(file.Filename))
		_, err = s.s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:        aws.String(bucketName),
			Body:          fileStream,
			Key:           aws.String(key),
			ContentType:   &filetype,
			ContentLength: &file.Size,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// ObjectKey 拼出对象在桶里的完整 key。上传和下载都必须走这个函数：
// 两边算法一旦分叉，签出来的链接就会指向一个不存在的对象（NoSuchKey）。
func ObjectKey(prefix, filename string) string {
	if prefix == "" {
		return filename
	}
	return prefix + "/" + filename
}

// GetFilePreSignUrl 按 key 签发下载链接。key 必须是对象在桶里的完整路径，
// 可能是「前缀/文件名」而不只是文件名，先过一遍 ResolveKey 再签。
func (s *Silo) GetFilePreSignUrl(key string, bucketName string) (string, error) {
	ctx := context.Background()
	presignClient := s3.NewPresignClient(s.s3Client)

	output, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: &bucketName,
		Key:    aws.String(key),
	})
	if err != nil {
		return "", err
	}
	return output.URL, nil
}

// ResolveKey 找出这个文件在桶里真实存在的 key。candidates 按优先级排列
// （文件记录里存的 key、传给工具的文件名……），命中即返回；都不存在时再按文件名
// 在桶里扫一遍，兜住早期版本把对象存成「前缀/文件名」、记录里却只有裸文件名的数据。
func (s *Silo) ResolveKey(bucketName string, fileName string, candidates ...string) (string, error) {
	ctx := context.Background()
	keys := make([]string, 0, len(candidates)+1)
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" || slices.Contains(keys, c) {
			continue
		}
		keys = append(keys, c)
	}
	if len(keys) == 0 {
		keys = append(keys, fileName)
	}
	for _, key := range keys {
		exist, err := s.objectExists(ctx, bucketName, key)
		if err != nil {
			return "", err
		}
		if exist {
			return key, nil
		}
	}
	listed, err := s.listObjectKeys(ctx, bucketName)
	if err != nil {
		return "", err
	}
	// 同一个文件名可能散落在多个前缀目录下，取最浅的那个。
	base := path.Base(fileName)
	best := ""
	for _, key := range listed {
		if path.Base(key) != base {
			continue
		}
		if best == "" || len(key) < len(best) || (len(key) == len(best) && key < best) {
			best = key
		}
	}
	if best == "" {
		return "", fmt.Errorf("对象存储桶 %s 里找不到 %s（已尝试 key：%s）", bucketName, fileName, strings.Join(keys, "、"))
	}
	return best, nil
}

// ResolveKeyWithin 只在给定的候选 key 中查找，不做整桶扫描。
// 用于把下载范围限制在当前知识库前缀内，避免命中其他知识库的同名文件。
func (s *Silo) ResolveKeyWithin(bucketName string, candidates ...string) (string, error) {
	keys := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || slices.Contains(keys, candidate) {
			continue
		}
		keys = append(keys, candidate)
	}
	for _, key := range keys {
		exist, err := s.objectExists(context.Background(), bucketName, key)
		if err != nil {
			return "", err
		}
		if exist {
			return key, nil
		}
	}
	return "", fmt.Errorf("对象存储桶 %s 里找不到当前知识库的文件（已尝试 key：%s）",
		bucketName, strings.Join(keys, "、"))
}

// ResolveLegacyKey 在桶里按文件名找迁移前的旧对象，但跳过所有 kb_ 前缀。
// 只给默认知识库里没有记录 key 的老文件兜底，避免命中新知识库的对象。
func (s *Silo) ResolveLegacyKey(bucketName, fileName string) (string, error) {
	keys, err := s.listObjectKeys(context.Background(), bucketName)
	if err != nil {
		return "", err
	}
	base := path.Base(strings.ReplaceAll(fileName, "\\", "/"))
	best := ""
	for _, key := range keys {
		if strings.HasPrefix(key, "kb_") {
			continue
		}
		if path.Base(key) != base {
			continue
		}
		// 同一个文件名可能在多个旧目录下，取最浅的那个
		if best == "" || len(key) < len(best) || (len(key) == len(best) && key < best) {
			best = key
		}
	}
	if best == "" {
		return "", fmt.Errorf("对象存储桶 %s 里找不到旧文件 %s", bucketName, fileName)
	}
	return best, nil
}

// objectExists 用「前缀列举 + 精确比对」判断 key 是否存在。
// 不用 HeadObject 是因为各家 S3 兼容实现回 404 的错误码并不统一。
func (s *Silo) objectExists(ctx context.Context, bucketName string, key string) (bool, error) {
	out, err := s.s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:  aws.String(bucketName),
		Prefix:  aws.String(key),
		MaxKeys: aws.Int32(4),
	})
	if err != nil {
		return false, err
	}
	for _, obj := range out.Contents {
		if aws.ToString(obj.Key) == key {
			return true, nil
		}
	}
	return false, nil
}

// DeleteObject 删除单个对象。对象不存在视为成功，方便重复清理。
func (s *Silo) DeleteObject(bucketName, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	_, err := s.s3Client.DeleteObject(context.Background(), &s3.DeleteObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	if err != nil && !isObjectNotFound(err) && !isInvalidBucketName(err) {
		return err
	}
	return nil
}

// DeletePrefix 删除某个前缀下的所有对象。拒绝空前缀，避免误删整个桶。
func (s *Silo) DeletePrefix(bucketName, prefix string) error {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return errors.New("拒绝删除空前缀")
	}
	keys, err := s.listObjectKeysWithPrefix(context.Background(), bucketName, prefix, 100000)
	if err != nil {
		if isObjectNotFound(err) || isInvalidBucketName(err) {
			return nil
		}
		return err
	}
	return s.deleteKeys(bucketName, keys)
}

func (s *Silo) deleteKeys(bucketName string, keys []string) error {
	const batchSize = 1000
	for start := 0; start < len(keys); start += batchSize {
		end := start + batchSize
		if end > len(keys) {
			end = len(keys)
		}
		objects := make([]types.ObjectIdentifier, 0, end-start)
		for _, key := range keys[start:end] {
			objects = append(objects, types.ObjectIdentifier{Key: aws.String(key)})
		}
		_, err := s.s3Client.DeleteObjects(context.Background(), &s3.DeleteObjectsInput{
			Bucket: aws.String(bucketName),
			Delete: &types.Delete{
				Objects: objects,
				Quiet:   aws.Bool(true),
			},
		})
		if err != nil && !isObjectNotFound(err) {
			return err
		}
	}
	return nil
}

func isObjectNotFound(err error) bool {
	if err == nil {
		return false
	}
	var noKey *types.NoSuchKey
	if errors.As(err, &noKey) {
		return true
	}
	var noBucket *types.NoSuchBucket
	if errors.As(err, &noBucket) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "NoSuchKey") || strings.Contains(msg, "NoSuchBucket")
}

// isInvalidBucketName 表示用户名本身不是合法的 S3 桶名（例如带下划线）。
// 这种桶不可能存有对象，删除时按“无对象”处理，避免知识库删不掉。
func isInvalidBucketName(err error) bool {
	return err != nil && strings.Contains(err.Error(), "InvalidBucketName")
}

// UploadFileName 只保留文件名本身，避免客户端把路径写进对象 key。
func UploadFileName(name string) string {
	return path.Base(strings.ReplaceAll(strings.TrimSpace(name), "\\", "/"))
}

// listObjectKeys 分页列出桶里的 key，供按文件名兜底查找。
func (s *Silo) listObjectKeys(ctx context.Context, bucketName string) ([]string, error) {
	// 兜底查找只服务小桶，避免桶异常大时把整桶读进内存。
	return s.listObjectKeysWithPrefix(ctx, bucketName, "", 10000)
}

func (s *Silo) listObjectKeysWithPrefix(ctx context.Context, bucketName, prefix string, maxKeys int) ([]string, error) {
	keys := make([]string, 0, 64)
	var token *string
	for {
		input := &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucketName),
			ContinuationToken: token,
		}
		if prefix != "" {
			input.Prefix = aws.String(prefix)
		}
		out, err := s.s3Client.ListObjectsV2(ctx, input)
		if err != nil {
			return nil, err
		}
		for _, obj := range out.Contents {
			keys = append(keys, aws.ToString(obj.Key))
			if maxKeys > 0 && len(keys) >= maxKeys {
				return keys, nil
			}
		}
		if !aws.ToBool(out.IsTruncated) {
			return keys, nil
		}
		token = out.NextContinuationToken
	}
}

func GetFileType(reader multipart.File) (string, error) {
	buffer := make([]byte, 512)
	n, err := reader.Read(buffer)
	if err != nil {
		return "", err
	}
	//重置文件的游标指针
	_, _ = reader.Seek(0, 0)
	filetype := http.DetectContentType(buffer[:n])
	return filetype, nil
}

func (s *Silo) GetBucketName() string {
	return s.bucketName
}
