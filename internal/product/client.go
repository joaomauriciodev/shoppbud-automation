package product

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"

	"shoppbud-automation/internal/auth"
)

// CreateRequest contém os dados necessários para criação de um produto
type CreateRequest struct {
	Name                        string
	CategoryID                  string
	Description                 string
	BarCode                     string
	LastPriceInCents            string
	ImportedExpectedFromCategory bool
	ImageBytes                  []byte
	ImageFilename               string
}

// UpdateRequest contém os dados necessários para atualização de um produto
type UpdateRequest struct {
	ID                          int
	Name                        string
	CategoryID                  string
	Description                 string
	BarCode                     string
	LastPriceInCents            string
	ImportedExpectedFromCategory bool
	ImageBytes                  []byte
	ImageFilename               string
}

// CreateResponse representa a resposta da criação do produto
type CreateResponse struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// SearchItem representa um produto retornado na busca
type SearchItem struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// SearchResponse representa a resposta da busca de produtos
type SearchResponse struct {
	Data  []SearchItem `json:"data"`
	Total int          `json:"total"`
}

// Client gerencia operações de produto na API ShoppBud
type Client struct {
	authClient *auth.Client
	httpClient *http.Client
	baseURL    string
}

// NewClient cria um novo client de produto
func NewClient(authClient *auth.Client, baseURL string) *Client {
	return &Client{
		authClient: authClient,
		httpClient: authClient.HTTPClient(),
		baseURL:    baseURL,
	}
}

// SearchByBarCode busca produtos pelo código de barras
func (c *Client) SearchByBarCode(barCode string) (*SearchResponse, error) {
	url := fmt.Sprintf("%s/product?ncm=&barCode=%s&page=1&perPage=10", c.baseURL, barCode)

	httpReq, err := c.authClient.AuthenticatedRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("erro ao criar request: %w", err)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("erro na busca de produto: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler resposta: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("busca de produto falhou (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	// A API pode retornar { data: [...] } ou diretamente um array
	var searchResp SearchResponse
	if err := json.Unmarshal(respBody, &searchResp); err != nil {
		// Tenta como array direto
		var items []SearchItem
		if err2 := json.Unmarshal(respBody, &items); err2 != nil {
			return nil, fmt.Errorf("erro ao parsear resposta de busca: %w", err)
		}
		searchResp.Data = items
		searchResp.Total = len(items)
	}

	return &searchResp, nil
}

// Create cria um novo produto via multipart/form-data
func (c *Client) Create(req CreateRequest) (*CreateResponse, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	fields := map[string]string{
		"name":        req.Name,
		"categoryId":  req.CategoryID,
		"description": req.Description,
		"barCodes[0]": req.BarCode,
		"lastPriceInCents": req.LastPriceInCents,
	}
	if req.ImportedExpectedFromCategory {
		fields["importedExpectedFromCategory"] = "true"
	} else {
		fields["importedExpectedFromCategory"] = "false"
	}

	for field, value := range fields {
		if err := writer.WriteField(field, value); err != nil {
			return nil, fmt.Errorf("erro ao escrever campo %q: %w", field, err)
		}
	}

	if len(req.ImageBytes) > 0 {
		filename := req.ImageFilename
		if filename == "" {
			filename = "produto.jpg"
		}
		part, err := writer.CreateFormFile("image", filename)
		if err != nil {
			return nil, fmt.Errorf("erro ao criar campo imagem: %w", err)
		}
		if _, err := io.Copy(part, bytes.NewReader(req.ImageBytes)); err != nil {
			return nil, fmt.Errorf("erro ao escrever imagem: %w", err)
		}
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("erro ao fechar multipart writer: %w", err)
	}

	url := c.baseURL + "/product/"
	httpReq, err := c.authClient.AuthenticatedRequest(http.MethodPost, url, &buf)
	if err != nil {
		return nil, fmt.Errorf("erro ao criar request autenticada: %w", err)
	}

	httpReq.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("erro na requisição de criação de produto: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler resposta: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("criação de produto falhou (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var createResp CreateResponse
	if err := json.Unmarshal(respBody, &createResp); err != nil {
		// Retorna resposta bruta se não conseguir parsear
		return &CreateResponse{Name: req.Name}, nil
	}

	return &createResp, nil
}

// Update atualiza um produto existente via multipart/form-data (PUT)
func (c *Client) Update(req UpdateRequest) (*CreateResponse, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	fields := map[string]string{
		"name":        req.Name,
		"categoryId":  req.CategoryID,
		"description": req.Description,
		"barCodes[0]": req.BarCode,
		"lastPriceInCents": req.LastPriceInCents,
	}
	if req.ImportedExpectedFromCategory {
		fields["importedExpectedFromCategory"] = "true"
	} else {
		fields["importedExpectedFromCategory"] = "false"
	}

	for field, value := range fields {
		if err := writer.WriteField(field, value); err != nil {
			return nil, fmt.Errorf("erro ao escrever campo %q: %w", field, err)
		}
	}

	if len(req.ImageBytes) > 0 {
		filename := req.ImageFilename
		if filename == "" {
			filename = "produto.jpg"
		}
		part, err := writer.CreateFormFile("image", filename)
		if err != nil {
			return nil, fmt.Errorf("erro ao criar campo imagem: %w", err)
		}
		if _, err := io.Copy(part, bytes.NewReader(req.ImageBytes)); err != nil {
			return nil, fmt.Errorf("erro ao escrever imagem: %w", err)
		}
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("erro ao fechar multipart writer: %w", err)
	}

	url := fmt.Sprintf("%s/product/%d", c.baseURL, req.ID)
	httpReq, err := c.authClient.AuthenticatedRequest(http.MethodPut, url, &buf)
	if err != nil {
		return nil, fmt.Errorf("erro ao criar request autenticada: %w", err)
	}

	httpReq.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("erro na requisição de atualização de produto: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler resposta: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("atualização de produto falhou (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var updateResp CreateResponse
	if err := json.Unmarshal(respBody, &updateResp); err != nil {
		return &CreateResponse{ID: req.ID, Name: req.Name}, nil
	}

	return &updateResp, nil
}
