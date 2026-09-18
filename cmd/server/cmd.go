package server

import (
	"context"
	"log"
	"net/http"
	"os"

	"rag/internal/config"
	"rag/internal/database"
	"rag/internal/embedder"
	"rag/internal/indexer"
	"rag/internal/ingest"
	"rag/internal/parser"
	"rag/internal/router"
	"rag/internal/silo"
	"rag/internal/transformer"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/gin-gonic/gin"
	"github.com/qdrant/go-client/qdrant"
	"github.com/subosito/gotenv"
)

const (
	// 与 config/config.yaml 里配置的 embedding 模型维度保持一致
	embedDimension = 2048
	chunkSize      = 500
	chunkOverlap   = 200
)

func Cmd() {
	if err := gotenv.Load(); err != nil && !os.IsNotExist(err) {
		log.Printf("加载 .env 失败（如果是用环境变量注入配置，可以忽略）: %v", err)
	}
	if err := config.Init(); err != nil {
		log.Fatal("加载 config.yaml 失败，请在项目根目录运行: ", err)
	}
	r := gin.Default()
	db, err := database.Init()
	if err != nil {
		log.Fatal(err)
	}
	//s3对象存储客户端
	s3Client := s3.New(s3.Options{
		BaseEndpoint: aws.String(os.Getenv("SiloEndPoint")),
		Credentials:  credentials.NewStaticCredentialsProvider(os.Getenv("SiloAccessKey"), os.Getenv("SiloSecret"), ""),
		UsePathStyle: true,
		Region:       "langfan",
	})
	//qdrant向量数据库的客户端
	qdrantclient, err := qdrant.NewClient(&qdrant.Config{
		Host: config.Conf.Qdrant.Host,
		Port: config.Conf.Qdrant.Port,
	})
	if err != nil {
		panic(err)
	}
	//文本嵌入工具
	Textembedder, err := embedder.NewTextEmbedder(context.Background())
	if err != nil {
		panic(err)
	}
	//多模态嵌入工具
	multiModalembedder := embedder.NewMultimodalEmbedder(embedder.Config{
		BaseUrl: os.Getenv("MultiModalBaseUrl"),
		ApiKey:  os.Getenv("MultiModalApiKey"),
		Model:   os.Getenv("MultiModalModel"),
	})
	//大模型对象
	chatmodel, err := openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
		BaseURL: os.Getenv("DEEPSEEK_BASE_URL"),
		APIKey:  os.Getenv("DEEPSEEK_API_KEY"),
		Model:   os.Getenv("DEEPSEEK_MODEL_NAME"),
	})
	if err != nil {
		panic(err)
	}
	// 解析器、切分器、索引器都是无状态的，构造一次交给流水线复用
	siloClient := silo.NewSilo(silo.ConfigSilo{S3Client: s3Client})
	extParser, err := parser.NewExtParser()
	if err != nil {
		log.Fatal("初始化解析器失败: ", err)
	}
	pipeline := ingest.New(&ingest.Config{
		Parser:        extParser,
		Transformer:   transformer.NewTransformer(chunkSize, chunkOverlap, Textembedder),
		Indexer:       indexer.NewQdrantIndexer(&indexer.Config{Client: qdrantclient, Dimension: embedDimension}),
		Silo:          siloClient,
		TextEmbedder:  Textembedder,
		MultiEmbedder: multiModalembedder,
	})

	router.InitRouter(r, db, qdrantclient, siloClient, pipeline, Textembedder, chatmodel)
	srv := http.Server{
		Addr:    ":8080",
		Handler: r,
	}
	err = srv.ListenAndServe()
	if err != nil {
		log.Fatal(err)
		os.Exit(1)
	}
}
