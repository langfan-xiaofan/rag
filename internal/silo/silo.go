package silo

import (
	"context"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"

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
		key := file.Filename
		if prefix != "" {
			key = prefix + "/" + file.Filename
		}
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

func (s *Silo) GetFilePreSignUrl(Filename string, bucketName string) (string, error) {
	ctx := context.Background()
	presignClient := s3.NewPresignClient(s.s3Client)

	output, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: &bucketName,
		Key:    aws.String(Filename),
	})
	if err != nil {
		return "", err
	}
	return output.URL, nil
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
