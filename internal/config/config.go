package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	BaseURL      string `json:"base_url"`
	Email        string `json:"email"`
	Password     string `json:"password"`
	UserID       string `json:"user_id"`
	GeminiAPIKey string `json:"gemini_api_key"`
}

// LoadConfig carrega as configurações do arquivo .env (se existir) ou das variáveis de ambiente
func LoadConfig(path string) (*Config, error) {
	// Em produção (ex: Railway) o .env não existe — as vars já estão no ambiente
	_ = godotenv.Load(path)

	cfg := &Config{
		BaseURL:      os.Getenv("BASE_URL"),
		Email:        os.Getenv("EMAIL"),
		Password:     os.Getenv("PASSWORD"),
		UserID:       os.Getenv("USER_ID"),
		GeminiAPIKey: os.Getenv("GEMINI_API_KEY"),
	}

	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.shoppbud.com.br"
	}

	if cfg.Email == "" || cfg.Password == "" || cfg.GeminiAPIKey == "" {
		return nil, fmt.Errorf("variáveis de ambiente obrigatórias não definidas (EMAIL, PASSWORD, GEMINI_API_KEY)")
	}

	return cfg, nil
}

// DefaultConfig retorna uma config padrão para gerar o arquivo inicial
func DefaultConfig() *Config {
	return &Config{
		BaseURL:      "https://api.shoppbud.com.br",
		Email:        "seu_email@exemplo.com",
		Password:     "sua_senha",
		UserID:       "000000",
		GeminiAPIKey: "sua_chave_gemini",
	}
}
