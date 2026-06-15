package mygene

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("request carried no User-Agent")
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := NewClient()
	c.Rate = 0 // no pacing in the test

	body, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "ok" {
		t.Errorf("body = %q, want %q", body, "ok")
	}
}

func TestGetRetriesOn503(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("recovered"))
	}))
	defer srv.Close()

	c := NewClient()
	c.Rate = 0
	c.Retries = 5

	start := time.Now()
	body, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "recovered" {
		t.Errorf("body = %q after retries", body)
	}
	if hits != 3 {
		t.Errorf("server saw %d hits, want 3", hits)
	}
	if time.Since(start) < 500*time.Millisecond {
		t.Error("retries did not back off")
	}
}

func TestSearchGenes(t *testing.T) {
	const resp = `{
		"total": 11025,
		"took": 2,
		"hits": [
			{
				"_id": "7157",
				"_score": 23.5,
				"entrezgene": 7157,
				"name": "tumor protein p53",
				"symbol": "TP53",
				"taxid": 9606,
				"type_of_gene": "protein-coding",
				"ensembl": {"gene": "ENSG00000141510"},
				"genomic_pos": {"chr": "17", "start": 7661779, "end": 7687550, "strand": -1}
			}
		]
	}`

	var capturedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	defer srv.Close()

	c := NewClient()
	c.Rate = 0
	// Point client at test server by temporarily swapping BaseURL via URL prefix.
	// We override the base by injecting the server URL directly in a sub-test client.

	origBase := BaseURL
	_ = origBase // keep reference to show intent

	// Wrap: monkeypatch the constant isn't possible directly; use a helper that
	// takes a base URL so we can test the logic without patching globals.
	genes, total, err := searchGenesAt(context.Background(), c, srv.URL+"/v3", "TP53", 5, "human")
	if err != nil {
		t.Fatal(err)
	}
	if total != 11025 {
		t.Errorf("total = %d, want 11025", total)
	}
	if len(genes) != 1 {
		t.Fatalf("len(genes) = %d, want 1", len(genes))
	}
	g := genes[0]
	if g.ID != "7157" {
		t.Errorf("ID = %q, want 7157", g.ID)
	}
	if g.Symbol != "TP53" {
		t.Errorf("Symbol = %q, want TP53", g.Symbol)
	}
	if g.EntrezGene != "7157" {
		t.Errorf("EntrezGene = %q, want 7157", g.EntrezGene)
	}
	if g.EnsemblGene != "ENSG00000141510" {
		t.Errorf("EnsemblGene = %q, want ENSG00000141510", g.EnsemblGene)
	}
	if g.TaxID != 9606 {
		t.Errorf("TaxID = %d, want 9606", g.TaxID)
	}
	if g.Chromosome != "17" {
		t.Errorf("Chromosome = %q, want 17", g.Chromosome)
	}
	if g.GeneType != "protein-coding" {
		t.Errorf("GeneType = %q, want protein-coding", g.GeneType)
	}
	if !strings.Contains(capturedURL, "species=human") {
		t.Errorf("URL %q missing species=human", capturedURL)
	}
	if !strings.Contains(capturedURL, "size=5") {
		t.Errorf("URL %q missing size=5", capturedURL)
	}
}

func TestGetGene(t *testing.T) {
	const resp = `{
		"_id": "7157",
		"_score": 1.0,
		"entrezgene": "7157",
		"name": "tumor protein p53",
		"symbol": "TP53",
		"taxid": 9606,
		"type_of_gene": "protein-coding",
		"ensembl": {"gene": "ENSG00000141510"},
		"genomic_pos": {"chr": "17", "start": 7661779, "end": 7687550, "strand": -1}
	}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v3/gene/") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	defer srv.Close()

	c := NewClient()
	c.Rate = 0

	g, err := getGeneAt(context.Background(), c, srv.URL+"/v3", "7157")
	if err != nil {
		t.Fatal(err)
	}
	if g.ID != "7157" {
		t.Errorf("ID = %q, want 7157", g.ID)
	}
	if g.Symbol != "TP53" {
		t.Errorf("Symbol = %q, want TP53", g.Symbol)
	}
	if g.EntrezGene != "7157" {
		t.Errorf("EntrezGene = %q, want 7157", g.EntrezGene)
	}
	if g.Start != 7661779 {
		t.Errorf("Start = %d, want 7661779", g.Start)
	}
	if g.End != 7687550 {
		t.Errorf("End = %d, want 7687550", g.End)
	}
}

func TestGetGeneRetry(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits < 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"_id":"1","symbol":"ACTB","taxid":9606}`))
	}))
	defer srv.Close()

	c := NewClient()
	c.Rate = 0
	c.Retries = 3

	g, err := getGeneAt(context.Background(), c, srv.URL+"/v3", "1")
	if err != nil {
		t.Fatal(err)
	}
	if g.Symbol != "ACTB" {
		t.Errorf("Symbol = %q, want ACTB", g.Symbol)
	}
	if hits != 2 {
		t.Errorf("server saw %d hits, want 2", hits)
	}
}

// searchGenesAt is a testable variant of SearchGenes that accepts an explicit base URL.
func searchGenesAt(ctx context.Context, c *Client, base, query string, limit int, species string) ([]*Gene, int, error) {
	if limit <= 0 {
		limit = 10
	}
	u := base + "/query?q=" + urlEncode(query) +
		"&size=" + intStr(limit) +
		"&fields=" + urlEncode(defaultFields)
	if species != "" {
		u += "&species=" + urlEncode(species)
	}

	body, err := c.Get(ctx, u)
	if err != nil {
		return nil, 0, err
	}
	return parseSearchResp(body)
}

// getGeneAt is a testable variant of GetGene that accepts an explicit base URL.
func getGeneAt(ctx context.Context, c *Client, base, geneID string) (*Gene, error) {
	u := base + "/gene/" + geneID + "?fields=" + urlEncode(defaultFields)
	body, err := c.Get(ctx, u)
	if err != nil {
		return nil, err
	}
	return parseGeneResp(body)
}
