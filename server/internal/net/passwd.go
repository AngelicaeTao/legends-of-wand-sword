package net

// 密码哈希：PBKDF2 风格的多轮 sha256 + 随机盐，零外部依赖。
// 存储格式： "s:<saltHex>:<hashHex>"，4096 轮，足够内测原型使用。

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const pwRounds = 4096

func hashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil { return "", err }
	h := derive(pw, salt)
	return fmt.Sprintf("s:%s:%s", hex.EncodeToString(salt), hex.EncodeToString(h)), nil
}

func checkPassword(stored, pw string) bool {
	parts := strings.Split(stored, ":")
	if len(parts) != 3 || parts[0] != "s" { return false }
	salt, err := hex.DecodeString(parts[1]); if err != nil { return false }
	want, err := hex.DecodeString(parts[2]); if err != nil { return false }
	got := derive(pw, salt)
	if len(got) != len(want) { return false }
	 diff := byte(0)
	for i := range got { diff |= got[i] ^ want[i] }
	return diff == 0
}

func derive(pw string, salt []byte) []byte {
	cur := append(append([]byte{}, salt...), []byte(pw)...)
	for i := 0; i < pwRounds; i++ {
		h := sha256.Sum256(cur)
		cur = h[:]
	}
	return cur
}
