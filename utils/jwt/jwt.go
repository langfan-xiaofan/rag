package jwt

import (
	"errors"
	"rag/internal/config"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type MyClaims struct {
	Username string `json:"username"`
	UserID   uint   `json:"user_id"`
	jwt.RegisteredClaims
}

func GenerateToken(username string, userID uint) (string, error) {
	claims := MyClaims{
		username,
		userID,
		jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour * time.Duration(config.Conf.Jwt.Hour))),
			Issuer:    "rag",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	var jwtKey = []byte(config.Conf.Jwt.Key)
	return token.SignedString(jwtKey)

}
func ParseToken(tokenString string) (*MyClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &MyClaims{}, func(token *jwt.Token) (interface{}, error) {
		return []byte(config.Conf.Jwt.Key), nil
	})
	if err != nil {
		return nil, err
	}
	if claims, ok := token.Claims.(*MyClaims); ok {
		return claims, nil
	}
	return nil, errors.New("invalid token")
}
