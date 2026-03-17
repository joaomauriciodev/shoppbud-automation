package main

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"

	"shoppbud-automation/internal/auth"
	"shoppbud-automation/internal/config"
	"shoppbud-automation/internal/ocr"
	"shoppbud-automation/internal/product"
)

//go:embed static/index.html
var indexHTML []byte

const (
	configPath        = ".env"
	maxUploadBytes    = 10 << 20 // 10 MB
	defaultPort       = "8080"
	categoriaIDPadrao = "23769"
)

type appServer struct {
	ocrClient     *ocr.Client
	productClient *product.Client
	authClient    *auth.Client
	cfg           *config.Config
}

// --- request / response types ---

type errResponse struct {
	Erro string `json:"erro"`
}

type ocrResponse struct {
	Produtos []ocr.ProdutoCupom `json:"produtos"`
}

// produtoIntegrar estende ProdutoCupom com a categoria escolhida pelo usuário
type produtoIntegrar struct {
	Nome         string  `json:"nome"`
	CodigoEAN    string  `json:"codigoEAN"`
	Preco        float64 `json:"preco"`
	Quantidade   float64 `json:"quantidade"`
	CategoriaID  string  `json:"categoriaID"`
	ImagemBase64 string  `json:"imagemBase64,omitempty"`
	ImagemNome   string  `json:"imagemNome,omitempty"`
}

type integrarRequest struct {
	Produtos []produtoIntegrar `json:"produtos"`
}

type resultadoProduto struct {
	Nome string `json:"nome"`
	ID   int    `json:"id,omitempty"`
	Erro string `json:"erro,omitempty"`
}

type integrarResponse struct {
	Resultados []resultadoProduto `json:"resultados"`
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// --- handlers ---

func (s *appServer) ocrHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errResponse{"método não permitido"})
		return
	}

	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		writeJSON(w, http.StatusBadRequest, errResponse{"falha ao parsear multipart: " + err.Error()})
		return
	}

	file, _, err := r.FormFile("imagem")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errResponse{"campo 'imagem' não encontrado: " + err.Error()})
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxUploadBytes))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errResponse{"erro ao ler arquivo: " + err.Error()})
		return
	}

	produtos, err := s.ocrClient.ExtrairProdutosDeBytes(data)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errResponse{"erro no OCR: " + err.Error()})
		return
	}

	if produtos == nil {
		produtos = []ocr.ProdutoCupom{}
	}

	writeJSON(w, http.StatusOK, ocrResponse{Produtos: produtos})
}

func (s *appServer) categoriasHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errResponse{"método não permitido"})
		return
	}

	q := r.URL.Query()
	name := q.Get("name")
	page := q.Get("page")
	perPage := q.Get("perPage")
	if page == "" {
		page = "1"
	}
	if perPage == "" {
		perPage = "100"
	}

	apiURL := fmt.Sprintf("%s/category/?name=%s&page=%s&perPage=%s",
		s.cfg.BaseURL,
		url.QueryEscape(name),
		page,
		perPage,
	)

	req, err := s.authClient.AuthenticatedRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errResponse{"erro ao criar request: " + err.Error()})
		return
	}

	resp, err := s.authClient.Do(req)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errResponse{"erro ao buscar categorias: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errResponse{"erro ao ler resposta: " + err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}

type buscarProdutoResponse struct {
	Existe bool   `json:"existe"`
	ID     int    `json:"id,omitempty"`
	Nome   string `json:"nome,omitempty"`
}


func (s *appServer) buscarProdutoHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errResponse{"método não permitido"})
		return
	}

	barCode := strings.TrimSpace(r.URL.Query().Get("barCode"))
	if barCode == "" {
		writeJSON(w, http.StatusOK, buscarProdutoResponse{Existe: false})
		return
	}

	result, err := s.productClient.SearchByBarCode(barCode)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errResponse{"erro ao buscar produto: " + err.Error()})
		return
	}

	if result == nil || len(result.Data) == 0 {
		writeJSON(w, http.StatusOK, buscarProdutoResponse{Existe: false})
		return
	}

	first := result.Data[0]
	writeJSON(w, http.StatusOK, buscarProdutoResponse{
		Existe: true,
		ID:     first.ID,
		Nome:   first.Name,
	})
}


func (s *appServer) integrarHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errResponse{"método não permitido"})
		return
	}

	var req integrarRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errResponse{"payload inválido: " + err.Error()})
		return
	}

	var resultados []resultadoProduto

	for _, p := range req.Produtos {
		nome := strings.TrimSpace(p.Nome)
		if nome == "" || p.Preco <= 0 {
			resultados = append(resultados, resultadoProduto{
				Nome: nome,
				Erro: "nome e preço são obrigatórios",
			})
			continue
		}

		ean := p.CodigoEAN
		if ean == "" {
			ean = "0"
		}

		catID := strings.TrimSpace(p.CategoriaID)
		if catID == "" {
			catID = categoriaIDPadrao
		}

		precoCents := fmt.Sprintf("%d", int(math.Round(p.Preco*100)))

		// Resolve imagem (base64)
		var imgBytes []byte
		var imgFilename string
		if p.ImagemBase64 != "" {
			if data, decErr := base64.StdEncoding.DecodeString(p.ImagemBase64); decErr == nil {
				imgBytes = data
				imgFilename = p.ImagemNome
				if imgFilename == "" {
					imgFilename = "produto.jpg"
				}
			}
		}

		// Verifica se o produto já existe pelo código de barras
		var resultado *product.CreateResponse
		var err error
		searchResult, searchErr := s.productClient.SearchByBarCode(ean)
		if searchErr == nil && searchResult != nil && len(searchResult.Data) > 0 {
			// Produto existe: atualiza via PUT
			existing := searchResult.Data[0]
			updateReq := product.UpdateRequest{
				ID:                           existing.ID,
				Name:                         nome,
				CategoryID:                   catID,
				Description:                  nome,
				BarCode:                      ean,
				LastPriceInCents:             precoCents,
				ImportedExpectedFromCategory: true,
				ImageBytes:                   imgBytes,
				ImageFilename:                imgFilename,
			}
			resultado, err = s.productClient.Update(updateReq)
		} else {
			// Produto não existe: cria via POST
			createReq := product.CreateRequest{
				Name:                         nome,
				CategoryID:                   catID,
				Description:                  nome,
				BarCode:                      ean,
				LastPriceInCents:             precoCents,
				ImportedExpectedFromCategory: true,
				ImageBytes:                   imgBytes,
				ImageFilename:                imgFilename,
			}
			resultado, err = s.productClient.Create(createReq)
		}

		if err != nil {
			resultados = append(resultados, resultadoProduto{Nome: nome, Erro: err.Error()})
			continue
		}

		resultados = append(resultados, resultadoProduto{Nome: resultado.Name, ID: resultado.ID})
	}

	writeJSON(w, http.StatusOK, integrarResponse{Resultados: resultados})
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

// --- main ---

func main() {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("Erro ao carregar configuração: %v", err)
	}

	log.Printf("Autenticando com %s...", cfg.Email)
	authClient := auth.NewClient(cfg)
	if _, err := authClient.Login(); err != nil {
		log.Fatalf("Erro no login ShoppBud: %v", err)
	}
	log.Println("Login realizado com sucesso.")

	s := &appServer{
		ocrClient:     ocr.NewClient(cfg.GeminiAPIKey),
		productClient: product.NewClient(authClient, cfg.BaseURL),
		authClient:    authClient,
		cfg:           cfg,
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", indexHandler)
	mux.HandleFunc("/ocr", s.ocrHandler)
	mux.HandleFunc("/integrar", s.integrarHandler)
	mux.HandleFunc("/categorias", s.categoriasHandler)
	mux.HandleFunc("/buscar-produto", s.buscarProdutoHandler)

	addr := fmt.Sprintf(":%s", port)
	log.Printf("Servidor em http://localhost%s", addr)

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("Erro ao iniciar servidor: %v", err)
	}
}
