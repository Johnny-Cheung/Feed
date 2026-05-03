package auth

import "golang.org/x/crypto/bcrypt"

// HashPassword 使用 bcrypt 生成密码哈希。
// bcrypt 的好处是：
// 1. 不是简单字符串加密，而是专门给密码设计的哈希算法
// 2. 支持盐值和计算成本，安全性比普通哈希高很多
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// ComparePassword 比较明文密码和数据库中的哈希是否匹配。
func ComparePassword(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}
