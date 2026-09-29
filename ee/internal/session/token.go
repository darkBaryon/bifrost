// 本文件生成会话和票据的随机值，并提供认证读取使用的令牌形状检查与摘要。
package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// tokenBytes 沿用已有会话与票据协议的 256 位随机值；编码长度由此推导。
const tokenBytes = 32

var tokenLength = base64.RawURLEncoding.EncodedLen(tokenBytes)

// ValidToken 沿用已有令牌的长度检查；不符合长度时不必查询数据库，不额外改变旧凭据的字符规则。
func ValidToken(raw string) bool { return len(raw) == tokenLength }

// Digest 返回令牌的 SHA-256 十六进制摘要，与已有会话和票据记录保持兼容。
func Digest(raw string) string {
	hash := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(hash[:])
}

func randomToken() string {
	var bytes [tokenBytes]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic("crypto/rand unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(bytes[:])
}

// randomID 沿用 UUIDv4 格式；随机源失效时进程已无法安全签发凭据，直接终止。
func randomID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic("crypto/rand unavailable")
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", bytes[:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:])
}
