package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenTTL 是签发 JWT 的有效期。
const TokenTTL = 2 * time.Hour

// Claims 是 ParseJWT 解出的业务声明。
type Claims struct {
	UID  int64
	Role Role
	Exp  time.Time
}

// Manager 是认证令牌的签发与解析接口。
type Manager interface {
	// IssueJWT 为用户签发 HS256 JWT，2 小时过期，claims: uid/role/exp。
	IssueJWT(u *User) (string, error)
	// ParseJWT 校验签名、算法与过期时间，返回业务 claims。
	ParseJWT(token string) (*Claims, error)
}

// jwtManager 是 Manager 的 HS256 实现。
type jwtManager struct {
	secret []byte
	now    func() time.Time
}

// NewManager 用给定 JWT secret 构造 Manager。secret 至少 16 字节。
func NewManager(secret string) Manager {
	return &jwtManager{secret: []byte(secret), now: time.Now}
}

func (m *jwtManager) IssueJWT(u *User) (string, error) {
	if u == nil {
		return "", fmt.Errorf("auth: nil user")
	}
	claims := jwt.MapClaims{
		"uid":  u.ID,
		"role": string(u.Role),
		"exp":  m.now().Add(TokenTTL).Unix(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
}

func (m *jwtManager) ParseJWT(token string) (*Claims, error) {
	parsed, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		return m.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil {
		return nil, fmt.Errorf("auth: parse jwt: %w", err)
	}
	mc, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("auth: unexpected claims type")
	}
	uidf, ok := mc["uid"].(float64)
	if !ok {
		return nil, fmt.Errorf("auth: missing uid claim")
	}
	role, _ := mc["role"].(string)
	exp := time.Time{}
	if expf, ok := mc["exp"].(float64); ok {
		exp = time.Unix(int64(expf), 0)
	}
	return &Claims{UID: int64(uidf), Role: Role(role), Exp: exp}, nil
}
