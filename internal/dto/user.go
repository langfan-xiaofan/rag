package dto

type RegisterReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required,min=6"`
}

type LoginReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type LoginRes struct {
	Token string `json:"token"`
}

type UserInfoRes struct {
	ID       uint   `json:"id"`
	Username string `json:"username"`
}
