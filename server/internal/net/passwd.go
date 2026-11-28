package net

// 密码哈希：标准 PBKDF2-HMAC-SHA256（120000 轮、16 字节盐、32 字节输出），零外部依赖。
// 新存储格式： "p2:<iter>:<saltHex>:<hashHex>"。
// 兼容旧格式： "s:<saltHex>:<hashHex>"（4096 轮迭代 sha256），登录成功后自动升级为新格式。

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

const (
	pwIterations = 120000 // PBKDF2 轮数
	pwSaltLen    = 16     // 盐长度（字节）
	pwKeyLen     = 32     // 输出长度（字节）

	legacyRounds = 4096 // 旧格式 "s:<salt>:<hash>" 的迭代轮数
)

func hashPassword(pw string) (string, error) {
	salt := make([]byte, pwSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	h := pbkdf2SHA256([]byte(pw), salt, pwIterations, pwKeyLen)
	return fmt.Sprintf("p2:%d:%s:%s", pwIterations, hex.EncodeToString(salt), hex.EncodeToString(h)), nil
}

// needsRehash 判断存储的哈希是否仍是旧格式（需要透明升级）。
func needsRehash(stored string) bool {
	return strings.HasPrefix(stored, "s:")
}

func checkPassword(stored, pw string) bool {
	parts := strings.Split(stored, ":")
	if len(parts) == 0 {
		return false
	}
	switch parts[0] {
	case "p2":
		if len(parts) != 4 {
			return false
		}
		iter, err := strconv.Atoi(parts[1])
		if err != nil || iter <= 0 {
			return false
		}
		salt, err := hex.DecodeString(parts[2])
		if err != nil {
			return false
		}
		want, err := hex.DecodeString(parts[3])
		if err != nil {
			return false
		}
		got := pbkdf2SHA256([]byte(pw), salt, iter, len(want))
		return hmac.Equal(got, want)
	case "s":
		// 旧格式兼容
		if len(parts) != 3 {
			return false
		}
		salt, err := hex.DecodeString(parts[1])
		if err != nil {
			return false
		}
		want, err := hex.DecodeString(parts[2])
		if err != nil {
			return false
		}
		got := legacyDerive(pw, salt)
		return hmac.Equal(got, want)
	}
	return false
}

// pbkdf2SHA256 标准 PBKDF2-HMAC-SHA256 实现（RFC 8018）。
func pbkdf2SHA256(password, salt []byte, iter, keyLen int) []byte {
	if keyLen <= 0 {
		return []byte{}
	}
	hLen := sha256.Size
	numBlocks := (keyLen + hLen - 1) / hLen
	out := make([]byte, 0, numBlocks*hLen)
	var buf [4]byte
	for block := 1; block <= numBlocks; block++ {
		m := hmac.New(sha256.New, password)
		m.Write(salt)
		binary.BigEndian.PutUint32(buf[:], uint32(block))
		m.Write(buf[:])
		u := m.Sum(nil)
		t := make([]byte, len(u))
		copy(t, u)
		for i := 1; i < iter; i++ {
			m.Reset()
			m.Write(u)
			u = m.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}

// legacyDerive 旧版哈希算法：sha256 迭代 4096 轮（仅用于校验历史密码）。
func legacyDerive(pw string, salt []byte) []byte {
	cur := append(append([]byte{}, salt...), []byte(pw)...)
	for i := 0; i < legacyRounds; i++ {
		h := sha256.Sum256(cur)
		cur = h[:]
	}
	return cur
}
