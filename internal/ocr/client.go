package ocr

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	gcvURL    = "https://vision.googleapis.com/v1/images:annotate"
	geminiURL = "https://generativelanguage.googleapis.com/v1beta/models/gemini-3-flash-preview:generateContent"
)

// ProdutoCupom representa um produto extraído de um cupom fiscal
type ProdutoCupom struct {
	Nome       string  `json:"nome"`
	CodigoEAN  string  `json:"codigoEAN"`
	Preco      float64 `json:"preco"`
	Quantidade float64 `json:"quantidade"`
}

// Client realiza OCR em imagens de cupom
type Client struct {
	gcvAPIKey    string
	geminiAPIKey string
	httpClient   *http.Client
}

// NewClient cria um novo client OCR.
// Se geminiAPIKey estiver preenchida, a imagem é enviada diretamente ao Gemini Vision
// (sem necessidade do GCV). Se vazia, usa Google Cloud Vision + parser regex.
func NewClient(geminiAPIKey string) *Client {
	return &Client{
		geminiAPIKey: geminiAPIKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ExtrairProdutosDeBytes processa bytes de imagem e retorna os produtos encontrados
func (c *Client) ExtrairProdutosDeBytes(data []byte) ([]ProdutoCupom, error) {
	b64 := base64.StdEncoding.EncodeToString(data)
	mime := detectarMediaTypeDeBytes(data)
	return c.extrairProdutos(b64, mime)
}

// ExtrairProdutos lê uma imagem de cupom e retorna os produtos encontrados
func (c *Client) ExtrairProdutos(imagePath string) ([]ProdutoCupom, error) {
	data, err := os.ReadFile(imagePath)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler imagem: %w", err)
	}
	b64 := base64.StdEncoding.EncodeToString(data)
	mime := detectarMediaType(imagePath)
	return c.extrairProdutos(b64, mime)
}

// extrairProdutos decide qual backend usar conforme as chaves disponíveis
func (c *Client) extrairProdutos(b64, mimeType string) ([]ProdutoCupom, error) {
	// Gemini Vision: envia a imagem diretamente, sem precisar do GCV
	if c.geminiAPIKey != "" {
		return c.extrairProdutosComGeminiVision(b64, mimeType)
	}

	// Fallback: Google Cloud Vision + parser regex
	texto, err := c.extrairTextoGCV(b64)
	if err != nil {
		return nil, err
	}
	if texto == "" {
		return nil, nil
	}
	return parsearCupom(texto), nil
}

// --- Gemini Vision ---

type geminiRequest struct {
	Contents []geminiContent `json:"contents"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text       string            `json:"text,omitempty"`
	InlineData *geminiInlineData `json:"inline_data,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mime_type"`
	Data     string `json:"data"`
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
}

const geminiPrompt = `Você é um parser especializado em cupons fiscais brasileiros (NFC-e / CF-e / cupom fiscal).

Analise esta imagem de cupom fiscal e retorne SOMENTE um array JSON com os produtos encontrados.
Cada objeto deve ter exatamente estas chaves:
  "nome"       – string: nome do produto limpo, sem códigos, em MAIÚSCULAS
  "codigoEAN"  – string: código de barras EAN (8, 12, 13 ou 14 dígitos); "" se não encontrado
  "preco"      – number: preço unitário em reais com duas casas decimais (ex: 12.50)
  "quantidade" – number: quantidade comprada (padrão 1)

Regras:
- Ignore cabeçalho, rodapé e linhas sem produto (CNPJ, CPF, TOTAL, TROCO, DESCONTO, SUBTOTAL, PAGAMENTO, DATA, HORA, CAIXA, EMITENTE, etc.)
- Se a linha mostrar "qtd X preço_unitário = total", use o preço unitário, não o total
- Retorne APENAS o JSON puro, sem markdown, sem explicações`

// extrairProdutosComGeminiVision envia a imagem ao Gemini e retorna os produtos parseados
func (c *Client) extrairProdutosComGeminiVision(b64, mimeType string) ([]ProdutoCupom, error) {
	payload := geminiRequest{
		Contents: []geminiContent{
			{
				Parts: []geminiPart{
					{InlineData: &geminiInlineData{MimeType: mimeType, Data: b64}},
					{Text: geminiPrompt},
				},
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("erro ao serializar payload Gemini: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, geminiURL+"?key="+c.geminiAPIKey, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("erro na requisição Gemini: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler resposta Gemini: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Gemini retornou HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var gemResp geminiResponse
	if err := json.Unmarshal(respBody, &gemResp); err != nil {
		return nil, fmt.Errorf("erro ao parsear resposta Gemini: %w", err)
	}

	if len(gemResp.Candidates) == 0 || len(gemResp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("Gemini não retornou conteúdo")
	}

	jsonStr := strings.TrimSpace(gemResp.Candidates[0].Content.Parts[0].Text)

	// Remove possíveis blocos markdown ```json ... ```
	if strings.HasPrefix(jsonStr, "```") {
		jsonStr = regexp.MustCompile(`(?s)^`+"```"+`[a-z]*\n?(.*?)`+"```"+`$`).ReplaceAllString(jsonStr, "$1")
		jsonStr = strings.TrimSpace(jsonStr)
	}

	var produtos []ProdutoCupom
	if err := json.Unmarshal([]byte(jsonStr), &produtos); err != nil {
		return nil, fmt.Errorf("erro ao parsear JSON do Gemini: %w — resposta: %s", err, jsonStr)
	}

	return produtos, nil
}

// --- Google Cloud Vision (fallback) ---

type gcvRequest struct {
	Requests []gcvAnnotateRequest `json:"requests"`
}

type gcvAnnotateRequest struct {
	Image    gcvImage     `json:"image"`
	Features []gcvFeature `json:"features"`
}

type gcvImage struct {
	Content string `json:"content"`
}

type gcvFeature struct {
	Type       string `json:"type"`
	MaxResults int    `json:"maxResults"`
}

type gcvResponse struct {
	Responses []struct {
		TextAnnotations []struct {
			Description string `json:"description"`
		} `json:"textAnnotations"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"responses"`
}

func (c *Client) extrairTextoGCV(b64 string) (string, error) {
	payload := gcvRequest{
		Requests: []gcvAnnotateRequest{
			{
				Image: gcvImage{Content: b64},
				Features: []gcvFeature{
					{Type: "TEXT_DETECTION", MaxResults: 1},
				},
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("erro ao serializar payload GCV: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, gcvURL+"?key="+c.gcvAPIKey, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("erro na requisição GCV: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("erro ao ler resposta GCV: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GCV retornou HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var gcvResp gcvResponse
	if err := json.Unmarshal(respBody, &gcvResp); err != nil {
		return "", fmt.Errorf("erro ao parsear resposta GCV: %w", err)
	}

	if len(gcvResp.Responses) == 0 || len(gcvResp.Responses[0].TextAnnotations) == 0 {
		if len(gcvResp.Responses) > 0 && gcvResp.Responses[0].Error != nil {
			return "", fmt.Errorf("erro GCV: %s", gcvResp.Responses[0].Error.Message)
		}
		return "", nil
	}

	return gcvResp.Responses[0].TextAnnotations[0].Description, nil
}

// --- Parser regex (fallback sem IA) ---

var (
	rePreco      = regexp.MustCompile(`\b(\d{1,3}(?:\.\d{3})*(?:[,]\d{2})|(?:\d+)[,](\d{2}))\b`)
	reEAN        = regexp.MustCompile(`\b(\d{8}|\d{12,14})\b`)
	reQtdUnidade = regexp.MustCompile(`(?i)\b(\d+(?:[,.]\d+)?)\s*(?:un|kg|lt|pc|cx|pç|gr|g\b|ml|l\b)\b`)
	reItemNum    = regexp.MustCompile(`^\d{1,4}\s+`)
	reIgnorar    = regexp.MustCompile(`(?i)(cnpj|cpf|total|troco|desconto|subtotal|pagamento|dinheiro|` +
		`credito|debito|pix|cupom|fiscal|nfc-e|nf-e|danfe|serie|data|hora|caixa|` +
		`operador|coo|ecf|obrigado|volte|telefone|endereco|logradouro|municipio|` +
		`emitente|www\.|http|cod\s*bar|codigo\s*bar|acrescimo|troco|` +
		`[-=*]{3,}|^\s*\d+\s*$)`)
)

func parsearCupom(texto string) []ProdutoCupom {
	linhas := strings.Split(texto, "\n")
	var produtos []ProdutoCupom

	for _, linha := range linhas {
		linha = strings.TrimSpace(linha)
		if len(linha) < 4 || reIgnorar.MatchString(linha) {
			continue
		}

		precos := rePreco.FindAllString(linha, -1)
		if len(precos) == 0 {
			continue
		}

		preco := parsearPreco(precos[len(precos)-1])
		if preco <= 0 {
			continue
		}

		ean := ""
		resto := linha
		if m := reEAN.FindString(linha); m != "" {
			ean = m
			resto = strings.Replace(resto, m, " ", 1)
		}

		qtd := 1.0
		if m := reQtdUnidade.FindStringSubmatch(resto); m != nil {
			if v, err := strconv.ParseFloat(strings.Replace(m[1], ",", ".", 1), 64); err == nil && v > 0 {
				qtd = v
			}
			resto = reQtdUnidade.ReplaceAllString(resto, " ")
		}

		resto = rePreco.ReplaceAllString(resto, " ")
		resto = reItemNum.ReplaceAllString(resto, "")
		resto = regexp.MustCompile(`\s+[xX]\s+\d+`).ReplaceAllString(resto, " ")
		resto = regexp.MustCompile(`[|\\/*]`).ReplaceAllString(resto, " ")
		resto = regexp.MustCompile(`\s{2,}`).ReplaceAllString(strings.TrimSpace(resto), " ")

		nome := strings.ToUpper(strings.TrimSpace(resto))
		if len(nome) < 3 || !regexp.MustCompile(`[A-Za-zÀ-ú]`).MatchString(nome) {
			continue
		}

		produtos = append(produtos, ProdutoCupom{
			Nome:       nome,
			CodigoEAN:  ean,
			Preco:      preco,
			Quantidade: qtd,
		})
	}

	return produtos
}

func parsearPreco(s string) float64 {
	if strings.Contains(s, ",") {
		s = strings.ReplaceAll(s, ".", "")
		s = strings.ReplaceAll(s, ",", ".")
	}
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// --- helpers de mime type ---

func detectarMediaType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return "image/jpeg"
	}
}

// detectarMediaTypeDeBytes detecta o mime type pelos magic bytes do arquivo
func detectarMediaTypeDeBytes(data []byte) string {
	if len(data) < 4 {
		return "image/jpeg"
	}
	switch {
	case data[0] == 0xFF && data[1] == 0xD8:
		return "image/jpeg"
	case data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47:
		return "image/png"
	case data[0] == 0x47 && data[1] == 0x49 && data[2] == 0x46:
		return "image/gif"
	case len(data) >= 12 && string(data[8:12]) == "WEBP":
		return "image/webp"
	default:
		return "image/jpeg"
	}
}
