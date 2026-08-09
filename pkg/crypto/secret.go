package crypto

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// secretPrefix 标记 EncryptSecret 写入的版本化密文。前缀使读取侧能在
// "加密后的新值"与"加密功能上线前落库的历史明文"之间做确定性判别：
// 无前缀的值一律视为非密文，禁止按明文降级使用。
const secretPrefix = "enc:v1:"

// ErrLegacyPlaintext 表示存储值不是 EncryptSecret 生成的密文
// （历史明文或无法识别的值）。调用方应将其视为"配置无效，需要重新保存"。
var ErrLegacyPlaintext = errors.New("crypto: stored value is not an encrypted secret (legacy plaintext)")

// IsEncrypted 报告 stored 是否为 EncryptSecret 生成的版本化密文（enc:v1: 前缀）。
// 读取侧用它在"加密后的新值"与"历史明文"之间做确定性判别：无前缀的值一律
// 视为明文，禁止把损坏的密文（有前缀但解不开）混入明文放行路径。
func IsEncrypted(stored string) bool {
	return strings.HasPrefix(stored, secretPrefix)
}

// EncryptSecret 用 AES-256-GCM 加密 plaintext，返回带版本前缀的密文
// （"enc:v1:" + base64）。空串按原样返回，不产生密文。
func EncryptSecret(key [32]byte, plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	ct, err := Encrypt(key, plaintext)
	if err != nil {
		return "", err
	}
	return secretPrefix + ct, nil
}

// DecryptSecret 解密 EncryptSecret 产生的密文，fail closed：
//   - 空串原样返回（空值无需加密）；
//   - 无前缀（历史明文或垃圾值）返回 ErrLegacyPlaintext；
//   - 有前缀但 payload 不是合法 base64（业务值恰好以 "enc:v1:" 开头）
//     同样按无法识别的值返回 ErrLegacyPlaintext；
//   - 前缀与 base64 合法但解密失败（密文损坏或 key 不匹配）返回错误。
//
// 存量兼容策略：加密功能上线前落库的明文没有前缀，读取时必然触发
// ErrLegacyPlaintext。调用方必须向用户返回"配置无效，请重新保存"，
// 禁止把存储值当作明文使用。
func DecryptSecret(key [32]byte, stored string) (string, error) {
	if stored == "" {
		return "", nil
	}
	if !strings.HasPrefix(stored, secretPrefix) {
		return "", ErrLegacyPlaintext
	}
	payload := strings.TrimPrefix(stored, secretPrefix)
	if _, err := base64.StdEncoding.DecodeString(payload); err != nil {
		return "", ErrLegacyPlaintext
	}
	pt, err := Decrypt(key, payload)
	if err != nil {
		return "", fmt.Errorf("crypto: decrypt secret: %w", err)
	}
	return pt, nil
}
