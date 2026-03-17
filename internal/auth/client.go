package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"shoppbud-automation/internal/config"
)

// LoginRequest é o payload enviado para /sessions/login
type LoginRequest struct {
	Email             string `json:"email"`
	Password          string `json:"password"`
	NotificationToken string `json:"notificationToken"`
}

// LoginResponse representa a resposta completa do login
type LoginResponse struct {
	Token TokenInfo `json:"token"`
	User  UserInfo  `json:"user"`
	Roles []Role    `json:"roles"`
}

type TokenInfo struct {
	Type      string `json:"type"`
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

type UserInfo struct {
	ID    int    `json:"id"`
	Email string `json:"email"`
}

type Role struct {
	Type string `json:"type"`
}

// Client gerencia a autenticação com a API ShoppBud
type Client struct {
	cfg        *config.Config
	httpClient *http.Client
	token      string
	expiresAt  time.Time
}

// NewClient cria um novo client de autenticação
func NewClient(cfg *config.Config) *Client {
	return &Client{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Login realiza a autenticação e armazena o bearer token
func (c *Client) Login() (*LoginResponse, error) {
	payload := LoginRequest{
		Email:             c.cfg.Email,
		Password:          c.cfg.Password,
		NotificationToken: "",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("erro ao serializar payload: %w", err)
	}

	url := c.cfg.BaseURL + "/sessions/login"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("erro ao criar request: %w", err)
	}

	c.setDefaultHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("erro na requisição de login: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler resposta: %w", err)
	}

	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("login falhou (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var loginResp LoginResponse
	if err := json.Unmarshal(respBody, &loginResp); err != nil {
		return nil, fmt.Errorf("erro ao parsear resposta: %w", err)
	}

	// Armazena o token para uso nas próximas requisições
	c.token = loginResp.Token.Token

	// Parseia a data de expiração
	if t, err := time.Parse(time.RFC3339, loginResp.Token.ExpiresAt); err == nil {
		c.expiresAt = t
	}

	return &loginResp, nil
}

// GetToken retorna o bearer token atual
func (c *Client) GetToken() string {
	return c.token
}

// IsTokenValid verifica se o token ainda é válido
func (c *Client) IsTokenValid() bool {
	if c.token == "" {
		return false
	}
	return time.Now().Before(c.expiresAt)
}

// EnsureAuthenticated garante que existe um token válido, fazendo login se necessário
func (c *Client) EnsureAuthenticated() error {
	if c.IsTokenValid() {
		return nil
	}

	_, err := c.Login()
	return err
}

// AuthenticatedRequest cria uma request já com o header Authorization preenchido
func (c *Client) AuthenticatedRequest(method, url string, body io.Reader) (*http.Request, error) {
	if err := c.EnsureAuthenticated(); err != nil {
		return nil, fmt.Errorf("falha na autenticação: %w", err)
	}

	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}

	c.setDefaultHeaders(req)
	req.Header.Set("Authorization", "Bearer "+c.token)

	return req, nil
}

// Do executa uma request autenticada e retorna a resposta
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	return c.httpClient.Do(req)
}

// HTTPClient retorna o http.Client interno para uso por outros pacotes
func (c *Client) HTTPClient() *http.Client {
	return c.httpClient
}

// setDefaultHeaders aplica os headers padrão que a API espera
func (c *Client) setDefaultHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://admin.shoppbud.com.br")
	req.Header.Set("Referer", "https://admin.shoppbud.com.br/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.0.0 Safari/537.36")
	req.Header.Set("X-App-Version", "1.76.3")
	req.Header.Set("X-Platform", "admin")
	req.Header.Set("X-User-Id", c.cfg.UserID)
}
