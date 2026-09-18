package config

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

var Conf = new(Config)

type Config struct {
	Jwt      Jwt      `json:"jwt"`
	Qdrant   Qdrant   `json:"qdrant"`
	Embedder Embedder `json:"embedder"`
	Mysql    Mysql    `json:"mysql"`
}

type Jwt struct {
	Key  string `mapstructure:"key"`
	Hour int    `mapstructure:"hour"`
}

type Qdrant struct {
	Host string
	Port int
}

type Embedder struct {
	BaseUrl string `mapstructure:"base_url" json:"base_url"`
	Model   string
	ApiKey  string `mapstructure:"api_key" json:"api_key"`
}

type Mysql struct {
	Host     string
	Port     string
	User     string
	Password string
	Name     string
}

func Init() error {
	viper.SetConfigFile("config/config.yaml")
	err := viper.ReadInConfig()
	if err != nil {
		return fmt.Errorf("读取config.yaml失败: %w", err)
	}
	viper.SetEnvPrefix("RAG")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()
	for _, key := range []string{"jwt.key", "embedder.api_key", "mysql.password"} {
		if err := viper.BindEnv(key); err != nil {
			return err
		}
	}
	if err := viper.Unmarshal(Conf); err != nil {
		return err
	}
	return Conf.Validate()
}
func (c *Config) Validate() error {
	switch {
	case c.Jwt.Key == "":
		return errors.New("JWT 密钥为空，请设置环境变量 RAG_JWT_KEY")
	case c.Mysql.Password == "":
		return errors.New("MySQL 密码为空，请设置环境变量 RAG_MYSQL_PASSWORD")
	case c.Embedder.ApiKey == "":
		return errors.New("Embedding API Key 为空，请设置环境变量 RAG_EMBEDDER_API_KEY")
	}
	return nil
}
