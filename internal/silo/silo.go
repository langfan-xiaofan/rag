package silo

import (
	"context"
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
		key := ObjectKey(prefix, file.Filename)
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

// listObjectKeys 分页列出桶里的 key，供按文件名兜底查找。
func (s *Silo) listObjectKeys(ctx context.Context, bucketName string) ([]string, error) {
	// 兜底查找只服务小桶，避免桶异常大时把整桶读进内存。
	const maxKeys = 10000
	keys := make([]string, 0, 64)
	var token *string
	for {
		out, err := s.s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucketName),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, err
		}
		for _, obj := range out.Contents {
			keys = append(keys, aws.ToString(obj.Key))
			if len(keys) >= maxKeys {
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
