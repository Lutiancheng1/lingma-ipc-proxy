package remote

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type Credential struct {
	CosyKey         string
	EncryptUserInfo string
	UserID          string
	MachineID       string
	Source          string
	TokenExpireTime int64
}

type storedCredentialFile struct {
	Source          string `json:"source"`
	TokenExpireTime string `json:"token_expire_time"`
	Auth            struct {
		CosyKey         string `json:"cosy_key"`
		EncryptUserInfo string `json:"encrypt_user_info"`
		UserID          string `json:"user_id"`
		MachineID       string `json:"machine_id"`
	} `json:"auth"`
}

func LoadCredential(authFile string) (Credential, error) {
	if path := strings.TrimSpace(authFile); path != "" {
		return loadCredentialFile(expandHome(path))
	}
	return importLingmaCacheCredential()
}

func loadCredentialFile(path string) (Credential, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Credential{}, fmt.Errorf("read remote auth file: %w", err)
	}
	var stored storedCredentialFile
	if err := json.Unmarshal(body, &stored); err != nil {
		return Credential{}, fmt.Errorf("parse remote auth file: %w", err)
	}
	cred := Credential{
		CosyKey:         stored.Auth.CosyKey,
		EncryptUserInfo: stored.Auth.EncryptUserInfo,
		UserID:          stored.Auth.UserID,
		MachineID:       stored.Auth.MachineID,
		Source:          valueOr(stored.Source, path),
		TokenExpireTime: parseExpire(stored.TokenExpireTime),
	}
	return cred, validateCredential(cred)
}

func importLingmaCacheCredential() (Credential, error) {
	var attempts []string
	for _, lingmaDir := range candidateLingmaCacheDirs() {
		cred, err := importLingmaCacheCredentialFromDir(lingmaDir)
		if err == nil {
			return cred, nil
		}
		attempts = append(attempts, fmt.Sprintf("%s: %v", lingmaDir, err))
	}
	if len(attempts) == 0 {
		return Credential{}, errors.New("no Lingma cache directory candidate was found")
	}
	return Credential{}, fmt.Errorf("load Lingma login cache: %s", strings.Join(attempts, "; "))
}

func importLingmaCacheCredentialFromDir(lingmaDir string) (Credential, error) {
	machineID, err := loadMachineID(lingmaDir)
	if err != nil {
		return Credential{}, err
	}
	userPath := filepath.Join(lingmaDir, "cache", "user")
	encrypted, err := os.ReadFile(userPath)
	if err != nil {
		return Credential{}, fmt.Errorf("read %s: %w", userPath, err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encrypted)))
	if err != nil {
		return Credential{}, fmt.Errorf("decode %s: %w", userPath, err)
	}
	plaintext, err := decryptCacheUser(machineID, ciphertext)
	if err != nil {
		return Credential{}, err
	}
	var payload struct {
		Key             string `json:"key"`
		EncryptUserInfo string `json:"encrypt_user_info"`
		UserID          string `json:"uid"`
		ExpireTime      any    `json:"expire_time"`
	}
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return Credential{}, fmt.Errorf("parse %s: %w", userPath, err)
	}
	cred := Credential{
		CosyKey:         payload.Key,
		EncryptUserInfo: payload.EncryptUserInfo,
		UserID:          payload.UserID,
		MachineID:       machineID,
		Source:          userPath,
		TokenExpireTime: parseExpireAny(payload.ExpireTime),
	}
	return cred, validateCredential(cred)
}

func candidateLingmaCacheDirs() []string {
	if explicit := strings.TrimSpace(os.Getenv("LINGMA_CACHE_DIR")); explicit != "" {
		return []string{expandHome(explicit)}
	}

	var dirs []string
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		dirs = append(dirs,
			filepath.Join(home, ".lingma"),
			filepath.Join(home, ".config", "Lingma"),
			filepath.Join(home, ".local", "share", "Lingma"),
		)
	}
	for _, envName := range []string{"APPDATA", "LOCALAPPDATA", "ProgramData"} {
		if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
			dirs = append(dirs,
				filepath.Join(value, "Lingma"),
				filepath.Join(value, "lingma"),
			)
		}
	}
	if value := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); value != "" {
		dirs = append(dirs, filepath.Join(value, "Lingma"))
	}
	return uniquePathStrings(dirs)
}

func loadMachineID(lingmaDir string) (string, error) {
	if body, err := os.ReadFile(filepath.Join(lingmaDir, "cache", "id")); err == nil {
		if value := strings.TrimSpace(string(body)); value != "" {
			return value, nil
		}
	}
	logBody, err := os.ReadFile(filepath.Join(lingmaDir, "logs", "lingma.log"))
	if err != nil {
		return "", fmt.Errorf("remote credential requires cache/id or lingma.log machine id: %w", err)
	}
	markers := []string{"using machine id from file:", "machine id:"}
	text := string(logBody)
	for _, marker := range markers {
		index := strings.LastIndex(strings.ToLower(text), marker)
		if index < 0 {
			continue
		}
		line := text[index+len(marker):]
		if newline := strings.IndexByte(line, '\n'); newline >= 0 {
			line = line[:newline]
		}
		if value := strings.TrimSpace(line); value != "" {
			return value, nil
		}
	}
	return "", errors.New("machine id not found in Lingma cache")
}

func decryptCacheUser(machineID string, ciphertext []byte) ([]byte, error) {
	if len(machineID) < aes.BlockSize {
		return nil, errors.New("machine id too short for cache decryption")
	}
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, errors.New("invalid cache/user ciphertext size")
	}
	key := []byte(machineID[:aes.BlockSize])
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, key).CryptBlocks(plaintext, ciphertext)
	return unpadPKCS7(plaintext)
}

func unpadPKCS7(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty plaintext")
	}
	padLen := int(data[len(data)-1])
	if padLen <= 0 || padLen > aes.BlockSize || padLen > len(data) {
		return nil, errors.New("invalid cache/user padding")
	}
	for _, b := range data[len(data)-padLen:] {
		if int(b) != padLen {
			return nil, errors.New("invalid cache/user padding bytes")
		}
	}
	return data[:len(data)-padLen], nil
}

func validateCredential(cred Credential) error {
	if strings.TrimSpace(cred.CosyKey) == "" {
		return errors.New("remote credential missing cosy_key")
	}
	if strings.TrimSpace(cred.EncryptUserInfo) == "" {
		return errors.New("remote credential missing encrypt_user_info")
	}
	if strings.TrimSpace(cred.UserID) == "" {
		return errors.New("remote credential missing user_id")
	}
	if strings.TrimSpace(cred.MachineID) == "" {
		return errors.New("remote credential missing machine_id")
	}
	return nil
}

func parseExpire(value string) int64 {
	parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return parsed
}

func parseExpireAny(value any) int64 {
	switch typed := value.(type) {
	case string:
		return parseExpire(typed)
	case float64:
		return int64(typed)
	case int64:
		return typed
	case int:
		return int64(typed)
	default:
		return 0
	}
}

func IsExpired(cred Credential, margin time.Duration) bool {
	return cred.TokenExpireTime > 0 && time.Now().Add(margin).UnixMilli() > cred.TokenExpireTime
}

func MachineOSHeader() string {
	switch runtime.GOOS {
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return "arm64_darwin"
		}
		return "x86_64_darwin"
	case "windows":
		if runtime.GOARCH == "arm64" {
			return "arm64_windows"
		}
		return "x86_64_windows"
	case "linux":
		if runtime.GOARCH == "arm64" {
			return "arm64_linux"
		}
		return "x86_64_linux"
	default:
		return runtime.GOARCH + "_" + runtime.GOOS
	}
}

func uniquePathStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		cleaned := filepath.Clean(value)
		key := strings.ToLower(cleaned)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, cleaned)
	}
	return out
}
